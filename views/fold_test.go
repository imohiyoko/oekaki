package views

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A service in front of a row of identical workers, each with a config map of
// its own, and a queue chain behind it.
func crowded() *core.Graph {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork}}
	g.Groups = []core.Group{{ID: "ns:shop", Axis: core.AxisNetwork, Type: "namespace", Label: "shop"}}
	g.Nodes = []core.Node{
		{ID: "svc:api", Type: "service", Name: "api", Groups: map[string]string{core.AxisNetwork: "ns:shop"}},
	}
	for _, name := range []string{"worker-a", "worker-b", "worker-c", "worker-d"} {
		g.Nodes = append(g.Nodes, core.Node{
			ID: "pod:" + name, Type: "pod", Name: name,
			Groups: map[string]string{core.AxisNetwork: "ns:shop"},
		})
		g.Edges = append(g.Edges, core.Edge{
			From: "svc:api", To: "pod:" + name, Kind: core.EdgeIACRef, Relation: "selects",
		})
	}
	g.Normalize()
	return g
}

func folded(t *testing.T, g *core.Graph, opts FoldOptions) (*core.Graph, map[string]Folded) {
	t.Helper()
	if opts.Budget == 0 {
		opts.Budget = 1 // fold whatever the rules can, so a small fixture folds at all
	}
	out, folds, err := Fold(g, opts)
	if err != nil {
		t.Fatal(err)
	}
	byStand := map[string]Folded{}
	for _, f := range folds {
		byStand[f.Stands] = f
	}
	return out, byStand
}

func ids(g *core.Graph) map[string]bool {
	out := map[string]bool{}
	for _, n := range g.Nodes {
		out[n.ID] = true
	}
	return out
}

// Four boxes that are the same thing, in the same place, joined to the same
// things become one box that says there were four.
func TestTwinsBecomeOneBoxAndACount(t *testing.T) {
	out, folds := folded(t, crowded(), FoldOptions{Rules: []string{FoldTwins}})

	if len(out.Nodes) != 2 {
		t.Fatalf("got %d boxes, want the service and one standing for the workers: %#v", len(out.Nodes), ids(out))
	}
	var stand core.Node
	for _, n := range out.Nodes {
		if IsFold(n.ID) {
			stand = n
		}
	}
	if stand.ID == "" {
		t.Fatal("nothing was folded")
	}
	if stand.Attrs["members"] != 4 {
		t.Fatalf("the box does not say how many it stands for: %#v", stand.Attrs)
	}
	if !strings.Contains(stand.Name, "×4") {
		t.Fatalf("the label does not carry the count: %q", stand.Name)
	}
	if stand.Type != "pod" || stand.Groups[core.AxisNetwork] != "ns:shop" {
		t.Fatalf("the box standing in is not the same kind of thing, in the same place: %#v", stand)
	}
	// And the four lines to them become one line that says it stands for four.
	if len(out.Edges) != 1 {
		t.Fatalf("got %d lines, want one: %#v", len(out.Edges), out.Edges)
	}
	if out.Edges[0].Attrs["references"] != 4 {
		t.Fatalf("the line does not say how many it stands for: %#v", out.Edges[0].Attrs)
	}
	// The record says which ones, so a viewer can put them back.
	f := folds[stand.ID]
	if len(f.Members) != 4 || f.Members[0] != "pod:worker-a" {
		t.Fatalf("the record does not name what was folded: %#v", f)
	}
}

// The one with something else attached is not interchangeable with the rest,
// and folding it would hide the only interesting thing about it.
func TestABoxWithSomethingElseAttachedIsNotATwin(t *testing.T) {
	g := crowded()
	g.Nodes = append(g.Nodes, core.Node{ID: "db:orders", Type: "database", Name: "orders"})
	g.Edges = append(g.Edges, core.Edge{From: "pod:worker-c", To: "db:orders", Kind: core.EdgeObserved, Relation: "writes"})
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldTwins}})
	if !ids(out)["pod:worker-c"] {
		t.Fatalf("the worker that talks to the database was folded away: %#v", ids(out))
	}
}

// A drawing whose subject is which boxes have logs must not fold a box that
// has none into one that does.
func TestCoverageAndClaimAreCheckedBeforeFolding(t *testing.T) {
	g := crowded()
	for i := range g.Nodes {
		switch g.Nodes[i].ID {
		case "pod:worker-a":
			g.Nodes[i].Coverage = &core.Coverage{
				State: core.CoverageBlind, Evidence: []core.Evidence{{Kind: core.EvidenceNone}},
			}
		case "pod:worker-b":
			g.Nodes[i].Claim = &core.Claim{Origin: core.OriginHuman, Author: "operator"}
		}
	}
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldTwins}})
	present := ids(out)
	if !present["pod:worker-a"] {
		t.Fatal("a box with no logs was folded in with boxes that have them")
	}
	if !present["pod:worker-b"] {
		t.Fatal("a box somebody asserted was folded in with boxes a parser found")
	}
}

// Everything hanging off one box by a single line becomes a number on that
// box's side, and the shape of the drawing is untouched.
func TestLeavesFoldPerHostAndPerType(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "svc:api", Type: "service", Name: "api"}}
	for _, name := range []string{"one", "two", "three"} {
		g.Nodes = append(g.Nodes, core.Node{ID: "cm:" + name, Type: "configmap", Name: name})
		g.Edges = append(g.Edges, core.Edge{From: "svc:api", To: "cm:" + name, Kind: core.EdgeIACRef, Relation: "reads"})
	}
	g.Nodes = append(g.Nodes, core.Node{ID: "secret:tls", Type: "secret", Name: "tls"})
	g.Edges = append(g.Edges, core.Edge{From: "svc:api", To: "secret:tls", Kind: core.EdgeIACRef, Relation: "reads"})
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldLeaves}})
	present := ids(out)
	if present["cm:one"] || present["cm:two"] || present["cm:three"] {
		t.Fatalf("the config maps were not folded: %#v", present)
	}
	// One of a kind is not a crowd: folding it would replace a box with a box.
	if !present["secret:tls"] {
		t.Fatalf("a lone attachment was folded into a box of its own: %#v", present)
	}
}

// A run that passes something along keeps its ends and says how far it is.
func TestAChainKeepsItsEnds(t *testing.T) {
	g := core.New()
	chain := []string{"in:gateway", "q:one", "w:worker", "q:two", "out:bucket"}
	for _, id := range chain {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	for i := 1; i < len(chain); i++ {
		g.Edges = append(g.Edges, core.Edge{From: chain[i-1], To: chain[i], Kind: core.EdgeIACRef, Relation: "calls"})
	}
	g.Normalize()

	out, folds := folded(t, g, FoldOptions{Rules: []string{FoldChain}})
	present := ids(out)
	if !present["in:gateway"] || !present["out:bucket"] {
		t.Fatalf("an end of the chain was folded away: %#v", present)
	}
	if present["q:one"] || present["w:worker"] || present["q:two"] {
		t.Fatalf("the middle was not folded: %#v", present)
	}
	for _, f := range folds {
		if f.Label != "3 hops" {
			t.Fatalf("the box does not say how far it is: %q", f.Label)
		}
	}
}

// Folding runs until the drawing is inside the budget and then stops, so
// nothing is folded that did not need to be.
func TestFoldingStopsWhenTheDrawingFits(t *testing.T) {
	out, folds, err := Fold(crowded(), FoldOptions{Budget: 80})
	if err != nil {
		t.Fatal(err)
	}
	if len(folds) != 0 {
		t.Fatalf("a drawing already inside its budget was folded: %#v", folds)
	}
	if len(out.Nodes) != 5 {
		t.Fatalf("got %d boxes, want the five that were there", len(out.Nodes))
	}
}

// Whatever the reader is looking at is not something to fold away underneath
// them.
func TestKeepIsNotFolded(t *testing.T) {
	out, _ := folded(t, crowded(), FoldOptions{Rules: []string{FoldTwins}, Keep: []string{"pod:worker-b"}})
	if !ids(out)["pod:worker-b"] {
		t.Fatal("the box the reader asked to keep was folded away")
	}
}

// A fold is not a deletion, and the result has to be a document in its own
// right: same input, same drawing, and it validates.
func TestFoldingIsDeterministicAndProducesAValidGraph(t *testing.T) {
	first, _, err := Fold(crowded(), FoldOptions{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("the folded graph is not valid: %v", err)
	}
	for range 5 {
		again, _, err := Fold(crowded(), FoldOptions{Budget: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(again.Nodes) != len(first.Nodes) {
			t.Fatalf("%d boxes then %d", len(first.Nodes), len(again.Nodes))
		}
		for i := range first.Nodes {
			if again.Nodes[i].ID != first.Nodes[i].ID {
				t.Fatalf("box %d is %q then %q", i, first.Nodes[i].ID, again.Nodes[i].ID)
			}
		}
	}
}

// The input is somebody else's document. Folding a copy is the difference
// between a projection and an edit.
func TestFoldingDoesNotTouchTheInput(t *testing.T) {
	g := crowded()
	before := len(g.Nodes)
	if _, _, err := Fold(g, FoldOptions{Budget: 1}); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != before {
		t.Fatalf("the input went from %d boxes to %d", before, len(g.Nodes))
	}
}

// Evidence about a box that is no longer drawn has nothing to attach to, and a
// route through folded boxes is a route through what stands for them.
func TestFoldingCarriesRoutesAndDropsOrphanedReadings(t *testing.T) {
	g := crowded()
	g.Paths = []core.Path{{Nodes: []string{"svc:api", "pod:worker-a"}, Kind: core.EdgeObserved}}
	value := 9.0
	g.Observations = []core.Observation{
		{Subject: "pod:worker-a", Metric: "cpu", Value: &value},
		{Subject: core.PathKey([]string{"svc:api", "pod:worker-a"}), Metric: "path_requests", Value: &value},
	}
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldTwins}})
	if err := out.Validate(); err != nil {
		t.Fatalf("the folded graph is not valid: %v", err)
	}
	if len(out.Paths) != 1 {
		t.Fatalf("the route did not survive folding: %#v", out.Paths)
	}
	if out.Paths[0].Nodes[1] == "pod:worker-a" {
		t.Fatal("the route still walks a box that is no longer drawn")
	}
	for _, o := range out.Observations {
		if o.Subject == "pod:worker-a" {
			t.Fatal("a reading about a folded box was kept, naming nothing")
		}
	}
}

// Four pods that talk to each other say something about what is inside the
// fold. A box joined to itself says nothing and draws badly, so it becomes a
// number.
func TestReferencesInsideAFoldBecomeANumber(t *testing.T) {
	g := crowded()
	g.Edges = append(g.Edges,
		core.Edge{From: "pod:worker-a", To: "pod:worker-b", Kind: core.EdgeObserved, Relation: "calls"},
		core.Edge{From: "pod:worker-b", To: "pod:worker-a", Kind: core.EdgeObserved, Relation: "calls"},
	)
	g.Normalize()

	out, folds := folded(t, g, FoldOptions{Rules: []string{FoldTwins}})
	for _, e := range out.Edges {
		if e.From == e.To {
			t.Fatalf("a box was joined to itself: %#v", e)
		}
	}

	// The two that call each other fold together — a mesh is the commonest
	// crowd there is — and the box says what happens inside it. The two that
	// do not are a separate box, and it says nothing about traffic it does not
	// have.
	mesh := ""
	for stands, f := range folds {
		for _, id := range f.Members {
			if id == "pod:worker-a" {
				mesh = stands
			}
		}
	}
	if mesh == "" {
		t.Fatalf("the pair that call each other were not folded: %#v", folds)
	}
	for _, n := range out.Nodes {
		if n.ID != mesh {
			continue
		}
		if n.Attrs["internal_references"] != 2 {
			t.Fatalf("what happens inside the fold was lost: %#v", n.Attrs)
		}
	}
}

// Peers that call each other have four different neighbour sets — each
// other”'s. Comparing those means a set of peers can never be folded, which is
// the commonest crowd there is.
func TestAMeshOfPeersFolds(t *testing.T) {
	g := core.New()
	members := []string{"a", "b", "c", "d"}
	for _, id := range members {
		g.Nodes = append(g.Nodes, core.Node{ID: "node:" + id, Type: "broker", Name: "broker-" + id})
	}
	for _, from := range members {
		for _, to := range members {
			if from == to {
				continue
			}
			g.Edges = append(g.Edges, core.Edge{
				From: "node:" + from, To: "node:" + to, Kind: core.EdgeObserved, Relation: "replicates",
			})
		}
	}
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldTwins}})
	if len(out.Nodes) != 1 {
		t.Fatalf("got %d boxes, want one standing for the mesh: %#v", len(out.Nodes), ids(out))
	}
	if out.Nodes[0].Attrs["members"] != 4 || out.Nodes[0].Attrs["internal_references"] != 12 {
		t.Fatalf("the box does not say what it stands for: %#v", out.Nodes[0].Attrs)
	}
}
