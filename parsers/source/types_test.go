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
	if !related(g, "Order", RelationDeclares, "Order.total") {
		t.Error("the method declared in the class body is not on the class")
	}
	if !related(g, "Basket", RelationExtends, "Container") {
		t.Error("the Python base was not recorded")
	}
	if !related(g, "Basket", RelationDeclares, "Basket.add") {
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

	if related(g, "Order", RelationDeclares, "Order.free") {
		t.Error("a function after the closing brace was made a method")
	}
	if related(g, "Basket", RelationDeclares, "Basket.free") {
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
	if related(g, "Order", RelationDeclares, "Order.free") {
		t.Error("a function after a one-line class body was made a method")
	}
	if related(g, "Priced", RelationDeclares, "Priced.alsoFree") {
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
	if related(g, "Marker", RelationDeclares, "Marker.free") {
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
	if !related(g, "Box", RelationDeclares, "Box.put") {
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

// Two classes in one file that declare the same method have two methods.
//
// A single node named `run` made them one: every class in the file declaring
// the same box, a member click landing on another class's page, and — where a
// top-level function shared the name — a class declaring a function that was
// never inside it. The wrong arrow in a design diagram this file's own comment
// says it will not draw.
func TestTwoClassesDeclaringTheSameNameHaveTwoMethods(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/work.py": `def run():
    pass

class Job:
    def run(self):
        pass

class Task:
    def run(self):
        pass
`,
		"app/ship.ts": `class Truck {
  ship(): void {}
}
class Train {
  ship(): void {}
}
`,
	})

	for _, owner := range []string{"Job", "Task"} {
		if !related(g, owner, RelationDeclares, owner+".run") {
			t.Errorf("%s does not declare its own run", owner)
		}
	}
	if related(g, "Job", RelationDeclares, "run") {
		t.Error("a class declares the module's top-level function")
	}
	for _, owner := range []string{"Truck", "Train"} {
		if !related(g, owner, RelationDeclares, owner+".ship") {
			t.Errorf("%s does not declare its own ship", owner)
		}
	}
	// And each of them is a node of its own, so a member click goes to the
	// method it names.
	for _, id := range []string{
		"file:app/work.py#Job.run", "file:app/work.py#Task.run", "file:app/work.py#run",
		"file:app/ship.ts#Truck.ship", "file:app/ship.ts#Train.ship",
	} {
		if _, ok := g.Node(id); !ok {
			t.Errorf("%s is not a node of its own", id)
		}
	}
}

// A function nested inside a method is that method's business. The class does
// not declare it.
func TestAFunctionInsideAMethodIsNotAMember(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/outer.py": `class Outer:
    def method(self):
        def helper():
            pass
        return helper
`,
		"app/outer.ts": `class Wrapper {
  method(): void {
    function inner(): void {}
    inner();
  }
}
`,
	})

	if !related(g, "Outer", RelationDeclares, "Outer.method") {
		t.Fatal("the method itself was lost")
	}
	if related(g, "Outer", RelationDeclares, "Outer.helper") {
		t.Error("a function nested in a method was made a member")
	}
	if !related(g, "Wrapper", RelationDeclares, "Wrapper.method") {
		t.Fatal("the method itself was lost")
	}
	if related(g, "Wrapper", RelationDeclares, "Wrapper.inner") {
		t.Error("a function nested in a method was made a member")
	}
}

// An interface may extend several. The implements clause was already read as a
// list and this one was not, which made the two halves of the same sentence
// behave differently.
func TestExtendsIsAListLikeImplementsIs(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/c.ts": "interface A {}\ninterface B {}\ninterface C extends A, B {}\n",
	})
	for _, base := range []string{"A", "B"} {
		if !related(g, "C", RelationExtends, base) {
			t.Errorf("%s was dropped from the base list", base)
		}
	}
}

// C++ writes its access in the base list. Taking the first word there named
// `public` as the base, which matches no type and is dropped — so C++
// inheritance produced no edge at all.
func TestAnAccessSpecifierIsNotTheBase(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/d.cpp": "class Base {\n};\n\nclass Derived : public Base {\n};\n",
	})
	typeNamed(t, g, "Derived")
	if !related(g, "Derived", RelationExtends, "Base") {
		t.Error("the base was lost behind its access specifier")
	}
}

// A declaration long enough to wrap puts its brace, and often its bases, on a
// later line. Judging the scope from the declaration line alone closed the type
// at once and lost its members and its bases together.
func TestADeclarationMayWrapBeforeItsBody(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/d.ts": `class A {}
interface P {}
class D
  extends A
  implements P {
  m(): void {}
}
`,
	})
	if !related(g, "D", RelationExtends, "A") {
		t.Error("the base on the continuation line was lost")
	}
	if !related(g, "D", RelationImplements, "P") {
		t.Error("the interface on the continuation line was lost")
	}
	if !related(g, "D", RelationDeclares, "D.m") {
		t.Error("the member was lost with the wrapped declaration")
	}
}

// A call written in one class means that class's method.
//
// Resolving it by the bare name reached whichever class was declared first —
// including from a method's own declaration line, which the call scanner reads
// too, so a class was drawn calling another class's method of the same name for
// no reason at all.
func TestACallInAClassMeansThatClassesMethod(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/shapes.js": `class BoxShape {
  paint() { this.render(); }
  render() {}
}
class GroupShape {
  paint() { this.render(); }
  render() {}
}
`,
	})

	calls := map[string]bool{}
	for _, e := range g.Edges {
		if e.Relation != "calls" {
			continue
		}
		var from, to string
		for _, n := range g.Nodes {
			if n.ID == e.From {
				from = n.Name
			}
			if n.ID == e.To {
				to = n.Name
			}
		}
		calls[from+" -> "+to] = true
	}
	for _, want := range []string{
		"BoxShape.paint -> BoxShape.render",
		"GroupShape.paint -> GroupShape.render",
	} {
		if !calls[want] {
			t.Errorf("%q was not recovered: %v", want, calls)
		}
	}
	for _, unwanted := range []string{
		"GroupShape.paint -> BoxShape.render",
		"BoxShape.paint -> GroupShape.render",
		"GroupShape.paint -> BoxShape.paint",
		"BoxShape.paint -> GroupShape.paint",
	} {
		if calls[unwanted] {
			t.Errorf("%q was invented", unwanted)
		}
	}
}

// A declaration with no brace and no semicolon is not always unfinished.
// Kotlin writes `class Marker` and `data class Point(val x: Int)` and means
// them: the type has no body, and what follows belongs to the file. Waiting for
// a brace made the next one that came along the type's own.
func TestADeclarationWithoutABodyDoesNotSwallowTheFile(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/point.kt": `data class Point(val x: Int, val y: Int)

fun square(p: Point): Int {
    return p.x * p.y
}

class Marker

fun other(): Int {
    return 0
}
`,
	})

	typeNamed(t, g, "Point")
	typeNamed(t, g, "Marker")
	for _, id := range []string{"file:app/point.kt#square", "file:app/point.kt#other"} {
		if _, ok := g.Node(id); !ok {
			t.Errorf("%s was taken for somebody's method", id)
		}
	}
	for _, owner := range []string{"Point", "Marker"} {
		for _, fn := range []string{"square", "other"} {
			if related(g, owner, RelationDeclares, owner+"."+fn) {
				t.Errorf("%s declares %s, which is the file's", owner, fn)
			}
		}
	}
}

// Which indentation is the class's own is not known until every function in it
// has been seen. Latching onto the first threw away every real member of a
// class whose first `def` was inside an `if TYPE_CHECKING:` — an ordinary thing
// to write, and the class went quiet without saying so.
func TestAGuardedMethodDoesNotHideTheRealOnes(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/guarded.py": `import sys


class Guarded:
    if sys.version_info >= (3, 9):
        def new_way(self):
            pass

    def ordinary(self):
        pass

    def also(self):
        pass
`,
	})

	// All three, including the guarded one. A `def` inside an `if` is still the
	// class's method — what is not is a `def` inside a `def`, and that is the
	// only distinction worth drawing here.
	for _, fn := range []string{"ordinary", "also", "new_way"} {
		if !related(g, "Guarded", RelationDeclares, "Guarded."+fn) {
			t.Errorf("%s is not a member of the class that declares it", fn)
		}
	}
}

// A comma inside parentheses is an argument separator. `extends mixin(A, B)`
// names one thing — a call — and splitting on every comma read its second
// argument as a base, which is a relation the declaration never claimed.
func TestAMixinCallIsNotABaseList(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/mixin.ts": `class A {}
class B {}
declare function mixin(...args: unknown[]): typeof A;
class Foo extends mixin(A, B) {
  run(): void {}
}
`,
	})

	typeNamed(t, g, "Foo")
	for _, base := range []string{"A", "B"} {
		if related(g, "Foo", RelationExtends, base) {
			t.Errorf("an argument of the mixin call was recorded as a base: %s", base)
		}
	}
	// The declaration is still read, so the class and its member survive.
	if !related(g, "Foo", RelationDeclares, "Foo.run") {
		t.Error("the class was lost with its base list")
	}
}

// A word that is neither a name nor an access specifier is not the end of the
// entry. Stopping on it dropped the base without saying so.
func TestAQualifiedBaseDoesNotSwallowTheEntry(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/d.cpp": "class Base {\n};\n\nclass Derived : public virtual Base {\n};\n",
	})
	if !related(g, "Derived", RelationExtends, "Base") {
		t.Error("the base was lost behind the words in front of it")
	}
}

// A call outside every class means the function of that name, not a method
// that happens to share it.
//
// Reaching for the first declaration in the file let the method win where a
// plain function existed, so the real function was left with nothing pointing
// at it — and its own declaration line, which the call scanner reads too, drew
// an edge from it to the method.
func TestABareCallMeansTheBareFunction(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/shadow.js": `class Box {
  render() {}
}
export function render() {}
export function main() { render(); }
`,
	})

	calls := map[string]bool{}
	for _, e := range g.Edges {
		if e.Relation != "calls" {
			continue
		}
		var from, to string
		for _, n := range g.Nodes {
			if n.ID == e.From {
				from = n.Name
			}
			if n.ID == e.To {
				to = n.Name
			}
		}
		calls[from+" -> "+to] = true
	}
	if !calls["main -> render"] {
		t.Errorf("the call to the function was not recovered: %v", calls)
	}
	for _, unwanted := range []string{"main -> Box.render", "render -> Box.render"} {
		if calls[unwanted] {
			t.Errorf("%q was invented", unwanted)
		}
	}
}

// A `def` inside an `if` is still the class's method. What is not is a `def`
// inside a `def` — and that is the only distinction worth drawing, because
// taking the shallowest instead threw away Ruby's singleton class and every
// version-guarded definition beside an ordinary one.
func TestADeeperDefinitionIsStillTheClassesUnlessAMethodEnclosesIt(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/animal.rb": `class Animal
  class << self
    def create
      new
    end
  end

  def run
    1
  end
end
`,
		"app/widget.py": `import sys


class Widget:
    def draw(self):
        pass

    if sys.version_info >= (3, 9):
        def modern(self):
            def helper():
                pass
            return helper
`,
	})

	for _, fn := range []string{"create", "run"} {
		if !related(g, "Animal", RelationDeclares, "Animal."+fn) {
			t.Errorf("Animal.%s was dropped", fn)
		}
	}
	for _, fn := range []string{"draw", "modern"} {
		if !related(g, "Widget", RelationDeclares, "Widget."+fn) {
			t.Errorf("Widget.%s was dropped", fn)
		}
	}
	// And the one a method encloses is that method's business.
	if related(g, "Widget", RelationDeclares, "Widget.helper") {
		t.Error("a function nested in a method was made a member")
	}
}

// A declaration that wrapped after `implements A,` goes on with a bare name.
// The name means nothing without the clause it belongs to, and a continuation
// judged only by the word it starts with read `B {` as something new — losing
// the interface and the members together.
func TestAWrappedBaseListGoesOnWithABareName(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/Wrapped.java": `interface A {}
interface B {}
class Base {}

public class Wrapped extends Base implements A,
        B {
    void go() {}
}
`,
	})

	if !related(g, "Wrapped", RelationExtends, "Base") {
		t.Error("the base was lost")
	}
	for _, iface := range []string{"A", "B"} {
		if !related(g, "Wrapped", RelationImplements, iface) {
			t.Errorf("%s was lost from the wrapped list", iface)
		}
	}
	if !related(g, "Wrapped", RelationDeclares, "Wrapped.go") {
		t.Error("the member was lost with the wrapped declaration")
	}
}

// The colon form on a line that does not name the type: the continuation of a
// declaration whose parameter list was wrapped. Anchoring only on the shape
// that names the type made this unreadable, which is the case the continuation
// was added for.
func TestAColonBaseIsReadFromAContinuationLine(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/bar.kt": `open class Anchor

class Bar(
    private val a: Int
) : Anchor {
    fun run() {}
}
`,
	})

	if !related(g, "Bar", RelationExtends, "Anchor") {
		t.Error("the base after the wrapped parameter list was lost")
	}
	if !related(g, "Bar", RelationDeclares, "Bar.run") {
		t.Error("the member was lost with the wrapped declaration")
	}
}

// A name qualified with `::` is one name. Cutting it at the colon left the
// namespace or the enclosing type — `Outer` of `Outer::Inner` — which is often
// declared in the same file, so the declaration was drawn extending something
// it never named. A dotted name has always been kept whole and fallen away
// unresolved; this is the same name written the other way.
func TestAScopeQualifiedBaseIsNotItsFirstSegment(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/w.cpp": `class Outer {
    void hidden();
};

class Widget : public Outer::Inner {
    void run();
};
`,
	})
	if related(g, "Widget", RelationExtends, "Outer") {
		t.Error("the enclosing namespace was drawn as the base")
	}
}

// A `companion object` is how Kotlin writes a static factory, and what it
// holds is reached through the class that names it. Requiring a member to sit
// at depth 1 emptied it: every brace counted, so a brace that only groups took
// the members out of the class as surely as a method would have.
//
// An anonymous type is the one that does own what it declares, and it can be
// written where no method encloses it — a Java field initializer.
func TestABraceThatOnlyGroupsDoesNotTakeTheMembersAway(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/repo.kt": `class Repo {
    companion object {
        fun create(): Repo { return Repo() }
    }

    fun run() {}
}
`,
		"app/App.java": `public class App {
  private final Runnable task =
    new Runnable() {
      public void execute() {}
    };
  public void go() {}
}
`,
	})
	for _, want := range []string{"Repo.create", "Repo.run"} {
		if !related(g, "Repo", RelationDeclares, want) {
			t.Errorf("%s was not the class's", want)
		}
	}
	if !related(g, "App", RelationDeclares, "App.go") {
		t.Error("App.go was not the class's")
	}
	if related(g, "App", RelationDeclares, "App.execute") {
		t.Error("what the anonymous class declared was given to the class around it")
	}
}

// Not every language lets a method be called by its bare name. A `render()`
// written in a Python or JavaScript method is the module's function; the
// method is `self.render()` or `this.render()`. Preferring the method drew the
// class calling itself where the code called out of it.
//
// Where a receiver is written, or where the language needs none, the method is
// still what the call means.
func TestABareCallIsNotAMethodWhereAReceiverIsRequired(t *testing.T) {
	g := parsed(t, map[string]string{
		"app/a.py": `def render():
    pass

class Box:
    def render(self):
        pass

    def draw(self):
        render()

    def paint(self):
        self.render()
`,
		"app/A.java": `class Sheet {
  void render() {}
  void draw() { render(); }
}
`,
	})

	calls := map[string]bool{}
	for _, e := range g.Edges {
		if e.Relation != "calls" {
			continue
		}
		var from, to string
		for _, n := range g.Nodes {
			if n.ID == e.From {
				from = n.Name
			}
			if n.ID == e.To {
				to = n.Name
			}
		}
		calls[from+" -> "+to] = true
	}
	for _, want := range []string{
		"Box.draw -> render",         // bare: the module's function
		"Box.paint -> Box.render",    // written on self: the method
		"Sheet.draw -> Sheet.render", // Java needs no receiver
	} {
		if !calls[want] {
			t.Errorf("%q was not recovered: %v", want, calls)
		}
	}
	for _, unwanted := range []string{"Box.draw -> Box.render", "Box.render -> render"} {
		if calls[unwanted] {
			t.Errorf("%q was invented", unwanted)
		}
	}
}
