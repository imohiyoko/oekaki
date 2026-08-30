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
	"path/filepath"
	"regexp"
	"sort"
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
	for _, path := range files {
		if err := parseFile(g, path, fileIDs[path], root); err != nil {
			return nil, err
		}
	}
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

func parseFile(g *core.Graph, path, fileID, root string) error {
	parserMu.RLock()
	custom := languageParsers[strings.ToLower(filepath.Ext(path))]
	parserMu.RUnlock()
	if custom != nil {
		return custom(g, path, fileID, root)
	}
	if strings.EqualFold(filepath.Ext(path), ".go") {
		return parseGoFile(g, path, fileID, root)
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
	funcs := map[string]string{}
	for line, text := range codeLines {
		name, ok := functionName(text, lang)
		if ok {
			id := fileID + "#" + name
			if _, exists := funcs[name]; !exists {
				funcs[name] = id
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
		if name, ok := functionName(text, lang); ok {
			current = funcs[name]
			depth = 0
			if braceDelimited {
				depth = braceDelta(text)
			}
			defIndent = indentation(lines[line])
			declared = true
		}
		if current != "" {
			for _, match := range callExpr.FindAllStringSubmatch(text, -1) {
				if to, exists := funcs[match[1]]; exists && to != current {
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

func parseGoFile(g *core.Graph, path, fileID, root string) error {
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
}

// addCrossFileCalls recovers the useful middle ground between a full
// language/type checker and a file-only picture. Calls across Python and
// JavaScript modules require an import that names the target; Go calls require
// the exact same directory and package declaration. Other languages retain the
// conservative explicit-package/directory boundary.
func addCrossFileCalls(g *core.Graph, root string) error {
	byFile := map[string][]sourceFunction{}
	for _, n := range g.Nodes {
		if n.Type != "code_function" || n.Source == nil || n.Source.File == "" || n.Source.Line == 0 {
			continue
		}
		name := n.Name
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		fn := sourceFunction{id: n.ID, name: name, file: n.Source.File, line: n.Source.Line}
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
						if !ok || candidate.id == fn.id || !canResolveCrossFile(file, source, candidate.file, target, call, candidate.name) {
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

func canResolveCrossFile(callerFile string, caller sourceFileInfo, targetFile string, target sourceFileInfo, call sourceCall, targetName string) bool {
	family := languageFamily(caller.lang)
	if languageFamily(target.lang) != family {
		return false
	}
	switch family {
	case "go":
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
	for i, raw := range lines {
		if i >= len(code) {
			break
		}
		switch languageFamily(language) {
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
