package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A small estate with two namespaces, a workload that holds two applications,
// and a call chain that leaves one namespace for the other.
func cluster() *core.Graph {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork, Label: "network"}}
	g.Groups = []core.Group{
		{ID: "ns:shop", Axis: core.AxisNetwork, Type: "namespace", Label: "shop"},
		{ID: "ns:pay", Axis: core.AxisNetwork, Type: "namespace", Label: "pay"},
		{ID: "ns:shop/tier", Axis: core.AxisNetwork, Type: "tier", Label: "tier", Parent: str("ns:shop")},
	}
	g.Nodes = []core.Node{
		{ID: "svc:web", Type: "service", Name: "web", Groups: map[string]string{core.AxisNetwork: "ns:shop"}},
		{ID: "svc:api", Type: "service", Name: "api", Groups: map[string]string{core.AxisNetwork: "ns:shop/tier"}},
		{ID: "svc:billing", Type: "service", Name: "billing", Groups: map[string]string{core.AxisNetwork: "ns:pay"}},
		{ID: "app:checkout", Type: "application", Name: "checkout", Groups: map[string]string{core.AxisNetwork: "ns:shop/tier"}},
		{ID: "app:cart", Type: "application", Name: "cart", Groups: map[string]string{core.AxisNetwork: "ns:shop/tier"}},
	}
	g.Edges = []core.Edge{
		{From: "svc:web", To: "svc:api", Kind: core.EdgeObserved, Relation: "calls"},
		{From: "svc:api", To: "svc:billing", Kind: core.EdgeObserved, Relation: "calls"},
		{From: "svc:api", To: "app:checkout", Kind: core.EdgeIACRef, Relation: "runs"},
		{From: "svc:api", To: "app:cart", Kind: core.EdgeIACRef, Relation: "runs"},
	}
	g.Normalize()
	return g
}

func str(s string) *string { return &s }

func find(a *Atlas, id string) *Diagram {
	for i := range a.Diagrams {
		if a.Diagrams[i].ID == id {
			return &a.Diagrams[i]
		}
	}
	return nil
}

func opening(d *Diagram, element string) *Opening {
	for i := range d.Opens {
		if d.Opens[i].Element == element {
			return &d.Opens[i]
		}
	}
	return nil
}

// The complaint the atlas answers: the first thing a reader sees is a list of
// containers, not every resource in the estate at once.
func TestRootLevelDrawsContainersAsBoxesAndNothingInside(t *testing.T) {
	a, err := BuildAtlas(cluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := find(a, a.Root)
	if root == nil {
		t.Fatalf("no root diagram in %d", len(a.Diagrams))
	}
	if root.Kind != KindPackage {
		t.Fatalf("a level of two namespaces is a %s", root.Kind)
	}
	ids := map[string]bool{}
	for _, n := range root.Graph.Nodes {
		ids[n.ID] = true
	}
	if !ids["ns:shop"] || !ids["ns:pay"] {
		t.Fatalf("a namespace is missing: %#v", ids)
	}
	if ids["svc:api"] || ids["ns:shop/tier"] {
		t.Fatalf("something below this level was drawn on it: %#v", ids)
	}
	if len(root.Graph.Groups) != 0 {
		t.Fatalf("a level nests nothing, got %d groups", len(root.Graph.Groups))
	}
}

// An edge between two things deep inside different containers is what makes
// the top level worth drawing at all: it says these two namespaces talk.
func TestEdgesAreLiftedToTheLevelBeingDrawn(t *testing.T) {
	a, err := BuildAtlas(cluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := find(a, a.Root)
	found := false
	for _, e := range root.Graph.Edges {
		if e.From == "ns:shop" && e.To == "ns:pay" {
			found = true
		}
		if e.From == e.To {
			t.Fatalf("a reference inside one container became a line to itself: %#v", e)
		}
	}
	if !found {
		t.Fatalf("the call from shop to pay did not reach the top level: %#v", root.Graph.Edges)
	}
}

// Opening a container arrives at its own level: its children as boxes, its own
// members as members. This is the namespace-then-pods reading.
func TestOpeningAContainerArrivesAtItsOwnLevel(t *testing.T) {
	a, err := BuildAtlas(cluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	open := opening(find(a, a.Root), "ns:shop")
	if open == nil {
		t.Fatal("the namespace box has no way in")
	}
	shop := find(a, open.Diagram)
	if shop == nil {
		t.Fatalf("the atlas promises %q and does not carry it", open.Diagram)
	}
	if shop.Parent != a.Root || shop.Origin != "ns:shop" {
		t.Fatalf("the trail back up is wrong: parent %q origin %q", shop.Parent, shop.Origin)
	}
	ids := map[string]bool{}
	for _, n := range shop.Graph.Nodes {
		ids[n.ID] = true
	}
	if !ids["svc:web"] || !ids["ns:shop/tier"] {
		t.Fatalf("shop should hold its own service and its child container: %#v", ids)
	}
	if ids["svc:api"] {
		t.Fatalf("a grandchild's member was drawn one level too high: %#v", ids)
	}
}

// Clicking a resource asks what is in it. What it runs is inside; what it
// merely calls is not, and the page has to say which is which.
func TestDetailSeparatesWhatIsHeldFromWhatIsCalled(t *testing.T) {
	a, err := BuildAtlas(cluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := find(a, detailID("svc:api"))
	if d == nil {
		t.Fatal("no detail page for the workload")
	}
	if d.Kind != KindDetail {
		t.Fatalf("a node that runs two applications has a %s page", d.Kind)
	}
	inside := map[string]bool{}
	for _, n := range d.Graph.Nodes {
		if n.ID == "svc:api" {
			continue
		}
		held, ok := n.Attrs["inside"].(bool)
		if !ok {
			t.Fatalf("%s does not say whether it is inside", n.ID)
		}
		inside[n.ID] = held
	}
	if !inside["app:checkout"] || !inside["app:cart"] {
		t.Fatalf("what the workload runs should be inside it: %#v", inside)
	}
	if inside["svc:web"] || inside["svc:billing"] {
		t.Fatalf("something it only talks to was drawn as contained: %#v", inside)
	}
}

// A node with nothing but calls is a communication page, and a node nothing
// touches has no page at all rather than an empty one.
func TestPagesExistOnlyWhereThereIsSomethingToSee(t *testing.T) {
	g := cluster()
	g.Nodes = append(g.Nodes, core.Node{ID: "svc:lonely", Type: "service", Name: "lonely", Groups: map[string]string{core.AxisNetwork: "ns:pay"}})
	g.Normalize()
	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d := find(a, detailID("svc:billing")); d == nil || d.Kind != KindCommunication {
		t.Fatalf("a node that is only called should get a communication page, got %#v", d)
	}
	if find(a, detailID("svc:lonely")) != nil {
		t.Fatal("a node nothing touches was given a page into an empty room")
	}
	pay := find(a, levelID("ns:pay"))
	if opening(pay, "svc:lonely") != nil {
		t.Fatal("the level offers a door into that empty room")
	}
}

// A sequence is the same edges in an order, and the order has to be stable and
// has to stop: a graph with a cycle in it is normal.
func TestSequenceNumbersStepsDepthFirstAndTerminates(t *testing.T) {
	g := cluster()
	g.Edges = append(g.Edges, core.Edge{From: "svc:billing", To: "svc:web", Kind: core.EdgeObserved, Relation: "calls"})
	g.Normalize()
	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seq := find(a, sequenceID("svc:web"))
	if seq == nil {
		t.Fatal("no sequence for the service that starts the chain")
	}
	var order []string
	for _, e := range seq.Graph.Edges {
		step, ok := e.Attrs["step"].(int)
		if !ok {
			t.Fatalf("a message with no step: %#v", e)
		}
		if step != len(order)+1 {
			t.Fatalf("steps out of order at %d: %#v", step, e)
		}
		order = append(order, e.From+"->"+e.To)
	}
	want := []string{"svc:web->svc:api", "svc:api->svc:billing", "svc:billing->svc:web"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("got %v, want %v", order, want)
		}
	}
}

// Containment is not a message. A sequence built from "runs" would read as a
// call nobody claimed.
func TestSequenceIgnoresContainment(t *testing.T) {
	a, err := BuildAtlas(cluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seq := find(a, sequenceID("svc:api"))
	if seq == nil {
		t.Fatal("no sequence for the caller")
	}
	for _, e := range seq.Graph.Edges {
		if e.Relation == "runs" {
			t.Fatalf("containment became a message: %#v", e)
		}
	}
}

// Depth bounds the walk, because a call chain can be as long as the estate.
func TestSequenceDepthIsBounded(t *testing.T) {
	a, err := BuildAtlas(cluster(), AtlasOptions{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	seq := find(a, sequenceID("svc:web"))
	if seq == nil || len(seq.Graph.Edges) != 1 {
		t.Fatalf("depth 1 should stop after one message: %#v", seq)
	}
}

// The atlas is committable only if it is derived the same way twice.
func TestAtlasIsDeterministic(t *testing.T) {
	first, err := BuildAtlas(cluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildAtlas(cluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Diagrams) != len(second.Diagrams) {
		t.Fatalf("%d diagrams then %d", len(first.Diagrams), len(second.Diagrams))
	}
	for i := range first.Diagrams {
		if first.Diagrams[i].ID != second.Diagrams[i].ID {
			t.Fatalf("diagram %d is %q then %q", i, first.Diagrams[i].ID, second.Diagrams[i].ID)
		}
	}
}

// An estate with ten thousand resources would otherwise produce a document
// nobody can open.
func TestLimitBoundsTheDocument(t *testing.T) {
	a, err := BuildAtlas(cluster(), AtlasOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Diagrams) > 2 {
		t.Fatalf("limit 2 produced %d diagrams", len(a.Diagrams))
	}
}

// A graph with no containment at all is the ordinary case for a source-parsed
// repository, and it has to produce a readable first page rather than an
// error.
func TestFlatGraphStillHasARootLevel(t *testing.T) {
	a, err := BuildAtlas(testGraph(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := find(a, a.Root)
	if root == nil || root.Kind != KindArchitecture {
		t.Fatalf("a flat graph should open on an architecture level: %#v", root)
	}
	if len(root.Graph.Nodes) != 3 {
		t.Fatalf("got %d nodes", len(root.Graph.Nodes))
	}
}
