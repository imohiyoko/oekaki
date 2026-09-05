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
		{ID: "tier", Axis: core.AxisNetwork, Type: "tier", Label: "tier", Parent: str("ns:shop")},
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
	if !ids["svc:web"] || !ids["tier"] {
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

// The trail back up is containment, not the route a reader happened to take.
// app:checkout is derived while building the page for the workload that runs
// it, and it still hangs off the level it lives in — otherwise the same
// element reached from two neighbours would offer two different ways home.
func TestDetailHangsOffItsOwnLevel(t *testing.T) {
	a, err := BuildAtlas(cluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := find(a, detailID("app:checkout"))
	if d == nil {
		t.Fatal("no page for the application")
	}
	if want := levelID("ns:shop/tier"); d.Parent != want {
		t.Fatalf("parent is %q, want %q", d.Parent, want)
	}
	// And the chain from there reaches the root without a gap in it.
	seen := 0
	for at := d; at != nil && at.Parent != ""; seen++ {
		at = find(a, at.Parent)
		if at == nil {
			t.Fatalf("the trail names a diagram the atlas does not carry")
		}
		if seen > 8 {
			t.Fatal("the trail does not reach the root")
		}
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

// Either end of an edge may be a container. Following one into a sequence
// produced a message to a participant the diagram had no lifeline for, and
// because every projected graph is validated, that was not a wrong picture but
// no picture at all — the error travelled out of BuildAtlas and the render
// wrote nothing.
func TestACallToAContainerDoesNotBreakTheWholeAtlas(t *testing.T) {
	g := cluster()
	g.Edges = append(g.Edges, core.Edge{From: "svc:web", To: "ns:pay", Kind: core.EdgeObserved, Relation: "calls"})
	g.Normalize()

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatalf("an edge pointing at a container stopped the atlas: %v", err)
	}
	seq := find(a, sequenceID("svc:web"))
	if seq == nil {
		t.Fatal("the sequence is missing entirely")
	}
	for _, e := range seq.Graph.Edges {
		if e.To == "ns:pay" {
			t.Fatalf("a container became a participant: %#v", e)
		}
	}
}

// A reference somebody asserted is not real and a reference that is are two
// different facts about the same pair of containers. The line has to be the
// real one — drawing it as denied says the relationship does not exist — and
// the denial has to survive somewhere, because "somebody said this is wrong"
// is itself worth knowing.
func TestLiftingKeepsSuppressedReferencesApart(t *testing.T) {
	g := cluster()
	g.Edges = append(g.Edges,
		core.Edge{From: "svc:web", To: "svc:billing", Kind: core.EdgeObserved, Relation: "calls",
			Suppressed: true, Claim: &core.Claim{Origin: core.OriginHuman, Note: "not real"}},
		core.Edge{From: "app:cart", To: "svc:billing", Kind: core.EdgeObserved, Relation: "calls"},
	)
	g.Normalize()

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var line *core.Edge
	for i, e := range find(a, a.Root).Graph.Edges {
		if e.From == "ns:shop" && e.To == "ns:pay" && e.Relation == "calls" {
			line = &find(a, a.Root).Graph.Edges[i]
		}
	}
	if line == nil {
		t.Fatal("the two containers are not joined at all")
	}
	if line.Suppressed {
		t.Fatal("a real reference between the two containers was drawn as denied")
	}
	if line.Attrs["suppressed_references"] == nil {
		t.Fatalf("the denial was lost in the fold: %#v", line.Attrs)
	}
}

// A pair whose references were all denied still gets its line, drawn as
// denied. "Somebody said this is wrong" and "this never existed" are different
// facts, and only the first one is true.
func TestAPairWhoseReferencesAreAllDeniedKeepsItsLine(t *testing.T) {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork}}
	g.Groups = []core.Group{
		{ID: "one", Axis: core.AxisNetwork, Type: "namespace", Label: "one"},
		{ID: "two", Axis: core.AxisNetwork, Type: "namespace", Label: "two"},
	}
	g.Nodes = []core.Node{
		{ID: "a", Type: "service", Name: "a", Groups: map[string]string{core.AxisNetwork: "one"}},
		{ID: "b", Type: "service", Name: "b", Groups: map[string]string{core.AxisNetwork: "two"}},
	}
	g.Edges = []core.Edge{{From: "a", To: "b", Kind: core.EdgeObserved, Relation: "calls",
		Suppressed: true, Claim: &core.Claim{Origin: core.OriginHuman}}}
	g.Normalize()

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	edges := find(a, a.Root).Graph.Edges
	if len(edges) != 1 || !edges[0].Suppressed {
		t.Fatalf("a denied reference should still be drawn, and drawn as denied: %#v", edges)
	}
}

// The viewer decides a contested box's stroke, an abnormal reading's red, the
// label filters and the timeline from these arrays. A page that dropped them
// draws an estate where nothing is contested and nothing is wrong.
func TestEvidenceTravelsWithThePageItBelongsTo(t *testing.T) {
	g := cluster()
	value := 91.0
	g.Observations = []core.Observation{
		{Subject: "svc:api", Metric: "cpu", Value: &value, State: "abnormal", ObservedAt: "2026-08-28T00:00:00Z"},
		{Subject: "svc:billing", Metric: "cpu", Value: &value, ObservedAt: "2026-08-28T00:00:00Z"},
	}
	g.LogRecords = []core.LogRecordSummary{{ID: "r1", Source: "svc:api", Labels: []string{"error"}}}
	g.Normalize()

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := find(a, detailID("svc:api"))
	subjects := map[string]bool{}
	for _, o := range d.Graph.Observations {
		subjects[o.Subject] = true
	}
	if !subjects["svc:api"] {
		t.Fatal("the reading about this page's own subject did not travel with it")
	}
	if len(d.Graph.LogRecords) != 1 {
		t.Fatalf("got %d log records", len(d.Graph.LogRecords))
	}

	// And evidence about something a page does not draw stays behind, or the
	// page is a document with a dangling reference in it.
	tier := find(a, levelID("ns:shop/tier"))
	for _, o := range tier.Graph.Observations {
		if o.Subject == "svc:billing" {
			t.Fatal("a reading about a node this level does not draw came along")
		}
	}
}

// A page records what it opens before the pages behind it are built, so the
// bound reached in the middle of that used to leave doors that do nothing.
func TestNothingOpensOntoADiagramThatWasNotBuilt(t *testing.T) {
	a, err := BuildAtlas(cluster(), AtlasOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	built := map[string]bool{}
	for _, d := range a.Diagrams {
		built[d.ID] = true
	}
	for _, d := range a.Diagrams {
		if d.Parent != "" && !built[d.Parent] {
			t.Errorf("%q hangs off %q, which was never built", d.ID, d.Parent)
		}
		for _, o := range d.Opens {
			if !built[o.Diagram] {
				t.Errorf("%q offers a way into %q, which was never built", d.ID, o.Diagram)
			}
		}
	}
}

// Containment is matched exactly. On substring terms a workload naming its
// ServiceAccount — runs-as — reads as runs, and the account is drawn inside
// the workload it merely authenticates as.
func TestANearMissIsNotContainment(t *testing.T) {
	g := cluster()
	g.Nodes = append(g.Nodes, core.Node{ID: "sa:api", Type: "serviceaccount", Name: "api", Groups: map[string]string{core.AxisNetwork: "ns:shop/tier"}})
	g.Edges = append(g.Edges, core.Edge{From: "svc:api", To: "sa:api", Kind: core.EdgeIACRef, Relation: "runs-as"})
	g.Normalize()

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range find(a, detailID("svc:api")).Graph.Nodes {
		if n.ID == "sa:api" && n.Attrs["inside"] == true {
			t.Fatal("the service account was drawn inside the workload that authenticates as it")
		}
	}
}
