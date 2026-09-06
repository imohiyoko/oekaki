package source

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

// Types, and the things declared on them.
//
// # Why this is here at all
//
// A code graph of files, functions and packages answers "what calls what". It
// cannot answer "what is this thing" — and a class diagram, and most of what
// people mean by "the design", are questions about types. Without a type in
// the IR there is nothing for those diagrams to be derived from, and a picture
// drawn anyway would be boxes labelled from filenames.
//
// # What is recorded, and what is not
//
// A type is recorded where it is declared. What it declares — its methods — is
// an edge to the function node that already existed, because a method is not a
// second thing: it is the one function, seen from the type's side.
//
// A relation to another type is recorded only when the other type is one this
// parser also read. A base class from a library nobody handed us is a name,
// and a box drawn from a name is a box nobody can open.
//
// The relations are the ones a declaration states outright. Nothing here
// infers that a type satisfies an interface by comparing method sets. That is
// a type checker's work, it is wrong more often than not without one, and a
// wrong arrow in a design diagram is worse than a missing one.
const (
	// NodeType is a class, struct, interface, enum, trait or alias.
	NodeType = "code_type"

	// RelationDeclares joins a type to a function declared on it.
	RelationDeclares = "declares"

	// RelationExtends is a base a declaration named: `extends B`, a Python
	// base list, or the colon form of Kotlin, Swift, Scala and C#.
	RelationExtends = "extends"

	// RelationImplements is an explicit `implements` clause. Only languages
	// that spell it separately produce one; where a declaration writes bases
	// and interfaces the same way, this parser cannot tell them apart and
	// says extends rather than guessing which was meant.
	RelationImplements = "implements"

	// RelationEmbeds is Go's embedding: a struct field or interface element
	// with no name of its own.
	RelationEmbeds = "embeds"

	// RelationHasField is a field whose type is another type here. The field
	// names ride on the edge, because two fields of the same type are one
	// line in a drawing and two facts about it.
	RelationHasField = "has_field"
)

var (
	typeDecl  = regexp.MustCompile(`^\s*(?:(?:export|default|public|private|protected|internal|abstract|final|sealed|static|open|data|pub|partial|case)\s+)*(class|interface|struct|enum|trait|protocol|record|type)\s+([A-Za-z_][A-Za-z0-9_]*)(.*)$`)
	pyClass   = regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(([^)]*)\))?\s*:`)
	extendsRe = regexp.MustCompile(`\bextends\s+([A-Za-z_][A-Za-z0-9_.]*)`)
	implsRe   = regexp.MustCompile(`\bimplements\s+([^{;]+)`)
	colonRe   = regexp.MustCompile(`\b(?:class|interface|struct|enum|trait|protocol|record)\s+[A-Za-z_][A-Za-z0-9_]*\s*(?:\([^)]*\))?\s*:\s*([^{]+)`)
	baseName  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.]*)`)
	generics  = regexp.MustCompile(`<[^<>]*>`)
)

// declarationKeywords are the words that may follow a type's name and still be
// part of its declaration. Anything else that looks like a name there means
// this was never a declaration: `struct sockaddr_in addr;` declares a
// variable, and reading it as a type produced a box for a type this file never
// wrote, with every function in the file hanging off it.
var declarationKeywords = map[string]bool{
	"extends": true, "implements": true, "where": true, "permits": true,
	"is": true, "of": true, "with": true, "derives": true,
}

// notAName is what a type is never called. `export default class extends Base`
// declares an anonymous class, and the first word after `class` is a keyword
// rather than its name — reading it as one made a type called "extends" that
// extended something.
var notAName = map[string]bool{
	"extends": true, "implements": true, "where": true, "permits": true,
	"class": true, "interface": true, "struct": true, "enum": true,
	"trait": true, "protocol": true, "record": true, "type": true,
}

// typeScan is what one pass over the files could not settle on its own.
//
// A relation names a type, and the type it names is very often declared in
// another file: a base class beside its subclass, a Go method beside the
// struct it is on. Resolving it needs every file read first, so the parse
// records the name and the joining happens once, at the end.
type typeScan struct {
	relations []pendingRelation
	methods   []pendingMethod

	// declared is the ids already recorded. The function scanner keeps a map
	// per file for the same reason: a linear walk of every node per
	// declaration turns a large tree into a quadratic one.
	declared map[string]bool
}

type pendingRelation struct {
	from     string // the type node the relation is on
	relation string
	target   string // the name the declaration wrote
	dir      string // the directory it was written in
	field    string // for has_field, the field's own name
}

type pendingMethod struct {
	receiver string // the type name the method is declared on
	function string // the function node id
	dir      string
}

// typeID is a type's node id.
//
// Types have a space of their own under the file because a class and a
// function may share a name in most languages, and two things sharing one id
// is the failure that silently merges them.
func typeID(fileID, name string) string { return fileID + "#type:" + name }

// declareType records a type and joins it to the file it was written in.
func declareType(g *core.Graph, scan *typeScan, fileID, name, kind, lang, rel string, line int) string {
	id := typeID(fileID, name)
	if scan.declared == nil {
		scan.declared = map[string]bool{}
	}
	if scan.declared[id] {
		return id
	}
	scan.declared[id] = true
	g.Nodes = append(g.Nodes, core.Node{
		ID: id, Type: NodeType, Name: name,
		Attrs:  map[string]any{"language": lang, "kind": kind},
		Source: &core.Source{File: rel, Line: line},
	})
	g.Edges = append(g.Edges, core.Edge{
		From: fileID, To: id, Kind: core.EdgeIACRef, Relation: "contains",
		Attrs: map[string]any{"language": lang, "reference_kind": "structural", "resolution": "static"},
	})
	return id
}

// scanTypes reads the type declarations of one file no AST parser handled, and
// the functions declared inside them.
//
// The scope tracking is the same shape as the call scanner's: a declaration
// opens a scope, and braces or indentation close it. A type declared inside
// another is not followed — it is rare enough that reading it wrong is worse
// than not reading it.
func scanTypes(g *core.Graph, scan *typeScan, lines, codeLines []string, fileID, rel, lang string) {
	braced := isBraceDelimitedLanguage(lang)
	dir := filepath.ToSlash(filepath.Dir(rel))

	current, currentName := "", ""
	depth, declIndent := 0, 0

	for line, text := range codeLines {
		name, kind, bases, ok := typeDeclaration(text, lang)
		if ok {
			current = declareType(g, scan, fileID, name, kind, lang, rel, line+1)
			currentName = name
			for _, b := range bases {
				scan.relations = append(scan.relations, pendingRelation{
					from: current, relation: b.relation, target: b.name, dir: dir,
				})
			}
			declIndent = indentation(lines[line])
			depth = 0
			if braced {
				depth = braceDelta(text)
				// A declaration with no body of its own, or one that opens and
				// closes on its own line, is over where it started. Leaving it
				// open made the next function in the file a method on it —
				// `class Order {}` followed by a function, a Rust unit struct,
				// a C forward declaration.
				if depth <= 0 {
					current, currentName = "", ""
				}
			}
			continue
		}
		if current == "" {
			continue
		}
		// Where the body ends is settled before what is on the line is read.
		// A `def` at the class's own indentation is the first thing after the
		// class, not the last thing in it, and attributing it first made every
		// following function a method.
		if !braced && strings.TrimSpace(text) != "" && indentation(lines[line]) <= declIndent {
			current, currentName = "", ""
			continue
		}

		// A function declared inside a type is a method on it.
		if fn, isFn := functionName(text, lang); isFn {
			scan.methods = append(scan.methods, pendingMethod{
				receiver: currentName, function: fileID + "#" + fn, dir: dir,
			})
		}

		if braced {
			depth += braceDelta(text)
			if depth <= 0 && strings.Contains(text, "}") {
				current, currentName = "", ""
			}
		}
	}
}

type declaredBase struct{ relation, name string }

// declares reports whether what follows a type's name still belongs to a
// declaration.
//
// A declaration is followed by its body, its bases, its parameters, an equals
// sign, a semicolon, or nothing at all. It is never followed by another name:
// that is a variable being declared, and `struct sockaddr_in addr;` is the
// shape that made this parser invent a type per C socket.
func declares(rest string) bool {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return true
	}
	if !isIdentifierStart(rest[0]) {
		return true
	}
	word := rest
	if at := strings.IndexFunc(word, notIdentifier); at >= 0 {
		word = word[:at]
	}
	return declarationKeywords[word]
}

func isIdentifierStart(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func notIdentifier(r rune) bool {
	switch {
	case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return true
}

// strip removes type parameters, innermost first, so that what is left is the
// declaration without its generics.
func strip(text string) string {
	for range 8 {
		next := generics.ReplaceAllString(text, "")
		if next == text {
			return text
		}
		text = next
	}
	return text
}

// typeDeclaration reads one line as a type declaration, and the bases it names.
func typeDeclaration(text, lang string) (name, kind string, bases []declaredBase, ok bool) {
	if lang == "py" {
		m := pyClass.FindStringSubmatch(text)
		if len(m) < 3 {
			return "", "", nil, false
		}
		for base := range strings.SplitSeq(m[2], ",") {
			// `object` is what every Python class descends from and nobody
			// draws. An arrow every box has is a line through the picture
			// that says nothing.
			if n := baseName.FindString(strings.TrimSpace(base)); n != "" && n != "object" {
				bases = append(bases, declaredBase{RelationExtends, n})
			}
		}
		return m[1], "class", bases, true
	}

	m := typeDecl.FindStringSubmatch(text)
	if len(m) < 4 {
		return "", "", nil, false
	}
	kind, name = m[1], m[2]
	if notAName[name] || !declares(m[3]) {
		return "", "", nil, false
	}
	if kind == "type" {
		kind = "alias"
	}

	// Type parameters come off before the bases are read. `class Box<T extends
	// Number>` bounds a parameter and names no base, and `implements
	// Map<String, Integer>` implements one interface rather than two — both
	// went wrong by reading the angle brackets as part of the declaration.
	text = strip(text)

	if e := extendsRe.FindStringSubmatch(text); len(e) > 1 {
		bases = append(bases, declaredBase{RelationExtends, e[1]})
	}
	if i := implsRe.FindStringSubmatch(text); len(i) > 1 {
		for one := range strings.SplitSeq(i[1], ",") {
			if n := baseName.FindString(strings.TrimSpace(one)); n != "" {
				bases = append(bases, declaredBase{RelationImplements, n})
			}
		}
	}
	// The colon form. Kotlin, Swift, Scala and C# write a base class and an
	// interface the same way, so this cannot tell generalization from
	// realization — and says extends rather than choosing one at random.
	//
	// Read from the stripped line, so a constraint inside angle brackets is
	// not mistaken for a base.
	if len(bases) == 0 {
		if c := colonRe.FindStringSubmatch(text); len(c) > 1 {
			for one := range strings.SplitSeq(c[1], ",") {
				if n := baseName.FindString(strings.TrimSpace(one)); n != "" && n != name {
					bases = append(bases, declaredBase{RelationExtends, n})
				}
			}
		}
	}
	return name, kind, bases, true
}

// resolveTypes joins what the files said about each other, once they have all
// been read.
//
// A name is resolved against the types declared in the same directory first: a
// directory is one package in Go and one module's worth of code nearly
// everywhere else, and a name means what it means locally. Failing that, a
// name exactly one type in the whole tree carries is that type — a name two
// types carry is dropped, because choosing one would draw an arrow to
// something nobody may have meant.
//
// Nothing unresolved is kept. A relation to a name is not a relation.
func resolveTypes(g *core.Graph, scan *typeScan) {
	if len(scan.relations) == 0 && len(scan.methods) == 0 {
		return
	}
	byDir := map[string][]string{}
	byName := map[string][]string{}
	for _, n := range g.Nodes {
		if n.Type != NodeType {
			continue
		}
		dir := ""
		if n.Source != nil {
			dir = filepath.ToSlash(filepath.Dir(n.Source.File))
		}
		byDir[dir+"\x00"+n.Name] = append(byDir[dir+"\x00"+n.Name], n.ID)
		byName[n.Name] = append(byName[n.Name], n.ID)
	}
	resolve := func(name, dir string) string {
		// A qualified name names another package, and this parser has no
		// notion of which package is which. Dropping the qualifier and
		// matching the bare name joined `*http.Client` to whatever local type
		// happened to be called Client — an arrow to something the declaration
		// never mentioned, in a document whose whole purpose is telling apart
		// what was claimed from what was seen.
		if strings.Contains(name, ".") {
			return ""
		}
		if ids := byDir[dir+"\x00"+name]; len(ids) == 1 {
			return ids[0]
		}
		if ids := byName[name]; len(ids) == 1 {
			return ids[0]
		}
		return ""
	}

	type edgeKey struct{ from, to, relation string }
	seen := map[edgeKey]bool{}
	fields := map[string][]string{}
	var out []core.Edge

	for _, r := range scan.relations {
		to := resolve(r.target, r.dir)
		if to == "" || to == r.from {
			continue
		}
		if r.field != "" {
			fields[r.from+"\x00"+to] = append(fields[r.from+"\x00"+to], r.field)
		}
		key := edgeKey{r.from, to, r.relation}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, core.Edge{
			From: r.from, To: to, Kind: core.EdgeIACRef, Relation: r.relation,
			Attrs: map[string]any{"reference_kind": "structural", "resolution": "static"},
		})
	}
	for i := range out {
		if out[i].Relation != RelationHasField {
			continue
		}
		names := fields[out[i].From+"\x00"+out[i].To]
		sort.Strings(names)
		out[i].Attrs["fields"] = strings.Join(dedupeSorted(names), ", ")
	}

	functions := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Type == "code_function" {
			functions[n.ID] = true
		}
	}
	for _, m := range scan.methods {
		from := resolve(m.receiver, m.dir)
		if from == "" || !functions[m.function] {
			continue
		}
		key := edgeKey{from, m.function, RelationDeclares}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, core.Edge{
			From: from, To: m.function, Kind: core.EdgeIACRef, Relation: RelationDeclares,
			Attrs: map[string]any{"reference_kind": "structural", "resolution": "static"},
		})
	}
	g.Edges = append(g.Edges, out...)
}

func dedupeSorted(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i > 0 && in[i-1] == s {
			continue
		}
		out = append(out, s)
	}
	return out
}
