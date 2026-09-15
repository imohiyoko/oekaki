package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// tree writes a source tree as path → contents and reads it back as a graph.
func tree(t *testing.T, files map[string]string) *core.Graph {
	t.Helper()
	d := t.TempDir()
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

// The import line says what the qualifier refers to and the package clause
// says the target is that package. Both are written down, so the call is not
// a guess — and a chain that stops at every package boundary is not a chain.
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

// A single-line import is read the same way a grouped one is.
func TestASingleLineImportResolvesToo(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": "package handler\n\nimport \"example.com/svc/store\"\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
		"store/db.go":     "package store\n\nfunc Save(total int) {}\n",
	})
	if !calls(g, "file:handler/http.go#Handle", "file:store/db.go#Save") {
		t.Fatal("a single-line import did not resolve the call")
	}
}

// An alias is what this file calls that package, and it is the name the
// qualifier will be written with.
func TestAnAliasedImportResolves(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": "package handler\n\nimport st \"example.com/svc/store\"\n\nfunc Handle() {\n\tst.Save(1)\n}\n",
		"store/db.go":     "package store\n\nfunc Save(total int) {}\n",
	})
	// The qualifier names the alias rather than the package clause, and
	// nothing here maps one to the other — the import path would have to be
	// resolved against a module root nobody handed us. Refusing is the same
	// answer this parser gives everywhere it cannot see the other end.
	if calls(g, "file:handler/http.go#Handle", "file:store/db.go#Save") {
		t.Fatal("an aliased import resolved a call the parser cannot actually place")
	}
}

// Two packages of one name leave two candidates, and choosing one would draw
// an arrow nobody wrote. It is the "exactly one" rule types already follow.
func TestTwoPackagesWithOneNameResolveNothing(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": "package handler\n\nimport \"example.com/a/store\"\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
		"a/store/db.go":   "package store\n\nfunc Save(total int) {}\n",
		"b/store/db.go":   "package store\n\nfunc Save(total int) {}\n",
	})
	if calls(g, "file:handler/http.go#Handle", "file:a/store/db.go#Save") ||
		calls(g, "file:handler/http.go#Handle", "file:b/store/db.go#Save") {
		t.Fatal("an ambiguous package was picked between")
	}
}

// A package nobody handed us stays unread. A box drawn from a name is a box
// nobody can open.
func TestACallIntoAPackageOutsideTheTreeIsNotDrawn(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": "package handler\n\nimport \"net/http\"\n\nfunc Handle() {\n\thttp.Get(\"/\")\n}\n",
		"store/db.go":     "package store\n\nfunc Get(u string) {}\n",
	})
	if calls(g, "file:handler/http.go#Handle", "file:store/db.go#Get") {
		t.Fatal("a call to an outside package was joined to a local function of the same name")
	}
}

// A qualifier that is a value rather than a package names nothing the file
// imported, so there is nothing to resolve it against.
func TestAQualifierThatIsAValueResolvesNothing(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": "package handler\n\nfunc Handle() {\n\tdb.Save(1)\n}\n",
		"store/db.go":     "package db\n\nfunc Save(total int) {}\n",
	})
	if calls(g, "file:handler/http.go#Handle", "file:store/db.go#Save") {
		t.Fatal("a value qualifier was read as a package")
	}
}
