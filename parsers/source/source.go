// Package source extracts a conservative, language-agnostic code graph.
//
// It intentionally prefers incomplete evidence over invented relationships.
// The adapter recognizes common function declarations and import forms across
// Go, Java, TypeScript/JavaScript, Python, and similar languages. Language
// specific AST parsers can later replace this package while emitting the same
// IR.
package source

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/imohiyoko/oekaki/core"
)

var (
	goFunc         = regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	kotlinFunc     = regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+|protected\s+)*(?:suspend\s+)?fun\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	rustFunc       = regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	swiftFunc      = regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+|fileprivate\s+)*func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	rubyFunc       = regexp.MustCompile(`^\s*def\s+([A-Za-z_][A-Za-z0-9_!?=]*)`)
	luaFunc        = regexp.MustCompile(`^\s*function\s+([A-Za-z_][A-Za-z0-9_.:]*)\s*\(`)
	scriptFunc     = regexp.MustCompile(`^\s*(?:(?:export|default|async|public|private|protected|static)\s+)*function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	scriptMethod   = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|async|get|set)\s+)*([A-Za-z_][A-Za-z0-9_]*)\s*\([^;{}]*\)\s*(?::[^\{]+)?\{`)
	typedCurlyFunc = regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|static|final|abstract|virtual|override|sealed|synchronized|native|extern|inline|constexpr|friend|unsafe|async|const)\s+)*(?:[A-Za-z_][A-Za-z0-9_:.?]*(?:\s*<[^>{};()]+>)?(?:\s*\[\])?)(?:\s*[*&]+\s*|\s+)([A-Za-z_][A-Za-z0-9_]*)\s*\([^;{}]*\)\s*(?:const\s*)?(?:noexcept\s*)?(?::[^\{]+)?\{`)
	pythonFunc     = regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	goImport       = regexp.MustCompile(`^\s*import\s+(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"([^"]+)"`)
	goModuleDecl   = regexp.MustCompile(`^\s*module\s+(\S+)`)
	goImportOpen   = regexp.MustCompile(`^\s*import\s*(\()`)
	// An import path is a string, and Go allows either kind. A path holds
	// neither quote character, so one class covers both without having to
	// decide which one opened.
	goImportSingle = regexp.MustCompile("^\\s*import(?:\\s+([A-Za-z_.][A-Za-z0-9_]*))?\\s*[\"`]([^\"`]+)[\"`]")
	goImportMember = regexp.MustCompile("^\\s*(?:([A-Za-z_.][A-Za-z0-9_]*)\\s+)?[\"`]([^\"`]+)[\"`]")
	esFromImport   = regexp.MustCompile(`^\s*import\s+(?:[^"']+\s+from\s+)?["']([^"']+)["']`)
	quotedImport   = regexp.MustCompile(`^\s*(?:import|from)\s+["']([^"']+)["']`)
	pythonImport   = regexp.MustCompile(`^\s*from\s+([A-Za-z_][A-Za-z0-9_.]*)\s+import\s+`)
	usingImport    = regexp.MustCompile(`^\s*using\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	includeImport  = regexp.MustCompile(`^\s*#include\s+[<"]([^>"]+)[>"]`)
	requireImport  = regexp.MustCompile(`\brequire\s*[ (]["']([^"']+)["']`)
	rubyRequire    = regexp.MustCompile(`^\s*(?:require|load)\s+["']([^"']+)["']`)
	useImport      = regexp.MustCompile(`^\s*use\s+([^;]+);`)
	callExpr       = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

// Options controls source discovery.
type Options struct {
	Root           string
	IncludeUnknown bool
}

// Parser is an optional language-specific adapter. It receives the shared
// graph, absolute path, stable file node ID, and source root. A plugin can
// emit precise AST entities while the default parser remains dependency-free.
type Parser func(g *core.Graph, path, fileID, root string) error

var (
	parserMu        sync.RWMutex
	languageParsers = map[string]Parser{}
)

// Register adds or replaces a parser for an extension such as ".swift". The
// extension is normalized so registrations are consistent across platforms.
func Register(extension string, parser Parser) {
	extension = strings.ToLower(extension)
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	parserMu.Lock()
	languageParsers[extension] = parser
	parserMu.Unlock()
}

// ParseDir walks a source tree and emits a conservative code graph.
func ParseDir(root string) (*core.Graph, error) {
	return ParseDirWithOptions(root, Options{})
}

// ParseDirWithOptions walks a source tree with optional unknown-text support.
func ParseDirWithOptions(root string, opts Options) (*core.Graph, error) {
	if root == "" {
		return nil, fmt.Errorf("source root is empty")
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat source root: %w", err)
	}
	if !st.IsDir() {
		return ParseFiles([]string{root}, filepath.Dir(root))
	}
	var files []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if path != root && ignoredDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if supported(filepath.Ext(path)) || (opts.IncludeUnknown && textFile(path)) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking source root: %w", err)
	}
	sort.Strings(files)
	return ParseFiles(files, root)
}

func ParseFiles(files []string, root string) (*core.Graph, error) {
	g := core.New()
	g.Metadata = &core.Metadata{Source: "source", Generator: "oekaki/source"}
	if root == "" && len(files) > 0 {
		root = filepath.Dir(files[0])
	}
	fileIDs := map[string]string{}
	for _, path := range files {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		id := "file:" + rel
		fileIDs[path] = id
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "code_file", Name: rel, Attrs: map[string]any{"language": language(path)}, Source: &core.Source{File: rel}})
	}
	// What one file said about a type another file declares. It is collected
	// as the files are read and joined once they all have been; see
	// resolveTypes.
	scan := &typeScan{}
	for _, path := range files {
		if err := parseFile(g, path, fileIDs[path], root, scan); err != nil {
			return nil, err
		}
	}
	resolveTypes(g, scan)
	if err := addCrossFileCalls(g, root); err != nil {
		return nil, err
	}
	addSourceGroups(g)
	g.Axes = []core.Axis{{ID: "source", Label: "Source"}}
	g.Normalize()
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return g, nil
}

func addSourceGroups(g *core.Graph) {
	created := map[string]bool{}
	for i := range g.Nodes {
		if g.Nodes[i].Source == nil || g.Nodes[i].Source.File == "" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(g.Nodes[i].Source.File))
		if dir == "." || dir == "" {
			continue
		}
		var path []string
		var parent *string
		for _, part := range strings.Split(dir, "/") {
			path = append(path, part)
			key := strings.Join(path, "/")
			id := "source:dir:" + strings.ReplaceAll(key, "/", "::")
			if !created[id] {
				created[id] = true
				p := parent
				g.Groups = append(g.Groups, core.Group{ID: id, Axis: "source", Type: "directory", Label: part, Parent: p})
			}
			cur := id
			parent = &cur
		}
		g.Nodes[i].SetGroup("source", strings.Join(pathIDs(path), core.GroupSeparator))
	}
}

func pathIDs(parts []string) []string {
	ids := make([]string, 0, len(parts))
	for i := range parts {
		ids = append(ids, "source:dir:"+strings.ReplaceAll(strings.Join(parts[:i+1], "/"), "/", "::"))
	}
	return ids
}

func parseFile(g *core.Graph, path, fileID, root string, scan *typeScan) error {
	parserMu.RLock()
	custom := languageParsers[strings.ToLower(filepath.Ext(path))]
	parserMu.RUnlock()
	if custom != nil {
		// A registered parser emits whatever it likes, including types: it is
		// handed the graph, and the resolution afterwards works on names it
		// left in the graph rather than on anything it had to tell us.
		return custom(g, path, fileID, root)
	}
	if strings.EqualFold(filepath.Ext(path), ".go") {
		return parseGoFile(g, path, fileID, root, scan)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var lines []string
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	if err := s.Err(); err != nil {
		return err
	}
	lang := language(path)
	braceDelimited := isBraceDelimitedLanguage(lang)
	codeLines := sanitizeSource(lines, lang)
	// Which lines declare a method, and on what. A method's node has to say
	// whose it is, the way Go's receiver naming already does: one file with two
	// classes that both declare `run` has two methods, and a single node named
	// `run` made them one — every class in the file declaring the same box, and
	// clicking a member taking the reader to another class's page.
	types := scanTypes(g, scan, lines, codeLines, fileID, filepath.ToSlash(mustRel(root, path)), lang)

	funcs := map[string]string{}
	// What a call written by name resolves to. It is the name as written, so
	// it cannot be the same map: a call to `run` says `run`, whoever declared
	// it. The first declaration wins, which is what happened before there were
	// methods to tell apart.
	byName := map[string]string{}
	for line, text := range codeLines {
		raw, ok := functionName(text, lang)
		if ok {
			name := qualify(types.method, line, raw)
			id := fileID + "#" + name
			if _, exists := funcs[name]; !exists {
				funcs[name] = id
				if _, taken := byName[raw]; !taken {
					byName[raw] = id
				}
				g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "code_function", Name: name, Attrs: map[string]any{"language": language(path)}, Source: &core.Source{File: filepath.ToSlash(mustRel(root, path)), Line: line + 1}})
				g.Edges = append(g.Edges, core.Edge{From: fileID, To: id, Kind: core.EdgeIACRef, Relation: "contains", Attrs: map[string]any{"language": language(path), "reference_kind": "structural", "resolution": "static"}})
			}
		}
		if imp, ok := importName(lines[line], text); ok {
			id := "package:" + imp
			if !hasNode(g, id) {
				g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "code_package", Name: imp})
			}
			g.Edges = append(g.Edges, core.Edge{From: fileID, To: id, Kind: core.EdgeIACRef, Relation: "imports", Attrs: map[string]any{"line": line + 1, "reference_kind": "library", "resolution": "static"}})
		}
	}
	var current string
	depth, defIndent := 0, 0
	for line, text := range codeLines {
		declared := false
		// The name in a declaration is not a call, though it is written the
		// way one is. Resolving it drew the function calling whatever else
		// carried its name, which used to be itself and go unsaid.
		declaring := ""
		if raw, ok := functionName(text, lang); ok {
			declaring = raw
			current = funcs[qualify(types.method, line, raw)]
			depth = 0
			if braceDelimited {
				depth = braceDelta(text)
			}
			defIndent = indentation(lines[line])
			declared = true
		}
		if current != "" {
			for _, match := range callExpr.FindAllStringSubmatchIndex(text, -1) {
				if len(match) < 4 || match[2] < 0 || match[3] < 0 {
					continue
				}
				name, receiver := text[match[2]:match[3]], ""
				if name == declaring {
					declaring = ""
					continue
				}
				if on := callTarget.FindStringSubmatch(text[:match[0]]); len(on) > 1 {
					receiver = on[1]
				}
				if to, exists := resolveCall(funcs, byName, types.declaredIn(line), receiver, name, lang); exists && to != current {
					g.Edges = append(g.Edges, core.Edge{From: current, To: to, Kind: core.EdgeIACRef, Relation: "calls", Attrs: map[string]any{"language": language(path), "reference_kind": "application", "resolution": "static_same_file"}})
				}
			}
		}
		if current != "" && !declared {
			if braceDelimited {
				depth += braceDelta(text)
				if depth <= 0 && strings.Contains(text, "}") {
					current = ""
				}
			} else {
				if strings.TrimSpace(text) != "" && indentation(lines[line]) <= defIndent {
					current = ""
				}
				if strings.TrimSpace(text) != "" && indentation(text) <= defIndent && strings.HasPrefix(strings.TrimSpace(text), "end") {
					current = ""
				}
			}
		}
		if braceDelimited && current != "" && declared && depth <= 0 && strings.Contains(text, "}") {
			current = ""
		}
	}
	return nil
}

type sourceLexState struct {
	blockComment bool
	stringEnd    string
	multiline    bool
}

// sanitizeSource masks comments and string literals while preserving byte
// offsets, indentation, braces, declarations, and executable call syntax.
// Regex-based discovery can then remain conservative without treating examples
// in comments, log messages, or documentation strings as code.
func sanitizeSource(lines []string, language string) []string {
	state := sourceLexState{}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = sanitizeSourceLine(line, language, &state)
	}
	return out
}

func sanitizeSourceLine(line, language string, state *sourceLexState) string {
	masked := []byte(line)
	blank := func(start, end int) {
		for i := start; i < end && i < len(masked); i++ {
			masked[i] = ' '
		}
	}

	for i := 0; i < len(line); {
		if state.blockComment {
			end := strings.Index(line[i:], "*/")
			if end < 0 {
				blank(i, len(line))
				break
			}
			end += i + 2
			blank(i, end)
			i = end
			state.blockComment = false
			continue
		}

		if state.stringEnd != "" {
			end := quotedEnd(line, i, state.stringEnd)
			if end < 0 {
				blank(i, len(line))
				if !state.multiline {
					state.stringEnd = ""
				}
				break
			}
			blank(i, end)
			i = end
			state.stringEnd = ""
			state.multiline = false
			continue
		}

		if strings.HasPrefix(line[i:], "/*") {
			blank(i, i+2)
			i += 2
			state.blockComment = true
			continue
		}
		if isLineComment(line[i:], language) {
			blank(i, len(line))
			break
		}

		if strings.HasPrefix(line[i:], `"""`) || strings.HasPrefix(line[i:], `'''`) {
			state.stringEnd = line[i : i+3]
			state.multiline = true
			blank(i, i+3)
			i += 3
			continue
		}
		if line[i] == '"' || line[i] == '\'' || line[i] == '`' {
			state.stringEnd = line[i : i+1]
			state.multiline = line[i] == '`'
			blank(i, i+1)
			i++
			continue
		}
		i++
	}
	return string(masked)
}

func quotedEnd(line string, start int, delimiter string) int {
	for i := start; i < len(line); {
		if strings.HasPrefix(line[i:], delimiter) {
			return i + len(delimiter)
		}
		if line[i] == '\\' && len(delimiter) == 1 {
			i += 2
			continue
		}
		i++
	}
	return -1
}

func isLineComment(s, language string) bool {
	switch language {
	case "go", "js", "jsx", "mjs", "cjs", "ts", "tsx", "java", "kt", "kts", "scala", "sc", "rs", "swift", "php", "c", "cc", "cpp", "h", "hh", "hpp", "cs", "fs", "fsx", "dart", "m", "mm", "groovy", "gvy", "sol", "zig", "v", "proto":
		return strings.HasPrefix(s, "//")
	case "py", "rb", "sh", "bash", "zsh", "fish", "ps1", "pl", "pm", "r":
		return strings.HasPrefix(s, "#")
	case "sql", "lua", "hs", "lhs":
		return strings.HasPrefix(s, "--")
	}
	return false
}

func textFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return false
	}
	return !strings.ContainsRune(string(buf[:n]), '\x00')
}

func braceDelta(s string) int {
	return strings.Count(s, "{") - strings.Count(s, "}")
}

func indentation(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t"))
}

// qualify is the name a function goes into the graph under: its own, or the
// type's and its own, when a type declared it.
func qualify(owner map[int]string, line int, name string) string {
	if t := owner[line]; t != "" {
		return t + "." + name
	}
	return name
}

// resolveCall is what a call written by name refers to.
//
// Inside a type, its own method first. A call to `paint()` written in one class
// means that class's paint, and resolving it by the bare name reached whichever
// class happened to be declared first — including from a method's own
// declaration line, which the scanner reads for calls too, so a class was drawn
// calling another class's method of the same name for no reason at all.
//
// Outside a type, or when the type has no such method, the bare name: a call
// says the name it was written with, and the first declaration of it wins,
// which is what happened before there were methods to tell apart.
//
// Not every language lets a method be called by its bare name. A `render()`
// written in a Python or JavaScript method is the module's function — the
// method is `self.render()`, `this.render()` or PHP's `$this->render()` — and
// preferring the method there drew a class calling itself where the code
// called out of it.
//
// The same rule says what a call written on anything else cannot be. In those
// languages `other.render()` is a method of whatever `other` holds, which this
// parser has no way of knowing, and it is not the module's `render` — the bare
// name would have been written for that. So it names nothing, rather than the
// function that happens to share its name.
func resolveCall(funcs, byName map[string]string, inside, receiver, name, lang string) (string, bool) {
	if receiverRequired[languageFamily(lang)] && receiver != "" && !selfReceiver[receiver] {
		return "", false
	}
	if inside != "" {
		own := inside + "." + name
		first, second := own, name
		if receiverRequired[languageFamily(lang)] && receiver == "" {
			// Where a method cannot be reached by its bare name, the name
			// means the function of that name. The method is still the better
			// second guess than another class's method of the same name,
			// which is all the bare-name map could offer.
			first, second = name, own
		}
		if id, ok := funcs[first]; ok {
			return id, true
		}
		if id, ok := funcs[second]; ok {
			return id, true
		}
	}
	// A function of that name, before anybody's method. Reaching for the first
	// declaration in the file instead let a method win where a plain function
	// of the same name existed — so a call outside every class went to a
	// class's method, and the real function was left with nothing pointing at
	// it. Its own declaration line, which the scanner reads for calls too,
	// then drew an edge from it to the method.
	if id, ok := funcs[name]; ok {
		return id, true
	}
	id, ok := byName[name]
	return id, ok
}

func parseGoFile(g *core.Graph, path, fileID, root string, scan *typeScan) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	rel := filepath.ToSlash(mustRel(root, path))
	for _, imp := range f.Imports {
		name := strings.Trim(imp.Path.Value, `"`)
		id := "package:" + name
		if !hasNode(g, id) {
			g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "code_package", Name: name})
		}
		line := fset.Position(imp.Pos()).Line
		g.Edges = append(g.Edges, core.Edge{From: fileID, To: id, Kind: core.EdgeIACRef, Relation: "imports", Attrs: map[string]any{"line": line, "reference_kind": "library", "resolution": "static"}})
	}
	goTypes(g, scan, f, fset, fileID, rel)

	funcs := map[string]string{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			if receiver := goReceiverName(fn.Recv.List[0].Type); receiver != "" {
				name = receiver + "." + name
				// A method is declared beside its type as often as not — in
				// another file of the same package — so which type this is on
				// is settled after every file has been read.
				scan.methods = append(scan.methods, pendingMethod{
					receiver: receiver, function: fileID + "#" + name,
					dir: filepath.ToSlash(filepath.Dir(rel)),
				})
			}
		} else {
			funcs[fn.Name.Name] = fileID + "#" + name
		}
		id := fileID + "#" + name
		line := fset.Position(fn.Pos()).Line
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "code_function", Name: name, Attrs: map[string]any{"language": "go"}, Source: &core.Source{File: rel, Line: line}})
		g.Edges = append(g.Edges, core.Edge{From: fileID, To: id, Kind: core.EdgeIACRef, Relation: "contains"})
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		from := funcs[fn.Name.Name]
		if from == "" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if to, exists := funcs[ident.Name]; exists && to != from {
				g.Edges = append(g.Edges, core.Edge{From: from, To: to, Kind: core.EdgeIACRef, Relation: "calls", Attrs: map[string]any{"language": "go", "reference_kind": "application", "resolution": "static_same_file"}})
			}
			return true
		})
	}
	return nil
}

type sourceFunction struct {
	id, name, file, lang, scope string
	line                        int

	// method records that the declaration named a receiver: `Origin.Rank` is
	// reached through a value of that type and `Rank` is not.
	method bool
}

type sourceImport struct {
	module    string
	symbols   map[string]string // local name -> exported name
	namespace string
	wildcard  bool
}

type sourceFileInfo struct {
	lines, code []string
	lang, scope string
	imports     []sourceImport
}

type sourceCall struct {
	name, qualifier string

	// nested records that the qualifier was not written against the call:
	// `store.Default.Save(1)` calls a method on a package variable, not the
	// package function `store.Save`, and the two are told apart only by how
	// far apart they were written.
	nested bool
}

// addCrossFileCalls recovers the useful middle ground between a full
// language/type checker and a file-only picture. Calls across Python and
// JavaScript modules require an import that names the target; Go calls require
// the exact same directory and package declaration. Other languages retain the
// conservative explicit-package/directory boundary.
func addCrossFileCalls(g *core.Graph, root string) error {
	module := goModule(root)
	byFile := map[string][]sourceFunction{}
	for _, n := range g.Nodes {
		if n.Type != "code_function" || n.Source == nil || n.Source.File == "" || n.Source.Line == 0 {
			continue
		}
		name := n.Name
		method := false
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
			method = true
		}
		fn := sourceFunction{id: n.ID, name: name, file: n.Source.File, line: n.Source.Line, method: method}
		byFile[n.Source.File] = append(byFile[n.Source.File], fn)
	}

	files := map[string]sourceFileInfo{}
	for file, functions := range byFile {
		path := filepath.Join(root, filepath.FromSlash(file))
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("opening source file %s for cross-file analysis: %w", path, err)
		}
		var lines []string
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			f.Close()
			return err
		}
		f.Close()
		if len(lines) == 0 {
			continue
		}
		lang := language(file)
		code := sanitizeSource(lines, lang)
		files[file] = sourceFileInfo{
			lines:   lines,
			code:    code,
			lang:    lang,
			scope:   sourcePackage(file, lang, code),
			imports: sourceImports(lines, code, lang),
		}
		for i := range functions {
			functions[i].lang = lang
			functions[i].scope = files[file].scope
		}
		sort.Slice(functions, func(i, j int) bool { return functions[i].line < functions[j].line })
		byFile[file] = functions
	}

	goImportNames(files, module)

	byName := map[string][]sourceFunction{}
	for _, functions := range byFile {
		for _, fn := range functions {
			key := crossFileKey(fn.lang, fn.name)
			byName[key] = append(byName[key], fn)
		}
	}

	for file, functions := range byFile {
		source, ok := files[file]
		if !ok || len(source.lines) == 0 {
			continue
		}
		for i, fn := range functions {
			start := fn.line
			end := len(source.code)
			if i+1 < len(functions) {
				end = functions[i+1].line - 1
			}
			if start < 1 || start > len(source.code) {
				continue
			}
			if end > len(source.code) {
				end = len(source.code)
			}
			for _, line := range source.code[start:end] {
				for _, call := range crossFileCalls(line) {
					candidateByID := map[string]sourceFunction{}
					for _, targetName := range importedTargetNames(source, call) {
						for _, candidate := range byName[crossFileKey(fn.lang, targetName)] {
							candidateByID[candidate.id] = candidate
						}
					}
					var candidates []sourceFunction
					for _, candidate := range candidateByID {
						target, ok := files[candidate.file]
						if !ok || candidate.id == fn.id || !canResolveCrossFile(file, source, candidate.file, target, call, candidate.name, candidate.method, module) {
							continue
						}
						candidates = append(candidates, candidate)
					}
					if len(candidates) != 1 || hasEdge(g, fn.id, candidates[0].id, "calls") {
						continue
					}
					g.Edges = append(g.Edges, core.Edge{From: fn.id, To: candidates[0].id, Kind: core.EdgeIACRef, Relation: "calls", Attrs: map[string]any{
						"language": language(file), "reference_kind": "application", "resolution": "static_cross_file_unique", "confidence": 0.65,
					}})
				}
			}
		}
	}
	return nil
}

func crossFileKey(language, name string) string {
	return languageFamily(language) + "\x00" + name
}

func languageFamily(language string) string {
	switch language {
	case "js", "jsx", "mjs", "cjs", "ts", "tsx":
		return "javascript"
	default:
		return language
	}
}

// callTarget is what a call was written on: `self.render()`, `this.render()`,
// `$this->render()`.
var callTarget = regexp.MustCompile(`(\$?[A-Za-z_][A-Za-z0-9_]*)\s*(?:\.|->|::)\s*$`)

// receiverRequired are the languages where a bare name is never a method.
var receiverRequired = map[string]bool{"py": true, "javascript": true, "php": true}

// selfReceiver is a call written on the object whose method it is written in.
var selfReceiver = map[string]bool{"self": true, "this": true, "$this": true, "cls": true}

var selectorCall = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)(?:\s*\.\s*[A-Za-z_][A-Za-z0-9_]*)*\s*\.\s*$`)

func crossFileCalls(line string) []sourceCall {
	var calls []sourceCall
	for _, match := range callExpr.FindAllStringSubmatchIndex(line, -1) {
		if len(match) < 4 || match[2] < 0 || match[3] < 0 {
			continue
		}
		call := sourceCall{name: line[match[2]:match[3]]}
		if qualifier := selectorCall.FindStringSubmatch(line[:match[0]]); len(qualifier) > 1 {
			call.qualifier = qualifier[1]
			call.nested = strings.Count(qualifier[0], ".") > 1
		}
		calls = append(calls, call)
	}
	return calls
}

func importedTargetNames(source sourceFileInfo, call sourceCall) []string {
	names := map[string]bool{call.name: true}
	for _, imp := range source.imports {
		if call.qualifier != "" {
			if imp.namespace == call.qualifier {
				names[call.name] = true
			}
			continue
		}
		if exported, ok := imp.symbols[call.name]; ok && exported != "*" {
			names[exported] = true
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func canResolveCrossFile(callerFile string, caller sourceFileInfo, targetFile string, target sourceFileInfo, call sourceCall, targetName string, targetMethod bool, module string) bool {
	family := languageFamily(caller.lang)
	if languageFamily(target.lang) != family {
		return false
	}
	switch family {
	case "go":
		// The cross-package reading first, because it is the exact one: an
		// import path that names the target's directory, and a qualifier that
		// matches what this file calls that package by. Nothing is matched on
		// a name looking right — the module path comes from go.mod, and
		// without one there is no way to turn an import path into a
		// directory, so `import "net/http"` would reach a local package that
		// happens to be called http.
		if module != "" && call.qualifier != "" {
			want := module
			if dir := goScopeDir(target.scope); dir != "" && dir != "." {
				want += "/" + dir
			}
			for _, imp := range caller.imports {
				if imp.module != want {
					continue
				}
				// The alias when this file gave one, and otherwise the
				// package's own clause — read from the package rather than
				// guessed from the last path element, so a version suffix, a
				// dotted host path and a hyphenated directory are not special
				// cases.
				name := imp.namespace
				if name == "" {
					name = goPackageName(target.scope)
				}
				if call.qualifier != name {
					continue
				}
				// What stands between the package and the call says which
				// kind of declaration was reached. `store.Save(1)` is the
				// package's function and never its method; `store.Default
				// .Save(1)` is a method on a package variable and never the
				// function `store.Save`. Both are written down in the
				// declaration, so neither has to be guessed.
				return call.nested == targetMethod
			}
		}
		// Otherwise it is a call inside one's own package. A qualifier that
		// names an imported package is excluded: the caller's own function of
		// that name is not a candidate for `store.Save`, and offering it would
		// leave two — which are dropped, losing the call in exactly the estate
		// that needed it, because a package and a caller sharing a function
		// name is ordinary.
		// Only where the reading above could have found something. A tree that
		// declares no module gets no cross-package edges at all, so applying
		// the guard there would take same-package edges away and give nothing
		// back — leaving such a tree worse off than before any of this.
		if module != "" && importedAs(caller, call.qualifier) {
			return false
		}
		return caller.scope != "" && caller.scope == target.scope
	case "py", "javascript":
		for _, imp := range caller.imports {
			if !importNamesFile(callerFile, targetFile, family, imp.module) {
				continue
			}
			if call.qualifier != "" {
				if imp.namespace == call.qualifier && targetName == call.name {
					return true
				}
				continue
			}
			if exported, ok := imp.symbols[call.name]; ok && (exported == "*" || exported == targetName) {
				return true
			}
			if imp.wildcard && targetName == call.name {
				return true
			}
		}
		return false
	default:
		return caller.scope == target.scope
	}
}

var (
	pythonFromStatement   = regexp.MustCompile(`^\s*from\s+([.A-Za-z_][A-Za-z0-9_.]*)\s+import\s+(.+)$`)
	pythonImportStatement = regexp.MustCompile(`^\s*import\s+([A-Za-z_][A-Za-z0-9_.]*)(?:\s+as\s+([A-Za-z_][A-Za-z0-9_]*))?`)
	jsNamedImport         = regexp.MustCompile(`^\s*import\s*\{([^}]*)\}\s*from\s*["']([^"']+)["']`)
	jsNamespaceImport     = regexp.MustCompile(`^\s*import\s*\*\s*as\s*([A-Za-z_][A-Za-z0-9_]*)\s*from\s*["']([^"']+)["']`)
	jsDefaultImport       = regexp.MustCompile(`^\s*import\s*([A-Za-z_][A-Za-z0-9_]*)\s*from\s*["']([^"']+)["']`)
	jsDestructuredRequire = regexp.MustCompile(`^\s*(?:const|let|var)\s*\{([^}]*)\}\s*=\s*require\s*\(\s*["']([^"']+)["']`)
	jsBoundRequire        = regexp.MustCompile(`^\s*(?:const|let|var)\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*require\s*\(\s*["']([^"']+)["']`)
)

func sourceImports(lines, code []string, language string) []sourceImport {
	var imports []sourceImport
	var goScan goImportScan
	for i, raw := range lines {
		if i >= len(code) {
			break
		}
		switch languageFamily(language) {
		case "go":
			imports = append(imports, goScan.line(raw, code[i])...)
		case "py":
			if groups, ok := activeGroups(raw, code[i], pythonFromStatement); ok {
				symbols, wildcard := importBindings(groups[1], "as")
				imports = append(imports, sourceImport{module: groups[0], symbols: symbols, wildcard: wildcard})
				// In `from . import utils`, utils is a submodule namespace,
				// not only a symbol exported by the current package.
				if strings.Trim(groups[0], ".") == "" {
					locals := make([]string, 0, len(symbols))
					for local := range symbols {
						locals = append(locals, local)
					}
					sort.Strings(locals)
					for _, local := range locals {
						imports = append(imports, sourceImport{module: groups[0] + symbols[local], namespace: local})
					}
				}
				continue
			}
			if groups, ok := activeGroups(raw, code[i], pythonImportStatement); ok {
				alias := groups[1]
				if alias == "" {
					alias = strings.Split(groups[0], ".")[0]
				}
				imports = append(imports, sourceImport{module: groups[0], namespace: alias})
			}
		case "javascript":
			if groups, ok := activeGroups(raw, code[i], jsNamedImport); ok {
				symbols, _ := importBindings(groups[0], "as")
				imports = append(imports, sourceImport{module: groups[1], symbols: symbols})
				continue
			}
			if groups, ok := activeGroups(raw, code[i], jsNamespaceImport); ok {
				imports = append(imports, sourceImport{module: groups[1], namespace: groups[0]})
				continue
			}
			if groups, ok := activeGroups(raw, code[i], jsDefaultImport); ok {
				imports = append(imports, sourceImport{module: groups[1], symbols: map[string]string{groups[0]: "*"}})
				continue
			}
			if groups, ok := activeGroups(raw, code[i], jsDestructuredRequire); ok {
				symbols, _ := importBindings(groups[0], ":")
				imports = append(imports, sourceImport{module: groups[1], symbols: symbols})
				continue
			}
			if groups, ok := activeGroups(raw, code[i], jsBoundRequire); ok {
				imports = append(imports, sourceImport{module: groups[1], namespace: groups[0], symbols: map[string]string{groups[0]: "*"}})
			}
		}
	}
	return imports
}

func importBindings(raw, aliasSeparator string) (map[string]string, bool) {
	symbols := map[string]string{}
	wildcard := false
	raw = strings.TrimSpace(strings.Trim(raw, "(){}"))
	if comment := strings.Index(raw, "#"); comment >= 0 {
		raw = raw[:comment]
	}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == "*" {
			wildcard = true
			continue
		}
		if aliasSeparator == ":" {
			parts := strings.SplitN(item, ":", 2)
			exported, local := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[0])
			if len(parts) == 2 {
				local = strings.TrimSpace(parts[1])
			}
			if exported != "" && local != "" {
				symbols[local] = exported
			}
			continue
		}
		fields := strings.Fields(item)
		exported, local := fields[0], fields[0]
		if len(fields) >= 3 && fields[1] == aliasSeparator {
			local = fields[2]
		}
		symbols[local] = exported
	}
	return symbols, wildcard
}

func importNamesFile(callerFile, targetFile, family, module string) bool {
	resolved := resolveImportModule(callerFile, family, module)
	if resolved == "" {
		return false
	}
	for _, alias := range sourceModuleAliases(targetFile, family) {
		if resolved == alias {
			return true
		}
	}
	return false
}

func resolveImportModule(callerFile, family, module string) string {
	module = strings.TrimSpace(strings.Split(strings.Split(module, "?")[0], "#")[0])
	if module == "" {
		return ""
	}
	var resolved string
	if family == "py" {
		dots := 0
		for dots < len(module) && module[dots] == '.' {
			dots++
		}
		rest := strings.ReplaceAll(module[dots:], ".", "/")
		if dots > 0 {
			base := filepath.ToSlash(filepath.Dir(callerFile))
			for level := 1; level < dots; level++ {
				base = filepath.ToSlash(filepath.Dir(base))
			}
			resolved = filepath.ToSlash(filepath.Join(base, filepath.FromSlash(rest)))
		} else {
			resolved = rest
		}
	} else {
		// Bare JavaScript/TypeScript specifiers are package names. Resolving
		// them to a coincidentally named repository file would require
		// package.json/tsconfig semantics that this parser does not have.
		if family == "javascript" && !strings.HasPrefix(module, ".") {
			return ""
		}
		if strings.HasPrefix(module, ".") {
			resolved = filepath.ToSlash(filepath.Join(filepath.Dir(callerFile), filepath.FromSlash(module)))
		} else {
			resolved = filepath.ToSlash(filepath.Clean(filepath.FromSlash(module)))
		}
	}
	return trimSourceExtension(strings.TrimPrefix(resolved, "./"))
}

func sourceModuleAliases(file, family string) []string {
	module := trimSourceExtension(filepath.ToSlash(file))
	aliases := []string{module}
	base := filepath.Base(module)
	if (family == "py" && base == "__init__") || (family == "javascript" && base == "index") {
		aliases = append(aliases, filepath.ToSlash(filepath.Dir(module)))
	}
	return aliases
}

func trimSourceExtension(module string) string {
	for _, ext := range []string{".py", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx"} {
		if strings.HasSuffix(strings.ToLower(module), ext) {
			return module[:len(module)-len(ext)]
		}
	}
	return module
}

var (
	packageDecl   = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;?`)
	namespaceDecl = regexp.MustCompile(`^\s*namespace\s+([A-Za-z_][A-Za-z0-9_.]*)`)
)

// sourcePackage is intentionally conservative. Go includes both directory and
// declared package so an external foo_test package cannot resolve unqualified
// names from foo merely because the files are adjacent.
func sourcePackage(file, language string, lines []string) string {
	for _, line := range lines {
		switch language {
		case "go":
			if match := packageDecl.FindStringSubmatch(line); len(match) > 1 {
				return filepath.ToSlash(filepath.Dir(file)) + "\x00" + match[1]
			}
		case "java", "kt", "kts", "scala", "sc":
			if match := packageDecl.FindStringSubmatch(line); len(match) > 1 {
				return match[1]
			}
		case "cs":
			if match := namespaceDecl.FindStringSubmatch(line); len(match) > 1 {
				return match[1]
			}
		}
	}
	return filepath.ToSlash(filepath.Dir(file))
}

// goImportScan reads a file's Go imports a line at a time.
//
// It carries state because an import block does: a path is only an import
// because of the brackets it sits inside, and whether those brackets are open
// was decided several lines earlier. Two kinds of nesting matter and both are
// tracked rather than guessed at per line — the block, and a comment inside it.
type goImportScan struct {
	grouped bool
	comment bool
}

// line returns the imports one line declares.
//
// raw is the line as written and code is the masked copy. Both are needed:
// masking blanks every string and an import path is a string, so the path can
// only be read from raw — while whether a line is code at all can only be
// asked of the mask.
func (s *goImportScan) line(raw, code string) []sourceImport {
	if s.grouped {
		return s.inBlock(raw)
	}
	// A block only opens on active code, so `import (` written inside a string
	// or a comment opens nothing.
	if groups, ok := activeGroups(raw, code, goImportOpen); ok {
		s.grouped = true
		// Everything after the bracket belongs to the block, including a whole
		// one-line group: `import ("fmt")` declares an import and closes again
		// before the line ends.
		if at := strings.Index(raw, groups[0]); at >= 0 {
			return s.inBlock(raw[at+len(groups[0]):])
		}
		return nil
	}
	if groups, ok := activeGroups(raw, code, goImportSingle); ok {
		return []sourceImport{goImportSpec(groups[0], groups[1])}
	}
	return nil
}

// inBlock reads what is left of a line inside a grouped import.
//
// The masked copy is no help here — every member is a string and masks to
// whitespace — so the comment a member could be hiding in is tracked instead.
// Go allows a comment between the brackets, and an import inside one is not an
// import however much it looks like the line above it.
func (s *goImportScan) inBlock(raw string) []sourceImport {
	if s.comment {
		closed := strings.Index(raw, "*/")
		if closed < 0 {
			return nil
		}
		s.comment = false
		raw = raw[closed+2:]
	}
	// A comment that opens and closes on this line is taken out of it rather
	// than ending the reading: `/* note */ "…/store"` is an import with a note
	// in front of it, and the import is the part that matters.
	for {
		opened := strings.Index(raw, "/*")
		// Whichever comment starts first is the one that starts. A `/*`
		// written inside a line comment — `// TODO: /* drop this later` —
		// opens nothing, and reading it as a block comment swallowed the rest
		// of the group and its closing bracket with it.
		if line := strings.Index(raw, "//"); line >= 0 && (opened < 0 || line < opened) {
			raw = raw[:line]
			break
		}
		if opened < 0 {
			break
		}
		closed := strings.Index(raw[opened+2:], "*/")
		if closed < 0 {
			s.comment = true
			raw = raw[:opened]
			break
		}
		raw = raw[:opened] + " " + raw[opened+2+closed+2:]
	}

	// The imports before the bracket rather than after it: a group whose last
	// member shares a line with its closing bracket — `"fmt")` — declares that
	// import and then ends. A semicolon is how Go writes more than one member
	// on a line, so each side of one is a member of its own; reading only the
	// first dropped the rest, and a dropped import is what sends a call to the
	// caller's own function of that name.
	var out []sourceImport
	for _, part := range strings.Split(raw, ";") {
		if groups := goImportMember.FindStringSubmatch(part); len(groups) > 2 {
			out = append(out, goImportSpec(groups[1], groups[2]))
		}
		if strings.Contains(part, ")") {
			// Both kinds of nesting end here. An unterminated comment that
			// opened after the bracket — `) /* a note about the group` — is
			// outside the group, and carrying it into the next `import (`
			// swallowed that whole declaration. The mask already knows where
			// comments are, so nothing here has to remember one.
			s.grouped = false
			s.comment = false
			break
		}
	}
	return out
}

// goImportSpec is one Go import line: the path it names, and the alias this
// file gave it.
//
// An absent alias stays absent rather than becoming the last element of the
// path. A package's name is its package clause, and `gopkg.in/yaml.v3`,
// `.../store/v2` and `.../go-bar` are all imported under names the path does
// not spell — so the name is read from the package being imported, where it
// is written.
func goImportSpec(alias, module string) sourceImport {
	return sourceImport{module: module, namespace: alias}
}

// goPackageName takes the package clause out of a Go scope, which carries the
// directory as well so that two directories declaring one package name stay
// apart.
func goPackageName(scope string) string {
	if _, name, found := strings.Cut(scope, "\x00"); found {
		return name
	}
	return ""
}

// importedAs reports whether a qualifier names a package this file imported.
//
// The name is the one the file uses: an alias where it gave one, the package
// clause where the import is in this tree (filled in by goImportNames), and
// otherwise the last element of the path, less a major-version suffix. That
// last one is a guess, but it is
// only reached for a package outside the tree, which is never a candidate
// anyway — and this has to agree with what the resolver calls a package, or
// the guard fails to fire exactly where the resolver did find the import and
// the caller's own function of that name is left crowding it out.
func importedAs(caller sourceFileInfo, qualifier string) bool {
	if qualifier == "" {
		return false
	}
	for _, imp := range caller.imports {
		// A blank or a dot import binds no identifier of its own, so no
		// qualifier can be naming it. Counting `_ "…/store"` as one took
		// `store.Save` away from a file that had its own Save and meant it.
		if imp.namespace == "_" || imp.namespace == "." {
			continue
		}
		if imp.namespace != "" {
			if imp.namespace == qualifier {
				return true
			}
			continue
		}
		if goImportBase(imp.module) == qualifier {
			return true
		}
	}
	return false
}

// goImportNames fills in what each in-tree import is called, once every file
// has been read.
//
// A package's name is its package clause, and the clause lives in the package
// being imported rather than in the file importing it — so it cannot be known
// while that file is being read alone. Writing it down here is what lets the
// resolver and importedAs ask one question instead of two: `.../store/v2`,
// `.../go-store` and `gopkg.in/yaml.v3` are all imported under names their
// path does not spell, and two readings that disagree about the name is how a
// call gets dropped.
func goImportNames(files map[string]sourceFileInfo, module string) {
	if module == "" {
		return
	}
	clause := map[string]string{}
	for _, info := range files {
		if languageFamily(info.lang) != "go" {
			continue
		}
		name := goPackageName(info.scope)
		// A directory holds one importable package, and may hold its external
		// test package as well. `builds_test` is not what `.../builds` is
		// imported as, and letting it win the directory renamed the package
		// for every file importing it.
		if name == "" || strings.HasSuffix(name, "_test") {
			continue
		}
		dir := goScopeDir(info.scope)
		if have, ok := clause[dir]; ok && have != name {
			// Not something Go allows, so there is nothing to choose between.
			clause[dir] = ""
			continue
		}
		clause[dir] = name
	}
	for _, info := range files {
		if languageFamily(info.lang) != "go" {
			continue
		}
		for i, imp := range info.imports {
			if imp.namespace != "" {
				continue
			}
			dir, ok := goImportDir(imp.module, module)
			if !ok {
				continue
			}
			if name := clause[dir]; name != "" {
				info.imports[i].namespace = name
			}
		}
	}
}

// goImportDir turns an import path into the directory it names, for the
// imports this tree's module covers.
func goImportDir(imported, module string) (string, bool) {
	switch {
	case imported == module:
		return ".", true
	case strings.HasPrefix(imported, module+"/"):
		return imported[len(module)+1:], true
	default:
		return "", false
	}
}

// goMajorVersion is the major-version element of a module path.
var goMajorVersion = regexp.MustCompile(`^v[0-9]+$`)

// goImportBase guesses what a package outside this tree is called from its
// path, which is all there is to go on: its package clause is not in the tree.
//
// A major-version suffix is not part of the name. `.../go-redis/redis/v8` is
// imported as redis and `gopkg.in/yaml.v3` as yaml — both conventions rather
// than syntax, but they are the two the ecosystem actually writes, and a guess
// that calls them `v8` and `yaml.v3` fails to recognise the package and hands
// `redis.NewClient` to whatever local function shares the name.
func goImportBase(module string) string {
	base := path.Base(module)
	if goMajorVersion.MatchString(base) {
		if dir := path.Dir(module); dir != "." && dir != "/" {
			base = path.Base(dir)
		}
	}
	if i := strings.LastIndex(base, "."); i > 0 && goMajorVersion.MatchString(base[i+1:]) {
		base = base[:i]
	}
	return base
}

// goScopeDir takes the directory out of a Go scope.
func goScopeDir(scope string) string {
	if dir, _, found := strings.Cut(scope, "\x00"); found {
		return dir
	}
	return ""
}

// goModule reads the module path a tree declares, which is the only thing that
// turns an import path into a directory. A tree with no go.mod gets no
// cross-package calls: the alternative is matching on the last path element,
// and that draws `net/http` at a local package called http.
func goModule(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		match := goModuleDecl.FindStringSubmatch(line)
		if len(match) < 2 {
			continue
		}
		// go.mod allows the path to be quoted. A quoted one kept its quotes
		// and then matched no import path, which does not fail loudly: the
		// tree silently becomes one with no module and loses every
		// cross-package call.
		if unquoted, err := strconv.Unquote(match[1]); err == nil {
			return unquoted
		}
		return match[1]
	}
	return ""
}

func hasEdge(g *core.Graph, from, to, relation string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Relation == relation {
			return true
		}
	}
	return false
}

func goReceiverName(expr ast.Expr) string {
	switch receiver := expr.(type) {
	case *ast.Ident:
		return receiver.Name
	case *ast.StarExpr:
		return goReceiverName(receiver.X)
	case *ast.IndexExpr:
		return goReceiverName(receiver.X)
	case *ast.IndexListExpr:
		return goReceiverName(receiver.X)
	default:
		return ""
	}
}

func functionName(s, language string) (string, bool) {
	for _, r := range []*regexp.Regexp{kotlinFunc, rustFunc, swiftFunc, rubyFunc, luaFunc, goFunc, pythonFunc} {
		if m := r.FindStringSubmatch(s); len(m) > 1 {
			return m[1], true
		}
	}
	if isScriptLanguage(language) {
		for _, r := range []*regexp.Regexp{scriptFunc, scriptMethod} {
			if m := r.FindStringSubmatch(s); len(m) > 1 && !controlFlowName(m[1]) {
				return m[1], true
			}
		}
	}
	if isTypedCurlyLanguage(language) {
		if m := typedCurlyFunc.FindStringSubmatch(s); len(m) > 1 && !controlFlowName(m[1]) && !startsWithKeyword(s, "new") {
			return m[1], true
		}
	}
	return "", false
}

func isScriptLanguage(language string) bool {
	switch language {
	case "js", "jsx", "ts", "tsx", "mjs", "cjs", "php":
		return true
	}
	return false
}

func isTypedCurlyLanguage(language string) bool {
	switch language {
	case "java", "c", "cc", "cpp", "h", "hh", "hpp", "cs", "dart", "m", "mm", "groovy", "gvy", "sol", "zig", "v":
		return true
	}
	return false
}

func isBraceDelimitedLanguage(language string) bool {
	if isScriptLanguage(language) || isTypedCurlyLanguage(language) {
		return true
	}
	switch language {
	case "kt", "kts", "rs", "swift":
		return true
	}
	return false
}

func controlFlowName(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "catch", "else", "do", "try", "with", "foreach", "synchronized":
		return true
	}
	return false
}

func startsWithKeyword(line, keyword string) bool {
	fields := strings.Fields(line)
	return len(fields) > 0 && fields[0] == keyword
}

func importName(raw, code string) (string, bool) {
	for _, pattern := range []*regexp.Regexp{
		goImport, esFromImport, quotedImport, pythonImport,
		usingImport, includeImport, requireImport, rubyRequire, useImport,
	} {
		if name, ok := activeCapture(raw, code, pattern); ok {
			return name, true
		}
	}
	return "", false
}

// activeCapture reads capture text from the original line, but only when the
// same match starts in lexically active code. String contents are needed for
// import paths, so matching the fully-masked line alone would lose the value;
// checking the corresponding span prevents comments, ordinary strings, and
// documentation strings from inventing imports.
func activeCapture(raw, code string, pattern *regexp.Regexp) (string, bool) {
	groups, ok := activeGroups(raw, code, pattern)
	if !ok || len(groups) == 0 {
		return "", false
	}
	return groups[0], true
}

func activeGroups(raw, code string, pattern *regexp.Regexp) ([]string, bool) {
	for _, match := range pattern.FindAllStringSubmatchIndex(raw, -1) {
		if len(match) < 4 || !hasActiveCode(code, match[0], match[1]) {
			continue
		}
		groups := make([]string, 0, len(match)/2-1)
		for i := 2; i+1 < len(match); i += 2 {
			if match[i] < 0 || match[i+1] < 0 {
				groups = append(groups, "")
				continue
			}
			groups = append(groups, raw[match[i]:match[i+1]])
		}
		return groups, true
	}
	return nil, false
}

func hasActiveCode(code string, start, end int) bool {
	if start < 0 || start >= len(code) {
		return false
	}
	if end > len(code) {
		end = len(code)
	}
	for _, char := range code[start:end] {
		if !strings.ContainsRune(" \t\r\n", char) {
			return true
		}
	}
	return false
}
func hasNode(g *core.Graph, id string) bool {
	for _, n := range g.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}
func mustRel(root, path string) string { r, _ := filepath.Rel(root, path); return r }
func ignoredDir(s string) bool {
	return s == ".git" || s == "node_modules" || s == "vendor" || s == "dist" || s == "build" || strings.HasPrefix(s, ".")
}
func supported(ext string) bool {
	switch strings.ToLower(ext) {
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".java", ".kt", ".kts", ".scala", ".sc", ".rs", ".rb", ".php", ".c", ".cc", ".cpp", ".h", ".hh", ".hpp", ".cs", ".fs", ".fsx", ".swift", ".m", ".mm", ".dart", ".clj", ".cljs", ".ex", ".exs", ".erl", ".hrl", ".hs", ".lhs", ".lua", ".pl", ".pm", ".r", ".R", ".jl", ".sql", ".sh", ".bash", ".zsh", ".fish", ".ps1", ".groovy", ".gvy", ".sol", ".zig", ".nim", ".v", ".asm", ".s", ".proto", ".graphql", ".gql":
		return true
	}
	return false
}
func language(path string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}
