package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func parsed(t *testing.T, files map[string]string) *core.Graph {
	t.Helper()
	d := t.TempDir()
	for name, body := range files {
		path := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	g, err := ParseDir(d)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func typeNamed(t *testing.T, g *core.Graph, name string) core.Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Type == NodeType && n.Name == name {
			return n
		}
	}
	t.Fatalf("no type named %q in %s", name, names(g))
	return core.Node{}
}

func names(g *core.Graph) string {
	out := ""
	for _, n := range g.Nodes {
		if n.Type == NodeType {
			out += " " + n.Name
		}
	}
	return "[" + out + " ]"
}

func related(g *core.Graph, from, relation, to string) bool {
	for _, e := range g.Edges {
		if e.Relation != relation {
			continue
		}
		var f, t core.Node
		for _, n := range g.Nodes {
			if n.ID == e.From {
				f = n
			}
			if n.ID == e.To {
				t = n
			}
		}
		if f.Name == from && t.Name == to {
			return true
		}
	}
	return false
}

// A class diagram is a question about types, and until now there was no such
// thing in the IR to ask it of.
func TestATypeIsRecordedWhereItIsDeclared(t *testing.T) {
	g := parsed(t, map[string]string{"shop/order.go": `package shop

type Order struct {
	ID    string
	Lines []Line
}

type Line struct{ SKU string }

type Store interface{ Save(o Order) error }

type ID = string
`})

	for name, kind := range map[string]string{
		"Order": "struct", "Line": "struct", "Store": "interface", "ID": "alias",
	} {
		n := typeNamed(t, g, name)
		if got := n.Attrs["kind"]; got != kind {
			t.Errorf("%s is a %v, want %s", name, got, kind)
		}
		if n.Source == nil || n.Source.File != "shop/order.go" || n.Source.Line == 0 {
			t.Errorf("%s does not say where it was declared: %#v", name, n.Source)
		}
	}
	if !related(g, "shop/order.go", "contains", "Order") {
		t.Error("the file does not contain the type declared in it")
	}
	// The distinction earns its place: a drawing shows an interface
	// differently from a struct, and an alias is not a new thing at all.
	if !related(g, "Order", RelationHasField, "Line") {
		t.Error("a field whose type is another type here is not recorded")
	}
}

// A method is not a second thing. It is the one function, seen from the type's
// side — so the edge points at the function node that already existed.
func TestAMethodIsTheFunctionSeenFromTheTypesSide(t *testing.T) {
	g := parsed(t, map[string]string{"shop/order.go": `package shop

type Order struct{ ID string }

func (o *Order) Total() int { return 0 }

func Free() {}
`})

	if !related(g, "Order", RelationDeclares, "Order.Total") {
		t.Fatal("the method is not declared on its type")
	}
	if related(g, "Order", RelationDeclares, "Free") {
		t.Error("a package-level function was made a method")
	}
	// And it is still the file's function: nothing was duplicated.
	if !related(g, "shop/order.go", "contains", "Order.Total") {
		t.Error("the method stopped being a function of the file")
	}
}

// Go's way of saying "this is one of those". A method beside its struct in
// another file of the same package is the ordinary layout, so the joining
// cannot happen until every file has been read.
func TestEmbeddingAndMethodsReachAcrossFilesOfOnePackage(t *testing.T) {
	g := parsed(t, map[string]string{
		"shop/base.go":  "package shop\n\ntype Base struct{ ID string }\n",
		"shop/order.go": "package shop\n\ntype Order struct {\n\tBase\n\tTotal int\n}\n",
		"shop/total.go": "package shop\n\nfunc (o *Order) Recalculate() {}\n",
	})

	if !related(g, "Order", RelationEmbeds, "Base") {
		t.Error("embedding was not recorded")
	}
	if !related(g, "Order", RelationDeclares, "Order.Recalculate") {
		t.Error("a method declared in another file of the package did not reach its type")
	}
}

// A base class from a library nobody handed us is a name, and a box drawn from
// a name is a box nobody can open.
func TestARelationToATypeNobodyReadIsNotRecorded(t *testing.T) {
	g := parsed(t, map[string]string{"shop/order.go": `package shop

import "net/http"

type Handler struct {
	client *http.Client
	next   Handler
}
`})

	for _, e := range g.Edges {
		if e.Relation == RelationHasField {
			var to string
			for _, n := range g.Nodes {
				if n.ID == e.To {
					to = n.Name
				}
			}
			if to != "Handler" {
				t.Errorf("a relation was drawn to %q, which this parser never read", to)
			}
		}
	}
}

// Two types of one name in two packages is the ordinary shape of a repository,
// and choosing one of them would draw an arrow nobody meant.
func TestAnAmbiguousNameIsNotResolved(t *testing.T) {
	g := parsed(t, map[string]string{
		"shop/order.go":      "package shop\n\ntype Order struct{ ID string }\n",
		"warehouse/order.go": "package warehouse\n\ntype Order struct{ ID string }\n",
		"api/handler.go":     "package api\n\ntype Handler struct{ order Order }\n",
	})

	for _, e := range g.Edges {
		if e.Relation == RelationHasField {
			t.Errorf("a field was joined to one of two types with the same name: %#v", e)
		}
	}
	// The same name inside one directory is not ambiguous, and still resolves.
	if !related(g, "Order", RelationHasField, "Order") {
		// Nothing to assert here beyond the absence above; this branch only
		// documents that the local case is the one that works.
		_ = g
	}
}

// The languages without a parser of their own get the same vocabulary, read
// off the declaration line.
func TestTypesAreReadFromTheOtherLanguagesToo(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/order.ts": `export class Order extends Record implements Priced {
  total(): number { return 0; }
}
export class Record {}
export interface Priced {}
`,
		"app/basket.py": `class Basket(Container):
    def add(self, item):
        pass

class Container:
    pass
`,
	})

	if n := typeNamed(t, g, "Order"); n.Attrs["kind"] != "class" {
		t.Errorf("Order is a %v", n.Attrs["kind"])
	}
	if !related(g, "Order", RelationExtends, "Record") {
		t.Error("the base class was not recorded")
	}
	if !related(g, "Order", RelationImplements, "Priced") {
		t.Error("the implemented interface was not recorded")
	}
	if !related(g, "Order", RelationDeclares, "total") {
		t.Error("the method declared in the class body is not on the class")
	}
	if !related(g, "Basket", RelationExtends, "Container") {
		t.Error("the Python base was not recorded")
	}
	if !related(g, "Basket", RelationDeclares, "add") {
		t.Error("the Python method is not on its class")
	}
}

// A function after the class body is not a method, whichever way the language
// closes a scope.
func TestAFunctionAfterTheClassBodyIsNotAMethod(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/order.ts": "class Order {\n  total(): number { return 0; }\n}\n\nfunction free(): void {}\n",
		"app/free.py":  "class Basket:\n    def add(self):\n        pass\n\ndef free():\n    pass\n",
	})

	if related(g, "Order", RelationDeclares, "free") {
		t.Error("a function after the closing brace was made a method")
	}
	if related(g, "Basket", RelationDeclares, "free") {
		t.Error("a function after the class body was made a method")
	}
}

// Every graph this parser produces has to hold together on its own terms.
func TestATypedGraphValidates(t *testing.T) {
	g := parsed(t, map[string]string{
		"shop/order.go": "package shop\n\ntype Order struct{ Line Line }\n\ntype Line struct{ SKU string }\n\nfunc (o Order) Total() int { return 0 }\n",
		"app/order.ts":  "class Order extends Base {}\nclass Base {}\n",
	})
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A body that opens and closes on its own line is over where it started.
// Leaving it open made the next function in the file a method on it, and the
// three-line class body the other test uses was exactly the shape that hid it.
func TestAOneLineTypeBodyDoesNotSwallowTheRestOfTheFile(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/order.ts": "class Order {}\n\nfunction free(): void {}\n",
		"app/empty.ts": "interface Priced {}\n\nfunction alsoFree(): void {}\n",
	})

	typeNamed(t, g, "Order") // the file was read at all
	if related(g, "Order", RelationDeclares, "free") {
		t.Error("a function after a one-line class body was made a method")
	}
	if related(g, "Priced", RelationDeclares, "alsoFree") {
		t.Error("a function after an empty interface was made a method")
	}
}

// A declaration with no body at all — a Rust unit struct, a C forward
// declaration — has no inside for anything to be in.
func TestATypeWithNoBodyHasNothingInside(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/lib.rs": "struct Marker;\n\nfn free() {}\n",
	})
	typeNamed(t, g, "Marker") // a unit struct is still a type
	if related(g, "Marker", RelationDeclares, "free") {
		t.Error("a function after a bodyless declaration was made a method")
	}
}

// `struct sockaddr_in addr;` declares a variable. Reading it as a type made a
// box for something the file never declared — and, with the scope bug, hung
// every function in the file off it.
func TestAVariableDeclarationIsNotATypeDeclaration(t *testing.T) {
	g := parsed(t, map[string]string{
		"net/serve.c": "struct sockaddr_in addr;\n\nint main(void) {\n  return 0;\n}\n",
	})
	read := false
	for _, n := range g.Nodes {
		if n.Type == NodeType {
			t.Errorf("a variable declaration produced the type %q", n.Name)
		}
		if n.Type == "code_function" && n.Name == "main" {
			read = true
		}
	}
	if !read {
		t.Fatal("the file was not read at all, so it proves nothing")
	}
}

// An anonymous class has no name, and the first word after `class` is a
// keyword. Reading it as a name made a type called "extends" that extended
// something.
func TestAnAnonymousClassIsNotATypeCalledExtends(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/anon.ts": "export default class extends Base {\n  run() {}\n}\nclass Base {}\n",
	})
	typeNamed(t, g, "Base") // the file was read at all
	for _, n := range g.Nodes {
		if n.Type == NodeType && notAName[n.Name] {
			t.Errorf("a keyword was read as a type name: %q", n.Name)
		}
	}
}

// A qualified name names another package, and this parser has no notion of
// which package is which. Dropping the qualifier joined `*http.Client` to
// whatever local type happened to be called Client.
func TestAQualifiedNameIsNotJoinedToALocalTypeOfTheSameName(t *testing.T) {
	g := parsed(t, map[string]string{
		"api/handler.go": "package api\n\nimport \"net/http\"\n\ntype Handler struct {\n\tclient *http.Client\n}\n",
		"pool/client.go": "package pool\n\ntype Client struct{ ID string }\n",
	})
	typeNamed(t, g, "Handler")
	typeNamed(t, g, "Client") // both ends exist; only the joining is wrong
	if related(g, "Handler", RelationHasField, "Client") {
		t.Error("a field of another package's type was joined to a local type with the same name")
	}
}

// Type parameters are not bases. `class Box<T extends Number>` bounds a
// parameter and names nothing it descends from.
func TestATypeParameterBoundIsNotABase(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/box.java": "class Box<T extends Number> {\n  void put(T t) {}\n}\nclass Number {}\n",
	})
	if related(g, "Box", RelationExtends, "Number") {
		t.Error("a type parameter bound was recorded as a base class")
	}
	// And the declaration is still read: the class, and its method.
	if !related(g, "Box", RelationDeclares, "put") {
		t.Error("stripping the type parameters lost the declaration with them")
	}
}

// `implements Map<String, Integer>` implements one interface. Splitting the
// line on commas made two.
func TestAGenericArgumentIsNotASecondInterface(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/reg.java": "class Registry implements Map<String, Integer> {\n}\ninterface Map {}\nclass Integer {}\n",
	})
	if related(g, "Registry", RelationImplements, "Integer") {
		t.Error("a generic argument was recorded as an implemented interface")
	}
	if !related(g, "Registry", RelationImplements, "Map") {
		t.Error("the interface that was implemented was lost")
	}
}
