package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func TestParseDirExtractsCrossLanguageBasics(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "main.go"), []byte("package main\nimport \"net/http\"\nfunc serve() { handle() }\nfunc handle() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "worker.py"), []byte("from queue import Queue\ndef consume():\n  helper()\ndef helper():\n  pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) < 5 {
		t.Fatalf("expected files, functions, and packages; got %d nodes", len(g.Nodes))
	}
	if len(g.Edges) < 5 {
		t.Fatalf("expected contains/imports edges; got %d", len(g.Edges))
	}
	var pythonCall bool
	for _, e := range g.Edges {
		if e.Relation == "calls" && e.From == "file:worker.py#consume" && e.To == "file:worker.py#helper" {
			pythonCall = true
		}
	}
	if !pythonCall {
		t.Fatal("generic parser did not recover the Python function call")
	}
}

func TestBraceFreeFunctionDefaultsDoNotCloseTheFunction(t *testing.T) {
	tests := []struct {
		name, file, body, from, to string
	}{
		{
			name: "python", file: "handler.py",
			body: "def handler(event, context={}):\n  return helper(event)\ndef helper(event):\n  return event\n",
			from: "file:handler.py#handler", to: "file:handler.py#helper",
		},
		{
			name: "ruby", file: "handler.rb",
			body: "def initialize(options = {})\n  helper(options)\nend\ndef helper(options)\n  options\nend\n",
			from: "file:handler.rb#initialize", to: "file:handler.rb#helper",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			if err := os.WriteFile(filepath.Join(d, tc.file), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			g, err := ParseDir(d)
			if err != nil {
				t.Fatal(err)
			}
			if !hasCallEdgeWithResolution(g, tc.from, tc.to, "static_same_file") {
				t.Fatalf("same-file call %s -> %s was not recovered", tc.from, tc.to)
			}
		})
	}
}

func TestPythonFloorDivisionDoesNotStartAComment(t *testing.T) {
	d := t.TempDir()
	body := "def handler(x):\n  return compute(x) // count(x)\ndef compute(x):\n  return x\ndef count(x):\n  return x\n"
	if err := os.WriteFile(filepath.Join(d, "maths.py"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"compute", "count"} {
		if !hasCallEdge(g, "file:maths.py#handler", "file:maths.py#"+target) {
			t.Errorf("call after floor division to %s was not recovered", target)
		}
	}
}

func TestSlashSlashIsNotACommentInPythonOrRuby(t *testing.T) {
	for _, language := range []string{"py", "rb"} {
		got := sanitizeSource([]string{"total = compute(x) // count(y)"}, language)[0]
		if !strings.Contains(got, "count(y)") {
			t.Errorf("%s treated // as a line comment: %q", language, got)
		}
	}
	got := sanitizeSource([]string{"total = compute(x); // count(y)"}, "js")[0]
	if strings.Contains(got, "count(y)") {
		t.Errorf("JavaScript line comment remained executable: %q", got)
	}
}

func TestPythonPackageRelativeSubmoduleImportAuthorizesQualifiedCall(t *testing.T) {
	d := t.TempDir()
	files := map[string]string{
		"pkg/api.py":   "from . import utils\ndef handler():\n  utils.work()\n",
		"pkg/utils.py": "def work():\n  pass\n",
	}
	for name, body := range files {
		path := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCallEdgeWithResolution(g, "file:pkg/api.py#handler", "file:pkg/utils.py#work", "static_cross_file_unique") {
		t.Fatal("package-relative submodule import did not authorize the qualified call")
	}
}

func TestParseDirIncludesCommonLanguageFilesEvenWithoutASTParser(t *testing.T) {
	d := t.TempDir()
	files := map[string]string{
		"App.cs":       "using System;\npublic void Run() {}\n",
		"App.swift":    "import Foundation\nfunc run() {}\n",
		"main.rs":      "use std::io;\nfn run() {}\n",
		"schema.sql":   "CREATE TABLE users (id integer);\n",
		"handler.dart": "import 'dart:io';\nvoid handle() {}\n",
		"deploy.sh":    "#!/bin/sh\necho deploy\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if _, ok := g.Node("file:" + name); !ok {
			t.Errorf("file %s was not represented", name)
		}
	}
}

func TestParseGoMethodsHaveDistinctFunctionIDs(t *testing.T) {
	d := t.TempDir()
	body := `package adapters
type SQLStore struct{}
type HTTPStore struct{}
func (SQLStore) Fetch() {}
func (HTTPStore) Fetch() {}
`
	if err := os.WriteFile(filepath.Join(d, "stores.go"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"file:stores.go#SQLStore.Fetch", "file:stores.go#HTTPStore.Fetch"} {
		if _, ok := g.Node(id); !ok {
			t.Errorf("method %s was not represented", id)
		}
	}
}

func TestParseDirSupportsUnknownTextAndRegisteredParsers(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "diagram.toy"), []byte("opaque syntax"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "notes.unknown"), []byte("plain text"), 0600); err != nil {
		t.Fatal(err)
	}
	Register(".toy", func(g *core.Graph, _ string, fileID, _ string) error {
		id := fileID + "#entry"
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "code_function", Name: "entry"})
		g.Edges = append(g.Edges, core.Edge{From: fileID, To: id, Kind: core.EdgeIACRef, Relation: "contains"})
		return nil
	})
	g, err := ParseDirWithOptions(d, Options{IncludeUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"file:diagram.toy#entry", "file:notes.unknown"} {
		if _, ok := g.Node(id); !ok {
			t.Errorf("expected registered/unknown source entity %s", id)
		}
	}
}

func TestParseDirNestsSourceEntitiesByDirectory(t *testing.T) {
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "services", "checkout"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d, "services", "checkout", "main.py")
	if err := os.WriteFile(path, []byte("def handle():\n  pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := g.Node("file:services/checkout/main.py#handle")
	if !ok {
		t.Fatal("function node was not created")
	}
	want := "source:dir:services/source:dir:services::checkout"
	if n.Groups["source"] != want {
		t.Fatalf("source group path = %q, want %q", n.Groups["source"], want)
	}
	if _, ok := g.Group("source:dir:services::checkout"); !ok {
		t.Fatal("nested source directory group was not created")
	}
}

func TestParseDirMarksLibraryAndCrossFileApplicationReferences(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "api.py"), []byte("from shared import helper\ndef serve():\n  helper()\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "shared.py"), []byte("def helper():\n  pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	var library, application bool
	for _, e := range g.Edges {
		if e.Relation == "imports" && e.Attrs["reference_kind"] == "library" {
			library = true
		}
		if e.From == "file:api.py#serve" && e.To == "file:shared.py#helper" && e.Attrs["resolution"] == "static_cross_file_unique" {
			application = true
		}
	}
	if !library {
		t.Fatal("library import was not marked as a library reference")
	}
	if !application {
		t.Fatal("unique cross-file application call was not recovered")
	}
}

func TestCurlyLanguageFunctionsExcludeControlFlowAndComments(t *testing.T) {
	d := t.TempDir()
	java := `package example;
public class App {
  /* public void CommentedOut() {} */
  private final Runnable task =
    new Runnable() {
      public void execute() {}
    };
  public void Run() {
    if (ready()) {
      helper();
    }
    for (;;) {}
  }
  private int helper() { return 1; }
}
`
	c := `// int CommentedOutToo() {}
int main(void) {
  while (running()) {}
  return 0;
}
char *copy(void) { return 0; }
`
	for name, body := range map[string]string{"App.java": java, "main.c": c} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"file:App.java#Run", "file:App.java#helper", "file:main.c#main", "file:main.c#copy"} {
		if _, ok := g.Node(id); !ok {
			t.Errorf("normal Java/C-family function %s was not represented", id)
		}
	}
	for _, id := range []string{
		"file:App.java#if", "file:App.java#for", "file:App.java#CommentedOut",
		"file:App.java#Runnable",
		"file:main.c#while", "file:main.c#CommentedOutToo",
	} {
		if _, ok := g.Node(id); ok {
			t.Errorf("non-function %s was represented as a function", id)
		}
	}
	if !hasCallEdge(g, "file:App.java#Run", "file:App.java#helper") {
		t.Fatal("Java method call was not recovered")
	}
}

func TestCrossFileCallsRespectLanguageAndPackageBoundaries(t *testing.T) {
	d := t.TempDir()
	files := map[string]string{
		"caller.js":       "function run() {\n  helper();\n}\n",
		"helper.py":       "def helper():\n  pass\n",
		"app/main.py":     "def start():\n  shared()\n",
		"library/util.py": "def shared():\n  pass\n",
	}
	for name, body := range files {
		path := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{
		{"file:caller.js#run", "file:helper.py#helper"},
		{"file:app/main.py#start", "file:library/util.py#shared"},
	} {
		if hasCallEdge(g, edge[0], edge[1]) {
			t.Errorf("unrelated functions were connected: %s -> %s", edge[0], edge[1])
		}
	}
}

func TestCrossFileCallsIgnoreCommentsAndStrings(t *testing.T) {
	d := t.TempDir()
	caller := `def serve():
  # helper()
  text = "helper()"
  more = '''helper()'''
`
	for name, body := range map[string]string{
		"api.py":    caller,
		"shared.py": "def helper():\n  pass\n",
	} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if hasCallEdge(g, "file:api.py#serve", "file:shared.py#helper") {
		t.Fatal("a call mentioned only in comments/strings produced an edge")
	}
}

func TestImportDiscoveryIgnoresCommentsStringsAndDocstrings(t *testing.T) {
	d := t.TempDir()
	python := `"""
from ghost_docstring import helper
"""
text = "from ghost_string import helper"
# from ghost_comment import helper
from real_module import helper
def run():
  pass
`
	javascript := `const example = "require('ghost_string_js')";
/* require("ghost_comment_js"); */
const real = require("./real");
import { helper } from "./named";
function run() {}
`
	for name, body := range map[string]string{"imports.py": python, "imports.js": javascript} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"real_module", "./real", "./named"} {
		if _, ok := g.Node("package:" + name); !ok {
			t.Errorf("real import %q is missing", name)
		}
	}
	for _, name := range []string{"ghost_docstring", "ghost_string", "ghost_comment", "ghost_string_js", "ghost_comment_js"} {
		if _, ok := g.Node("package:" + name); ok {
			t.Errorf("lexically inactive import %q produced a package", name)
		}
	}
}

func TestPythonAndJavaScriptCrossFileCallsRequireImports(t *testing.T) {
	d := t.TempDir()
	files := map[string]string{
		"py/api.py":    "def serve():\n  py_helper()\n",
		"py/shared.py": "def py_helper():\n  pass\n",
		"js/api.js":    "function serve() {\n  jsHelper();\n}\n",
		"js/shared.js": "export function jsHelper() {}\n",
		"ts/api.ts":    "export function serve() {\n  tsHelper();\n}\n",
		"ts/shared.ts": "export function tsHelper() {}\n",
	}
	for name, body := range files {
		path := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{
		{"file:py/api.py#serve", "file:py/shared.py#py_helper"},
		{"file:js/api.js#serve", "file:js/shared.js#jsHelper"},
		{"file:ts/api.ts#serve", "file:ts/shared.ts#tsHelper"},
	} {
		if hasCallEdge(g, edge[0], edge[1]) {
			t.Errorf("unimported cross-file call was created: %s -> %s", edge[0], edge[1])
		}
	}
}

func TestJavaScriptAndTypeScriptImportsAuthorizeCrossFileCalls(t *testing.T) {
	d := t.TempDir()
	files := map[string]string{
		"web/api.ts":    "import { helper as invoke } from './shared';\nexport function serve() {\n  invoke();\n}\n",
		"web/shared.js": "export function helper() {}\n",
	}
	for name, body := range files {
		path := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCallEdge(g, "file:web/api.ts#serve", "file:web/shared.js#helper") {
		t.Fatal("a named TypeScript-to-JavaScript import did not authorize the call")
	}
}

func TestJavaScriptBarePackageImportDoesNotResolveToLocalFile(t *testing.T) {
	d := t.TempDir()
	files := map[string]string{
		"api.ts":    "import { helper } from 'shared';\nexport function serve() {\n  helper();\n}\n",
		"shared.js": "export function helper() {}\n",
	}
	for name, body := range files {
		path := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if hasCallEdge(g, "file:api.ts#serve", "file:shared.js#helper") {
		t.Fatal("bare package import resolved to a coincidentally named local file")
	}
}

func TestGoCrossFileCallsRequireTheSameDeclaredPackage(t *testing.T) {
	d := t.TempDir()
	files := map[string]string{
		"caller.go":        "package foo\nfunc caller() {\n  helper()\n}\n",
		"helper.go":        "package foo\nfunc helper() {}\n",
		"external_test.go": "package foo_test\nfunc outsider() {\n  helper()\n}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCallEdge(g, "file:caller.go#caller", "file:helper.go#helper") {
		t.Fatal("same-package Go call was not recovered")
	}
	if hasCallEdge(g, "file:external_test.go#outsider", "file:helper.go#helper") {
		t.Fatal("external foo_test package was connected to foo by directory alone")
	}
}

func hasCallEdge(g *core.Graph, from, to string) bool {
	for _, edge := range g.Edges {
		if edge.From == from && edge.To == to && edge.Relation == "calls" {
			return true
		}
	}
	return false
}

func hasCallEdgeWithResolution(g *core.Graph, from, to, resolution string) bool {
	for _, edge := range g.Edges {
		if edge.From == from && edge.To == to && edge.Relation == "calls" && edge.Attrs["resolution"] == resolution {
			return true
		}
	}
	return false
}
