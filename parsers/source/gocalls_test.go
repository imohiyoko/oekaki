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

// An import block written inside a string is not an import block, and the
// quoted lines after it are not imports.
//
// Asserted through a call rather than through the import edges: those come
// from a different reading of the file, and would pass whatever this one did.
func TestAnImportBlockInAStringIsNotOne(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": "package handler\n\nconst doc = `\nimport (\n\t\"example.com/svc/store\"\n)\n`\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
		"store/db.go":    "package store\n\nfunc Save(total int) {}\n",
	})
	if calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
		t.Fatal("an import inside a string was read as one")
	}
}

// Go allows a comment between the brackets, and an import inside one is not an
// import however much it looks like the line above it.
func TestACommentedOutImportInABlockIsNotOne(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": `package handler

import (
	"fmt"
	/*
	"example.com/svc/store"
	*/
)

func Handle() {
	fmt.Println("x")
	store.Save(1)
}
`,
		"store/db.go": "package store\n\nfunc Save(total int) {}\n",
	})
	if calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
		t.Fatal("an import inside a block comment was read as one")
	}
}

// A comment after an import is ordinary Go — a blank driver import is usually
// written with one. Losing the line loses the import silently, and a call that
// then finds no package resolves at the caller's own function of that name,
// which is the arrow this reading exists to refuse.
func TestATrailingCommentDoesNotHideAnImport(t *testing.T) {
	for _, tc := range []struct{ name, imports string }{
		{"grouped", "import (\n\t_ \"example.com/svc/driver\" // registers it\n\t\"example.com/svc/store\" // persistence\n)"},
		{"single line", "import \"example.com/svc/store\" // persistence"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := tree(t, map[string]string{
				"handler/api.go": "package handler\n\n" + tc.imports + "\n\nfunc Handle() {\n\tstore.Save(1)\n}\n\nfunc Save(n int) {}\n",
				"store/db.go":    "package store\n\nfunc Save(total int) {}\n",
				"driver/d.go":    "package driver\n\nfunc Register() {}\n",
			})
			if !calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
				t.Error("the import was lost to its own comment")
			}
			if calls(g, "file:handler/api.go#Handle", "file:handler/api.go#Save") {
				t.Error("the call was drawn at the caller's own function of that name")
			}
		})
	}
}

// A group can open and close on one line, and its last member can share the
// line with the closing bracket. Both are ordinary gofmt output for one import.
func TestAGroupOnOneLineIsRead(t *testing.T) {
	for _, tc := range []struct{ name, imports string }{
		{"opens and closes", "import (\"example.com/svc/store\")"},
		{"closes on the last member", "import (\n\t\"example.com/svc/store\")"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := tree(t, map[string]string{
				"handler/api.go": "package handler\n\n" + tc.imports + "\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
				"store/db.go":    "package store\n\nfunc Save(total int) {}\n",
			})
			if !calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
				t.Error("the import was lost with its brackets")
			}
		})
	}
}

// A tree that declares no module gets no cross-package edges, so nothing here
// may take its same-package ones away either. "Exactly as before" has to be
// true on the side that draws, not only on the side that refuses.
func TestATreeWithNoModuleKeepsTheEdgesItAlreadyHad(t *testing.T) {
	files := map[string]string{
		"go.mod": "",
		"handler/api.go": `package handler

import "example.com/svc/store"

func Handle() {
	store.Save(1)
}

func Save(n int) {}
`,
		"store/db.go": "package store\n\nfunc Save(total int) {}\n",
	}
	g := tree(t, files)
	if calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
		t.Error("a tree with no module path resolved across packages anyway")
	}
	if !calls(g, "file:handler/api.go#Handle", "file:handler/api.go#Save") {
		t.Error("the edge this tree already had was taken away and nothing given back")
	}
}

// An import path is a string, and Go allows either kind of string. Reading
// only one of them loses the import silently, which is the failure this whole
// reading is most exposed to.
func TestARawStringImportPathIsRead(t *testing.T) {
	for _, tc := range []struct{ name, imports string }{
		{"single line", "import `example.com/svc/store`"},
		{"grouped", "import (\n\t`example.com/svc/store`\n)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := tree(t, map[string]string{
				"handler/api.go": "package handler\n\n" + tc.imports + "\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
				"store/db.go":    "package store\n\nfunc Save(total int) {}\n",
			})
			if !calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
				t.Error("an import written as a raw string was not read")
			}
		})
	}
}

// A comment that opens and closes before the import is a note in front of it,
// not a reason to stop reading the line.
func TestACommentInFrontOfAnImportDoesNotHideIt(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/api.go": "package handler\n\nimport (\n\t/* persistence */ \"example.com/svc/store\"\n)\n\nfunc Handle() {\n\tstore.Save(1)\n}\n",
		"store/db.go":    "package store\n\nfunc Save(total int) {}\n",
	})
	if !calls(g, "file:handler/api.go#Handle", "file:store/db.go#Save") {
		t.Fatal("a closed comment in front of an import hid it")
	}
}

// A package's name is its package clause, and the import path need not spell
// it. Both readings — the one that resolves the call and the one that keeps
// the caller's own function from crowding it out — have to agree about that,
// or the two together lose the call they were each written to find.
func TestAVersionSuffixedPackageIsStillReachedPastTheCallersOwnName(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": `package handler

import (
	"example.com/svc/store/v2"
)

func HandleOrder() {
	store.Save(1)
}

func Save(n int) {
	_ = n
}
`,
		"store/v2/db.go": `package store

func Save(n int) {
	_ = n
}
`,
	})
	if !calls(g, "file:handler/http.go#HandleOrder", "file:store/v2/db.go#Save") {
		t.Fatal("a call into a package whose path ends in a version was not resolved")
	}
	if calls(g, "file:handler/http.go#HandleOrder", "file:handler/http.go#Save") {
		t.Fatal("store.Save was answered by the caller's own Save")
	}
}

// A `/*` written inside a line comment opens nothing. Reading it as a block
// comment used to swallow the rest of the group and its closing bracket, which
// loses every import below it and leaves the scanner inside a block that never
// ends.
func TestABlockCommentInsideALineCommentOpensNothing(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": `package handler

import (
	// TODO: /* drop this later
	"example.com/svc/store"
)

func HandleOrder() {
	store.Save(1)
}

func Save(n int) {
	_ = n
}
`,
		"store/db.go": `package store

func Save(n int) {
	_ = n
}
`,
	})
	if !calls(g, "file:handler/http.go#HandleOrder", "file:store/db.go#Save") {
		t.Fatal("an import below a line comment holding /* was lost")
	}
}

// A blank import binds no identifier, so `store` in this file is the parameter
// and the call is the package's own. Counting the import as a name took the
// call away and put nothing in its place.
func TestABlankImportDoesNotClaimAName(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": `package handler

import (
	_ "example.com/svc/store"
)

type thing struct{}

func HandleOrder(store *thing) {
	store.Save(1)
}
`,
		"handler/other.go": `package handler

func Save(n int) {
	_ = n
}
`,
	})
	if !calls(g, "file:handler/http.go#HandleOrder", "file:handler/other.go#Save") {
		t.Fatal("a blank import was read as a package name and took the call with it")
	}
}

// `store.Default.Save(1)` is a method on a package variable. The package
// function of that name is never called, so no arrow is drawn to it.
func TestAMethodOnAPackageVariableIsNotAPackageFunction(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": `package handler

import (
	"example.com/svc/store"
)

func HandleOrder() {
	store.Default.Save(1)
}
`,
		"store/db.go": `package store

func Save(n int) {
	_ = n
}
`,
	})
	if calls(g, "file:handler/http.go#HandleOrder", "file:store/db.go#Save") {
		t.Fatal("a method call on a package variable was drawn at the package function")
	}
}

// go.mod allows the module path to be quoted. A quoted one that keeps its
// quotes matches no import path, and the tree quietly becomes one with no
// module at all.
func TestAQuotedModulePathIsStillAModulePath(t *testing.T) {
	g := tree(t, map[string]string{
		"go.mod": "module \"" + module + "\"\n\ngo 1.24\n",
		"handler/http.go": `package handler

import (
	"example.com/svc/store"
)

func HandleOrder() {
	store.Save(1)
}
`,
		"store/db.go": `package store

func Save(n int) {
	_ = n
}
`,
	})
	if !calls(g, "file:handler/http.go#HandleOrder", "file:store/db.go#Save") {
		t.Fatal("a quoted module path was not read as one")
	}
}

// A bracket and a quote are delimiters rather than words, so Go does not
// require a space in front of either.
func TestAnImportNeedsNoSpaceBeforeItsPath(t *testing.T) {
	for name, header := range map[string]string{
		"single": `import"example.com/svc/store"`,
		"group":  `import("example.com/svc/store")`,
	} {
		t.Run(name, func(t *testing.T) {
			g := tree(t, map[string]string{
				"handler/http.go": `package handler

` + header + `

func HandleOrder() {
	store.Save(1)
}
`,
				"store/db.go": `package store

func Save(n int) {
	_ = n
}
`,
			})
			if !calls(g, "file:handler/http.go#HandleOrder", "file:store/db.go#Save") {
				t.Fatal("an import written without a space before its path was not read")
			}
		})
	}
}

// The other half of the same rule: a method reached through a package variable
// is a method, and the declaration says so. Refusing every chained call would
// take `core.OriginParser.Rank()` away with the wrong one.
func TestAMethodReachedThroughAPackageVariableIsStillFound(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": `package handler

import (
	"example.com/svc/store"
)

func HandleOrder() {
	store.Default.Save(1)
}
`,
		"store/db.go": `package store

type Writer struct{}

var Default = Writer{}

func (w Writer) Save(n int) {
	_ = n
}
`,
	})
	if !calls(g, "file:handler/http.go#HandleOrder", "file:store/db.go#Writer.Save") {
		t.Fatal("a method on a package variable was not found")
	}
}

// A comment that opens after the closing bracket is outside the group. Keeping
// it open into the next declaration swallowed that one whole, and the file
// never left the group again.
func TestACommentAfterAGroupDoesNotEatTheNextOne(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": `package handler

import (
	"fmt"
) /* a note about the group
that runs on */

import (
	"example.com/svc/store"
)

func HandleOrder() {
	fmt.Println(store.Save(1))
}

func Save(n int) int {
	return n
}
`,
		"store/db.go": `package store

func Save(n int) int {
	return n
}
`,
	})
	if !calls(g, "file:handler/http.go#HandleOrder", "file:store/db.go#Save") {
		t.Fatal("an import block after a trailing comment was not read")
	}
	if calls(g, "file:handler/http.go#HandleOrder", "file:handler/http.go#Save") {
		t.Fatal("store.Save was answered by the caller's own Save")
	}
}

// A semicolon is how Go writes two members on one line. Reading only the first
// dropped the second, and a dropped import is not a missing edge but a wrong
// one.
func TestBothMembersOfAOneLineGroupAreRead(t *testing.T) {
	g := tree(t, map[string]string{
		"handler/http.go": `package handler

import ("fmt"; "example.com/svc/store")

func HandleOrder() {
	fmt.Println(store.Save(1))
}

func Save(n int) int {
	return n
}
`,
		"store/db.go": `package store

func Save(n int) int {
	return n
}
`,
	})
	if !calls(g, "file:handler/http.go#HandleOrder", "file:store/db.go#Save") {
		t.Fatal("the second member of a one-line group was dropped")
	}
	if calls(g, "file:handler/http.go#HandleOrder", "file:handler/http.go#Save") {
		t.Fatal("store.Save was answered by the caller's own Save")
	}
}

// A major-version suffix is not a package name. Guessing `v8` or `yaml.v3`
// fails to recognise the package, and the call lands on whatever local
// function happens to share the name.
func TestAVersionedModuleOutsideTheTreeIsStillRecognisedAsAPackage(t *testing.T) {
	for name, spec := range map[string]struct{ path, qualifier string }{
		"element": {"github.com/go-redis/redis/v8", "redis"},
		"suffix":  {"gopkg.in/yaml.v3", "yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			g := tree(t, map[string]string{
				"handler/http.go": `package handler

import (
	"` + spec.path + `"
)

func HandleOrder() {
	` + spec.qualifier + `.NewClient(nil)
}

func NewClient(o any) {
	_ = o
}
`,
			})
			if calls(g, "file:handler/http.go#HandleOrder", "file:handler/http.go#NewClient") {
				t.Fatal("a call into a versioned module outside the tree was answered by a local function")
			}
		})
	}
}
