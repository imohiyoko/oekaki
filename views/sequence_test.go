package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// The references say a request could go gateway, checkout, ledger. A trace says
// one went gateway, checkout, audit. They are different claims, and the second
// one is the one a reader asked for when they opened a sequence.
func tracedChain() *core.Graph {
	g := core.New()
	for _, id := range []string{"gateway", "checkout", "ledger", "audit"} {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	g.Edges = []core.Edge{
		{From: "gateway", To: "checkout", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "checkout", To: "ledger", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "checkout", To: "audit", Kind: core.EdgeObserved, Relation: "calls"},
	}
	g.Normalize()
	return g
}

func sequenceOf(t *testing.T, g *core.Graph, root string) *Diagram {
	t.Helper()
	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := find(a, sequenceID(root))
	if d == nil {
		t.Fatalf("no sequence for %q", root)
	}
	return d
}

func walkOf(d *Diagram) []string {
	var out []string
	for _, e := range d.Graph.Edges {
		out = append(out, e.From+"→"+e.To)
	}
	return out
}

// With nothing recorded, the order is a reading of the references and the page
// says so.
func TestASequenceWithoutARecordSaysItsOrderIsDerived(t *testing.T) {
	d := sequenceOf(t, tracedChain(), "gateway")
	if d.Order != OrderDerived {
		t.Fatalf("the order is %q", d.Order)
	}
	if d.Subtitle == "" || d.Subtitle[len(d.Subtitle)-4:] == "" {
		t.Fatalf("the subtitle says nothing: %q", d.Subtitle)
	}
}

// A route something walked beats a walk this package worked out, and the page
// says which of the two it is drawing.
func TestARecordedRouteBeatsTheDerivedWalk(t *testing.T) {
	g := tracedChain()
	g.Paths = []core.Path{{
		Nodes: []string{"gateway", "checkout", "audit"}, Kind: core.EdgeObserved,
		Claim: &core.Claim{Origin: core.OriginParser, Note: "request traces"},
	}}
	g.Normalize()

	d := sequenceOf(t, g, "gateway")
	if d.Order != OrderObserved {
		t.Fatalf("a recorded route was drawn as %q", d.Order)
	}
	got := walkOf(d)
	if len(got) != 2 || got[0] != "gateway→checkout" || got[1] != "checkout→audit" {
		t.Fatalf("got %v, want the route that was walked", got)
	}
	for _, e := range d.Graph.Edges {
		if e.Attrs["step"] == nil {
			t.Fatalf("a message with no step: %#v", e)
		}
	}
}

// A declared route is the same kind of reading the walk already is. Preferring
// it would say "observed" about an order nobody saw.
func TestADeclaredRouteIsNotAnObservedOrder(t *testing.T) {
	g := tracedChain()
	g.Paths = []core.Path{{Nodes: []string{"gateway", "checkout", "ledger"}, Kind: core.EdgeIACRef}}
	g.Normalize()

	if d := sequenceOf(t, g, "gateway"); d.Order != OrderDerived {
		t.Fatalf("a declared route was presented as %q", d.Order)
	}
}

// A route that goes further tells the reader more, and the shorter ones are
// usually its beginning.
func TestTheLongestRecordedRouteWins(t *testing.T) {
	g := tracedChain()
	g.Paths = []core.Path{
		{Nodes: []string{"gateway", "checkout"}, Kind: core.EdgeObserved},
		{Nodes: []string{"gateway", "checkout", "audit"}, Kind: core.EdgeObserved},
	}
	g.Normalize()

	if got := walkOf(sequenceOf(t, g, "gateway")); len(got) != 2 {
		t.Fatalf("got %v, want the longer of the two", got)
	}
}

// A step of a recorded route with no edge under it is still drawn: the route
// saying a request went from here to there is already the claim that it went,
// and dropping it would lose evidence the document has.
func TestAStepWithNoEdgeUnderItIsStillDrawn(t *testing.T) {
	g := tracedChain()
	g.Paths = []core.Path{{
		Nodes: []string{"gateway", "checkout", "ledger", "audit"}, Kind: core.EdgeObserved,
		Claim: &core.Claim{Origin: core.OriginParser, Note: "request traces"},
	}}
	g.Normalize()

	d := sequenceOf(t, g, "gateway")
	got := walkOf(d)
	if len(got) != 3 || got[2] != "ledger→audit" {
		t.Fatalf("got %v, want every step of the route that was walked", got)
	}
	for _, e := range d.Graph.Edges {
		if e.From == "ledger" && e.Claim == nil {
			t.Fatalf("a step with no edge under it carries no claim: %#v", e)
		}
	}
}
