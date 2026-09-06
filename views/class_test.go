package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A small code graph: an Order that holds a Line, extends a Base, and declares
// two methods; and a package-level function that is nobody's method.
func typed() *core.Graph {
	g := core.New()
	g.Axes = []core.Axis{{ID: "source", Label: "Source"}}
	file := core.Node{ID: "file:shop/order.go", Type: "code_file", Name: "shop/order.go"}
	g.Nodes = []core.Node{
		file,
		{ID: "file:shop/order.go#type:Order", Type: "code_type", Name: "Order",
			Attrs: map[string]any{"kind": "struct"}},
		{ID: "file:shop/order.go#type:Line", Type: "code_type", Name: "Line",
			Attrs: map[string]any{"kind": "struct"}},
		{ID: "file:shop/order.go#type:Base", Type: "code_type", Name: "Base",
			Attrs: map[string]any{"kind": "struct"}},
		{ID: "file:shop/order.go#Order.Total", Type: "code_function", Name: "Order.Total"},
		{ID: "file:shop/order.go#Order.Save", Type: "code_function", Name: "Order.Save"},
		{ID: "file:shop/order.go#Free", Type: "code_function", Name: "Free"},
	}
	ref := func(from, to, relation string) core.Edge {
		return core.Edge{From: from, To: to, Kind: core.EdgeIACRef, Relation: relation}
	}
	g.Edges = []core.Edge{
		ref("file:shop/order.go", "file:shop/order.go#type:Order", "contains"),
		ref("file:shop/order.go", "file:shop/order.go#type:Line", "contains"),
		ref("file:shop/order.go", "file:shop/order.go#type:Base", "contains"),
		ref("file:shop/order.go", "file:shop/order.go#Order.Total", "contains"),
		ref("file:shop/order.go", "file:shop/order.go#Order.Save", "contains"),
		ref("file:shop/order.go", "file:shop/order.go#Free", "contains"),
		ref("file:shop/order.go#type:Order", "file:shop/order.go#Order.Total", "declares"),
		ref("file:shop/order.go#type:Order", "file:shop/order.go#Order.Save", "declares"),
		ref("file:shop/order.go#type:Order", "file:shop/order.go#type:Line", "has_field"),
		ref("file:shop/order.go#type:Order", "file:shop/order.go#type:Base", "embeds"),
	}
	g.Normalize()
	return g
}

func page(t *testing.T, a *Atlas, id string) Diagram {
	t.Helper()
	for _, d := range a.Diagrams {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("no page %q", id)
	return Diagram{}
}

// A type is drawn as a class: one page, read the way the thing itself is
// written.
func TestATypeOpensAsAClassDiagram(t *testing.T) {
	a, err := BuildAtlas(typed(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := page(t, a, "detail:file:shop/order.go#type:Order")
	if d.Kind != KindClass {
		t.Fatalf("a type opens as %q", d.Kind)
	}
	if d.Title != "Order" {
		t.Errorf("the page is titled %q", d.Title)
	}
}

// UML puts members inside the box, and it is right to: a class with nine
// methods drawn as nine boxes is a picture of nine things, when it is a
// picture of one thing with nine methods.
func TestWhatAClassDeclaresIsInTheBoxAndNotBesideIt(t *testing.T) {
	a, err := BuildAtlas(typed(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := page(t, a, "detail:file:shop/order.go#type:Order")

	for _, n := range d.Graph.Nodes {
		if n.Type == "code_function" {
			t.Errorf("%q is drawn as a box beside its own class", n.Name)
		}
	}
	var centre *core.Node
	for i, n := range d.Graph.Nodes {
		if n.ID == "file:shop/order.go#type:Order" {
			centre = &d.Graph.Nodes[i]
		}
	}
	if centre == nil {
		t.Fatal("the class is not on its own page")
	}
	declares, _ := centre.Attrs["declares"].([]string)
	if len(declares) != 2 {
		t.Fatalf("the box lists %#v", centre.Attrs["declares"])
	}
	// Inside the class the receiver is the box it is written in.
	if declares[0] != "Save" || declares[1] != "Total" {
		t.Errorf("the members still carry their receiver: %#v", declares)
	}
}

// Nothing is lost by listing them: the file that contains the type contains
// its functions too, and that page still draws every one of them as a box a
// reader can open.
func TestTheMethodsAreStillReachableFromTheFile(t *testing.T) {
	a, err := BuildAtlas(typed(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := page(t, a, "detail:file:shop/order.go")

	drawn := map[string]bool{}
	for _, n := range d.Graph.Nodes {
		drawn[n.Name] = true
	}
	for _, want := range []string{"Order.Total", "Order.Save", "Free"} {
		if !drawn[want] {
			t.Errorf("%q cannot be reached from the file that contains it", want)
		}
	}
}

// The types a declaration mentions are the diagram. Each one is a box that
// opens as its own class, which is how a reader walks a design.
func TestTheTypesADeclarationMentionsAreDrawnAndOpen(t *testing.T) {
	a, err := BuildAtlas(typed(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := page(t, a, "detail:file:shop/order.go#type:Order")

	drawn := map[string]bool{}
	for _, n := range d.Graph.Nodes {
		drawn[n.Name] = true
	}
	for _, want := range []string{"Line", "Base"} {
		if !drawn[want] {
			t.Errorf("%q is not on the class diagram", want)
		}
	}
	relations := map[string]bool{}
	for _, e := range d.Graph.Edges {
		relations[e.Relation] = true
	}
	for _, want := range []string{"has_field", "embeds"} {
		if !relations[want] {
			t.Errorf("the %s relation is not drawn", want)
		}
	}
	opens := map[string]Kind{}
	for _, o := range d.Opens {
		opens[o.Element] = o.Kind
	}
	if opens["file:shop/order.go#type:Line"] != KindClass {
		t.Errorf("a type on a class diagram does not open as one: %#v", d.Opens)
	}
}

// Every page still has to hold together on its own terms.
func TestAClassPageValidates(t *testing.T) {
	a, err := BuildAtlas(typed(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range a.Diagrams {
		if err := d.Graph.Validate(); err != nil {
			t.Fatalf("%s: %v", d.ID, err)
		}
	}
}
