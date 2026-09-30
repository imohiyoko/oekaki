package views

import (
	"encoding/json"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A view derives a document from a graph. It does not own that graph, and
// deriving must not change it.
//
// It has: five times now, and every one of them was found by somebody reading
// the code rather than by a test. Two of the five lost information — a
// conflict's claims were folded and reordered under the caller, and the
// sentence on a denial was rewritten in a drawing already handed to somebody
// else. The other three were latent, and latent is the whole difficulty:
// the pipeline normalizes its input before any view sees it, so the writes
// land on state that is already in the order they would put it in, and
// nothing looks wrong until a caller arrives that did not normalize first.
//
// So the graph here is deliberately not in that order. Everything a view
// shares with its input rather than copying — a node's coverage, the
// document's own metadata, a conflict's claims — is built unsorted, which is
// what a library caller hands over and what makes the writes visible.
func givenGraph(t *testing.T) *core.Graph {
	t.Helper()
	g := core.New()
	g.Axes = []core.Axis{{ID: "account", Label: "account"}}
	for _, id := range []string{"one", "two"} {
		g.Groups = append(g.Groups, core.Group{ID: id, Axis: "account", Type: "account", Label: id,
			Attrs: map[string]any{"note": id}})
	}
	node := func(id, group string) core.Node {
		return core.Node{ID: id, Type: "thing", Name: id, Provider: "aws",
			Groups:  map[string]string{"account": group},
			Attrs:   map[string]any{"where": group},
			Metrics: map[string]float64{"p95": 1},
			Claim:   &core.Claim{Origin: core.OriginHuman, Author: "operator", Note: "looked"}}
	}
	a1, a2 := node("a1", "one"), node("a2", "one")
	b1 := node("b1", "two")
	// Out of order on purpose: Normalize sorts this in place, through the
	// pointer every shallow copy of the node shares.
	a1.Coverage = &core.Coverage{State: core.CoverageFlowing, Evidence: []core.Evidence{
		{Kind: core.EvidenceObserved, Stream: "zeta"},
		{Kind: core.EvidenceObserved, Stream: "alpha"},
		{Kind: core.EvidenceDeclared, Stream: "mu"}}}
	g.Nodes = append(g.Nodes, a1, a2, b1)

	edge := func(from, to string, suppressed bool) core.Edge {
		e := core.Edge{From: from, To: to, Kind: core.EdgeIACRef, Relation: "remote_state",
			Attrs: map[string]any{"why": from}}
		if suppressed {
			e.Suppressed, e.AssertedAbsent = true, true
			e.Claim = &core.Claim{Origin: core.OriginHuman, Author: "auditor", Note: core.DeniedNote}
		}
		return e
	}
	g.Edges = append(g.Edges, edge("a1", "a2", false), edge("a1", "b1", true), edge("a2", "b1", false))

	// Likewise: shared by every view, and sorted in place.
	g.Metadata = &core.Metadata{Generator: "test",
		Overlays: []core.OverlayRef{
			{Source: "zeta", Origin: core.OriginHuman},
			{Source: "alpha", Origin: core.OriginHuman}}}

	g.Conflicts = []core.Conflict{{
		TargetKind: core.ConflictTargetEdge,
		Target:     core.EdgeKey("a1", "b1", core.EdgeIACRef, "remote_state"),
		Field:      "suppressed",
		Claims: []core.ClaimedValue{
			{Value: "false", Claim: core.Claim{Origin: core.OriginParser}},
			{Value: "true", Claim: core.Claim{Origin: core.OriginHuman, Author: "auditor", Note: core.DeniedNote}},
			{Value: "true", Claim: core.Claim{Origin: core.OriginHuman, Author: "auditor", Note: core.DeniedNote}},
		},
	}}
	return g
}

func TestDerivingADocumentDoesNotChangeTheGraphItCameFrom(t *testing.T) {
	for _, c := range []struct {
		name   string
		derive func(*core.Graph) error
	}{
		{"Apply", func(g *core.Graph) error { _, err := Apply(g, Options{}); return err }},
		{"Focus", func(g *core.Graph) error { _, err := Focus(g, "account", "one"); return err }},
		{"Collapse", func(g *core.Graph) error { _, err := Collapse(g, "account", 1); return err }},
		{"Fold", func(g *core.Graph) error { _, _, err := Fold(g, FoldOptions{Budget: 2}); return err }},
		{"BuildAtlas", func(g *core.Graph) error { _, err := BuildAtlas(g, AtlasOptions{}); return err }},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := givenGraph(t)
			before, err := json.Marshal(g)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.derive(g); err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(g)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Errorf("the graph changed under the caller:\n  before %s\n  after  %s", before, after)
			}
		})
	}
}

// A graph built in memory has nil where a graph read from a document has an
// empty array, and the copy goes through the document. Normalize has always
// filled those in; taking the copy off core.Encode took that with it, and
// every view that copies first stopped working on a hand-built graph.
func TestCopyingAGraphBuiltInMemoryWorks(t *testing.T) {
	g := &core.Graph{Version: core.Version}
	g.Nodes = append(g.Nodes, core.Node{ID: "a", Type: "thing"})
	// Axes, Edges, Groups and LogRecords left nil, as a caller who built this
	// by hand rather than by reading a file leaves them.

	if _, err := Apply(g, Options{}); err != nil {
		t.Errorf("Apply: %v", err)
	}
	if _, _, err := Fold(g, FoldOptions{}); err != nil {
		t.Errorf("Fold: %v", err)
	}
	if g.Edges != nil {
		t.Error("copying the graph filled in the caller's own nil arrays")
	}
}
