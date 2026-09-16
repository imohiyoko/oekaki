package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

const module = "example.com/svc"

// tree writes a source tree as path → contents and reads it back as a graph.
// A go.mod is written unless the case supplies one, because that is what a Go
// tree has and what turns an import path into a directory.
func tree(t *testing.T, files map[string]string) *core.Graph {
	t.Helper()
	d := t.TempDir()
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = "module " + module + "\n\ngo 1.24\n"
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
	return g
}

func calls(g *core.Graph, from, to string) bool {
	for _, e := range g.Edges {
		if e.Relation == "calls" && e.From == from && e.To == to {
			return true
		}
	}
	return false
}

func imports(g *core.Graph, from string) []string {
	var out []string
	for _, e := range g.Edges {
		if e.Relation == "imports" && e.From == from {
			out = append(out, e.To)
		}
	}
	return out
}

// The import path names the directory the target is in, and the package clause
// says what it is called. Both are written down, so the call is not a guess —
// and a chain that stops at every package boundary is not a chain.
func TestAGoCallIntoAnotherPackageOfTheTreeResolves(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": `package handler

import (
	"net/http"
	"example.com/svc/store"
)

func HandleOrder(r *http.Request) {
	store.Save(1)
}
`,
		"store/db.go": "package store\n\nfunc Save(total int) {}\n",
	})
	if !calls(g, "file:handler/http.go#HandleOrder", "file:store/db.go#Save") {
		t.Fatalf("the cross-package call was not recovered:\n%v", g.Edges)
	}
}

// The import path is what is matched, not the last element of it. A package
// nobody handed us stays unread even when this tree has one of that name.
func TestAnOutsidePackageIsNotJoinedToALocalOneOfTheSameName(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": "package handler\n\nimport \"net/http\"\n\nfunc Handle() {\n\thttp.Get(\"/\")\n}\n",
		"http/client.go": "package http\n\nfunc Get(u string) {}\n",
	})
	if calls(g, "file:handler/api.go#Handle", "file:http/client.go#Get") {
		t.Fatal("net/http was joined to a local package called http")
	}
}

// Someone else's module that happens to end in the same directory name is
// still someone else's module.
func TestAnotherModuleEndingInTheSameNameIsNotJoined(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": "package handler\n\nimport \"github.com/somebody/else/store\"\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
		"store/db.go":    "package store\n\nfunc Save(total int) {}\n",
	})
	if calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
		t.Fatal("another module's store was joined to this tree's store")
	}
}

// A qualified call is a call into another package, so the caller's own
// function of that name is not a second candidate. It used to be, and two
// candidates are dropped — losing exactly the call this recovers.
func TestTheCallersOwnNameDoesNotCrowdOutTheImportedOne(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": `package handler

import "example.com/svc/store"

func Handle() {
	store.Save(1)
}

func Save(total int) {}
`,
		"store/db.go": "package store\n\nfunc Save(total int) {}\n",
	})
	if !calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
		t.Fatalf("a same-named local function hid the imported one:\n%v", g.Edges)
	}
}

// An alias is what this file calls that package by, and the qualifier will be
// written with it.
func TestAnAliasedImportResolves(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": "package handler\n\nimport st \"example.com/svc/store\"\n\nfunc Handle() {\n\tst.Save(1)\n}\n",
		"store/db.go":    "package store\n\nfunc Save(total int) {}\n",
	})
	if !calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
		t.Fatal("an aliased import did not resolve")
	}
}

// A package's name is its package clause. The last element of the path is not
// it — for a version suffix, a dotted host path, or a directory somebody
// hyphenated.
func TestThePackageClauseIsTheNameNotTheLastPathElement(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go":    "package handler\n\nimport \"example.com/svc/go-store/v2\"\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
		"go-store/v2/db.go": "package store\n\nfunc Save(total int) {}\n",
	})
	if !calls(g, "file:handler/api.go#Handle", "file:go-store/v2/db.go#Save") {
		t.Fatalf("the package clause was not used as the name:\n%v", g.Edges)
	}
}

// Two packages of one name are told apart by the path, which is the whole
// reason the path is what gets matched. The one that was imported is drawn and
// the other is not.
func TestTwoPackagesWithOneNameAreToldApartByThePath(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": "package handler\n\nimport \"example.com/svc/a/store\"\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
		"a/store/db.go":  "package store\n\nfunc Save(total int) {}\n",
		"b/store/db.go":  "package store\n\nfunc Save(total int) {}\n",
	})
	if !calls(g, "file:handler/api.go#Handle", "file:a/store/db.go#Save") {
		t.Error("the imported package was not reached")
	}
	if calls(g, "file:handler/api.go#Handle", "file:b/store/db.go#Save") {
		t.Error("a package nobody imported was reached")
	}
}

// A qualifier that is a value rather than a package names nothing the file
// imported, so there is nothing to resolve it against.
func TestAQualifierThatIsAValueResolvesNothing(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": "package handler\n\nfunc Handle() {\n\tdb.Save(1)\n}\n",
		"store/db.go":    "package db\n\nfunc Save(total int) {}\n",
	})
	if calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
		t.Fatal("a value qualifier was read as a package")
	}
}

// Without a go.mod there is no way to turn an import path into a directory,
// and matching on the last element is what draws net/http at a local package.
// So a tree that does not say what module it is keeps its calls inside a
// package, exactly as before any of this.
func TestATreeWithNoModulePathResolvesNoCrossPackageCall(t *testing.T) {
	g := tree(t, map[string]string{
		"go.mod":         "",
		"handler/api.go": "package handler\n\nimport \"example.com/svc/store\"\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
		"store/db.go":    "package store\n\nfunc Save(total int) {}\n",
	})
	if calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
		t.Fatal("a tree with no module path resolved across packages anyway")
	}
}

// An import block written inside a string or a comment is not an import
// block, and the quoted lines after it are not imports.
func TestAnImportBlockInAStringIsNotOne(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": "package handler\n\nconst doc = `\nimport (\n\t\"example.com/svc/store\"\n)\n`\n\nfunc Handle() {}\n",
	})
	if got := imports(g, "file:handler/api.go"); len(got) != 0 {
		t.Fatalf("imports were invented out of a string: %v", got)
	}
}
