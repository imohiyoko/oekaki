// Package tfsource builds a graph by reading Terraform source, without running
// Terraform.
//
// The other way in needs `terraform show -json`, which needs an initialised
// working directory and, for a state, credentials for every account. An estate
// spread over a hundred accounts is exactly the case where nobody has all of
// them, and it is also exactly the case worth drawing. This package reads what
// is committed instead.
//
// It reads two things and infers nothing else:
//
//	backend "s3" { key = ... }              a root module, and its identity
//	data "terraform_remote_state" { key }   that module reading another one
//
// The key is the identity because it is what Terraform itself uses to find the
// state, so two modules agree on a name exactly when Terraform does.
//
// The edge direction is "reads": A -> B means A reads B's state, which is to
// say removing B breaks A.
//
// Nodes are the root modules, not the accounts they belong to. Accounts are a
// grouping axis. Rolling modules up into accounts is a view — a useful one,
// and one views.Apply can produce — but a parser that only emitted accounts
// would have thrown away the thing the view is derived from.
package tfsource

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

// stateName is the attribute that names a state, per backend. It is the same
// attribute on both sides — a backend block says where this module's state
// goes, and a terraform_remote_state block says which state to read — so the
// two join on it.
//
// Backends absent from this table are ones whose configuration this package
// has not been shown; they are reported rather than guessed at.
var stateName = map[string]string{
	"s3":         "key",
	"azurerm":    "key",
	"oss":        "key",
	"gcs":        "prefix",
	"local":      "path",
	"consul":     "path",
	"pg":         "schema_name",
	"http":       "address",
	"kubernetes": "secret_suffix",
	// Terraform Cloud names a workspace instead, inside a nested block.
	"remote": "",
	"cloud":  "",
}

var (
	backendHead    = regexp.MustCompile(`backend\s+"([a-z0-9_]+)"\s*\{`)
	cloudHead      = regexp.MustCompile(`(?m)^\s*cloud\s*\{`)
	remoteHead     = regexp.MustCompile(`data\s+"terraform_remote_state"\s+"[^"]+"\s*\{`)
	workspaces     = regexp.MustCompile(`workspaces\s*\{`)
	providerHead   = regexp.MustCompile(`(?m)^\s*provider\s+"[^"]+"\s*\{`)
	assumeRoleHead = regexp.MustCompile(`assume_role\s*\{`)
	roleARN        = regexp.MustCompile(`\Aarn:aws:iam::(\d{12}):role/`)
)

// attr reads a literal string attribute out of a block body.
//
// The name may sit at the start of its own line or inside a one-line object —
// config = { key = "..." } is as valid as the block form. Requiring the line
// start reads one estate's house style as the language's grammar.
//
// The boundary before the name keeps workspace_key_prefix and the like out:
// there is no word boundary inside an identifier.
func attr(body, name string) (string, bool) {
	rx := regexp.MustCompile(`(?m)(?:^|[\s{])` + regexp.QuoteMeta(name) + `\s*=\s*"([^"]*)"`)
	if m := rx.FindStringSubmatch(body); m != nil {
		return m[1], true
	}
	return "", false
}

// mask blanks out everything that must not be read as structure: comments and
// heredoc bodies. Each is replaced by spaces of the same length, so every
// offset still lines up with the original text and the value of a real
// attribute is untouched.
//
// This is the whole reason reading HCL with expressions is safe enough here. A
// commented-out backend block is the most ordinary thing in a repository —
// somebody stopped using a module and left the block behind — and counting it
// invents a root module nobody deploys. A heredoc containing example
// configuration does the same.
func mask(text string) string {
	out := []byte(text)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	i, n := 0, len(out)
	for i < n {
		switch {
		case out[i] == '"':
			// Values live in strings, so they stay. Stepping over them keeps a
			// brace or a # inside one from being read as structure.
			i++
			for i < n && out[i] != '"' {
				if out[i] == '\\' {
					i++
				}
				i++
			}
		case out[i] == '#', i+1 < n && out[i] == '/' && out[i+1] == '/':
			start := i
			for i < n && out[i] != '\n' {
				i++
			}
			blank(start, i)
		case i+1 < n && out[i] == '/' && out[i+1] == '*':
			start := i
			i += 2
			for i+1 < n && (out[i] != '*' || out[i+1] != '/') {
				i++
			}
			i = min(i+2, n)
			blank(start, i)
		case i+1 < n && out[i] == '<' && out[i+1] == '<':
			j := i + 2
			if j < n && out[j] == '-' {
				j++
			}
			tag := j
			for j < n && (out[j] == '_' || ('A' <= out[j] && out[j] <= 'Z') ||
				('a' <= out[j] && out[j] <= 'z') || ('0' <= out[j] && out[j] <= '9')) {
				j++
			}
			if j == tag {
				i++
				continue
			}
			marker := string(out[tag:j])
			end := strings.Index(text[j:], "\n"+marker)
			if end < 0 {
				blank(i, n)
				return string(out)
			}
			stop := j + end + 1 + len(marker)
			blank(i, stop)
			i = stop
			continue
		}
		i++
	}
	return string(out)
}

// block returns the balanced body that starts at the brace ending head, and
// where it ends.
//
// Counting braces rather than matching to the first closing one: a backend can
// contain a nested block — Terraform Cloud's workspaces is one — and a
// non-greedy regex stops at the inner brace and reads half a block as the
// whole of it. Quoted strings are stepped over so a brace inside one does not
// count; comments and heredocs are already gone, see mask.
func block(text string, open int) (string, int) {
	depth, i, n := 0, open, len(text)
	start := -1
	for i < n {
		switch text[i] {
		case '"':
			i++
			for i < n && text[i] != '"' {
				if text[i] == '\\' {
					i++
				}
				i++
			}
		case '{':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start:i], i + 1
			}
		}
		i++
	}
	return "", n
}

// blocks yields the body of every block whose header matches rx.
func blocks(text string, rx *regexp.Regexp) []string {
	var out []string
	for _, loc := range rx.FindAllStringIndex(text, -1) {
		body, _ := block(text, loc[1]-1)
		out = append(out, body)
	}
	return out
}

// stateOf is the name a backend block gives its state, and whether the backend
// is one this package knows how to read.
func stateOf(kind, body string) (string, bool) {
	name, known := stateName[kind]
	if !known {
		return "", false
	}
	if name == "" {
		// Terraform Cloud: the workspace is the state.
		for _, ws := range blocks(body, workspaces) {
			if v, ok := attr(ws, "name"); ok {
				return v, true
			}
			if v, ok := attr(ws, "prefix"); ok {
				return v, true
			}
		}
		return "", true
	}
	v, ok := attr(body, name)
	return v, ok
}

// Module is one root module found in the tree.
type Module struct {
	Dir      string   // path relative to the scanned root
	Backend  string   // the backend type, e.g. s3 or gcs
	Key      string   // the name the backend gives this state: the identity
	Account  string   // the AWS account, when the source says which
	Requires []string // states this module reads
}

// Unknown is a backend whose configuration this package has not been shown.
// It is reported rather than skipped: a directory quietly left out looks the
// same as one that is not there.
type Unknown struct {
	Dir     string
	Backend string
}

// skipped are directories that are never this estate's own root modules.
// Vendored copies of other people's modules carry their own backend blocks,
// and counting those invents infrastructure nobody deployed.
var skipped = []string{".terraform", ".git", "node_modules"}

// Scan walks dir and returns the root modules it finds, ordered by key, along
// with the ones whose backend this package cannot read.
func Scan(dir string, conv *Conventions) ([]Module, []Unknown, error) {
	var out []Module
	var unknown []Unknown

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		for _, s := range skipped {
			if d.Name() == s {
				return filepath.SkipDir
			}
		}
		m, err := readModule(dir, path, conv)
		if err != nil {
			return err
		}
		switch {
		case m == nil:
		case m.Key == "":
			unknown = append(unknown, Unknown{Dir: m.Dir, Backend: m.Backend})
		default:
			out = append(out, *m)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	sort.Slice(unknown, func(i, j int) bool { return unknown[i].Dir < unknown[j].Dir })

	// Trimming a shared prefix can bring two keys together — states/vpc and
	// vpc become the same one. Downstream they would be one node, silently,
	// with one module's references attributed to the other. Say it here, where
	// both directories can still be named.
	seen := map[string]string{}
	for _, m := range out {
		if was, clash := seen[m.Key]; clash {
			return nil, nil, fmt.Errorf(
				"%s and %s both name their state %q once the shared prefix is removed; "+
					"they would be drawn as one module", was, m.Dir, m.Key)
		}
		seen[m.Key] = m.Dir
	}
	return out, unknown, nil
}

// readModule returns the module rooted at path, or nil when there is not one.
//
// Every .tf file in the directory is read, not only provider.tf: where the
// backend block lives is a matter of taste, and a scanner that guesses wrong
// silently reports a smaller estate than exists.
func readModule(root, path string, conv *Conventions) (*Module, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	// A repository that keeps examples or vendored copies alongside its own
	// modules can say what distinguishes its own. Counting the others reports
	// an estate larger than the one that exists, and every extra entry looks
	// like real infrastructure.
	if !conv.Owned(fileNames(entries)) {
		return nil, nil
	}
	sort.Strings(names)

	var whole strings.Builder
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filepath.Join(path, name), err)
		}
		whole.Write(body)
		whole.WriteString("\n")
	}
	text := mask(whole.String())

	kind, body := "", ""
	if loc := backendHead.FindStringSubmatchIndex(text); loc != nil {
		kind = text[loc[2]:loc[3]]
		body, _ = block(text, loc[1]-1)
	} else if loc := cloudHead.FindStringIndex(text); loc != nil {
		kind = "cloud"
		body, _ = block(text, loc[1]-1)
	} else {
		return nil, nil
	}

	name, ok := stateOf(kind, body)
	if _, known := stateName[kind]; !known {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		return &Module{Dir: filepath.ToSlash(rel), Backend: kind}, nil
	}
	if !ok || name == "" {
		// A backend that does not write down its state's name is one Terraform
		// is told about at init time. There is no name here to join on, so
		// there is nothing to say about it.
		return nil, nil
	}

	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	m := &Module{Dir: filepath.ToSlash(rel), Backend: kind, Key: conv.TrimKey(name)}

	seen := map[string]bool{}
	for _, body := range blocks(text, remoteHead) {
		// The block says which backend it reads, and that decides which
		// attribute carries the name. Assuming one backend would join two
		// estates' states on an attribute they do not share.
		want := kind
		if b, ok := attr(body, "backend"); ok {
			want = b
		}
		got, ok := stateOf(want, body)
		// A reference is trimmed the same way the key it points at is, or the
		// two halves of the join stop being the same string and every edge
		// becomes a dangling one.
		got = conv.TrimKey(got)
		if !ok || got == "" || seen[got] {
			continue
		}
		seen[got] = true
		m.Requires = append(m.Requires, got)
	}
	sort.Strings(m.Requires)
	m.Account = accountOf(text, conv)
	return m, nil
}

// accountOf is the account this module deploys into, when the source says so.
//
// Only role_arn inside an assume_role block counts. A role ARN can appear
// anywhere — a policy attachment naming another account's role is ordinary —
// and reading those makes the wrong answer more likely the more thorough the
// module is. A wrong account is worse than none: it does not leave a gap, it
// draws the module inside somebody else's estate.
//
// Estates that record the account some other way get nothing here rather than
// a guess. Saying where to look is a separate input.
func accountOf(text string, conv *Conventions) string {
	for _, prov := range blocks(text, providerHead) {
		for _, role := range blocks(prov, assumeRoleHead) {
			arn, ok := attr(role, "role_arn")
			if !ok {
				continue
			}
			if m := roleARN.FindStringSubmatch(arn); m != nil {
				return m[1]
			}
		}
	}
	// Nothing in the provider block names an account outright. Whether
	// anything else in this repository does is not something to guess at, so
	// it is only read when somebody has said where to look.
	return conv.account(text)
}

// Graph turns scanned modules into the IR.
//
// A reference to a key nothing declares is kept as an edge to a node marked
// unresolved rather than dropped. Terraform would fail on it too, and a
// dependency that points nowhere is worth seeing.
func Graph(mods []Module, scope string) *core.Graph { return GraphNamed(mods, scope, nil) }

// GraphNamed is Graph with the account names this estate wrote down.
//
// A group labelled with twelve digits is one nobody recognises, and every
// reader has to go and look each of them up. The names change nothing about
// what was found; they are what it is called.
func GraphNamed(mods []Module, scope string, names map[string]string) *core.Graph {
	byKey := make(map[string]Module, len(mods))
	for _, m := range mods {
		byKey[m.Key] = m
	}
	id := func(key string) string { return "module:" + key }

	g := &core.Graph{
		Version:  core.Version,
		Metadata: &core.Metadata{Generator: "terraform source scan", Source: "terraform-source", Scope: scope},
		Axes:     []core.Axis{{ID: "account", Label: "AWS account"}},
	}

	accounts := map[string]bool{}
	for _, m := range mods {
		n := core.Node{ID: id(m.Key), Type: "terraform_module", Name: m.Key, Provider: "aws",
			Attrs: map[string]any{"dir": m.Dir, "state_key": m.Key}}
		if m.Account != "" {
			n.Groups = map[string]string{"account": m.Account}
			n.Attrs["account_id"] = m.Account
			accounts[m.Account] = true
		}
		g.Nodes = append(g.Nodes, n)
	}

	unresolved := map[string]bool{}
	for _, m := range mods {
		for _, want := range m.Requires {
			if _, ok := byKey[want]; !ok {
				unresolved[want] = true
			}
			g.Edges = append(g.Edges, core.Edge{From: id(m.Key), To: id(want),
				Kind: core.EdgeIACRef, Relation: "remote_state"})
		}
	}
	for _, key := range sorted(unresolved) {
		g.Nodes = append(g.Nodes, core.Node{ID: id(key), Type: "terraform_module",
			Name: key, Provider: "aws",
			Attrs: map[string]any{"state_key": key, "unresolved": true}})
	}

	for _, a := range sorted(accounts) {
		label := a
		if name, ok := names[a]; ok && name != "" {
			// The id stays in the label. Two accounts can be called the same
			// thing in two places, and the id is what settles which one this
			// is when somebody has to go and look.
			label = name + " (" + a + ")"
		}
		g.Groups = append(g.Groups, core.Group{ID: a, Axis: "account", Type: "aws_account", Label: label})
	}
	// Naming the estate has to qualify the ids, not only the metadata: two
	// repositories can both hold a state called states/vpc, and combining the
	// documents would merge them into one box.
	g.ApplyScope(scope)
	return g
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fileNames is the plain file names in a directory listing.
func fileNames(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}
