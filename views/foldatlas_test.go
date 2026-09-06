package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// Three namespaces, each holding a service and a row of identical replicas:
// an estate that navigates well and still has a level nobody can read.
func crowdedCluster() *core.Graph {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork}}
	for _, team := range []string{"shop", "pay"} {
		ns := "ns:" + team
		g.Groups = append(g.Groups, core.Group{ID: ns, Axis: core.AxisNetwork, Type: "namespace", Label: team})
		svc := "svc:" + team
		g.Nodes = append(g.Nodes, core.Node{
			ID: svc, Type: "service", Name: team, Groups: map[string]string{core.AxisNetwork: ns},
		})
		for i := range 12 {
			pod := "pod:" + team + string(rune('a'+i))
			g.Nodes = append(g.Nodes, core.Node{
				ID: pod, Type: "pod", Name: team + "-worker",
				Groups: map[string]string{core.AxisNetwork: ns},
			})
			g.Edges = append(g.Edges, core.Edge{From: svc, To: pod, Kind: core.EdgeIACRef, Relation: "selects"})
		}
	}
	g.Edges = append(g.Edges, core.Edge{From: "svc:shop", To: "svc:pay", Kind: core.EdgeObserved, Relation: "calls"})
	g.Normalize()
	return g
}

// Each page is folded on its own terms, and says which page it belongs to. The
// same box is on several pages of an atlas — a workload is on its level and on
// its own detail page — and a crowd on one of them is not a crowd on another.
func TestFoldingAnAtlasIsPerPage(t *testing.T) {
	a, err := BuildAtlas(crowdedCluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	folds, err := FoldAtlas(a, FoldOptions{Budget: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(folds) == 0 {
		t.Fatal("a level of twelve identical replicas was not folded")
	}

	pages := map[string]*Diagram{}
	for i := range a.Diagrams {
		pages[a.Diagrams[i].ID] = &a.Diagrams[i]
	}
	for _, f := range folds {
		if f.Diagram == "" {
			t.Fatalf("a fold does not say which page it is on: %#v", f)
		}
		page, ok := pages[f.Diagram]
		if !ok {
			t.Fatalf("a fold names a page the atlas does not have: %q", f.Diagram)
		}
		// A fold that named boxes its page does not draw is the whole reason
		// this is done per page.
		for _, id := range f.Members {
			if _, drawn := page.Graph.Node(id); !drawn {
				t.Fatalf("the fold on %q stands for %q, which that page does not draw", f.Diagram, id)
			}
		}
	}
}

// The pages keep their whole graphs. The viewer is what folds them, and it
// needs the members to put back.
func TestFoldingAnAtlasLeavesThePagesWhole(t *testing.T) {
	a, err := BuildAtlas(crowdedCluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]int{}
	for _, d := range a.Diagrams {
		before[d.ID] = len(d.Graph.Nodes)
	}

	if _, err := FoldAtlas(a, FoldOptions{Budget: 5}); err != nil {
		t.Fatal(err)
	}
	for _, d := range a.Diagrams {
		if len(d.Graph.Nodes) != before[d.ID] {
			t.Fatalf("%s went from %d boxes to %d: the page cannot put back what it no longer has",
				d.ID, before[d.ID], len(d.Graph.Nodes))
		}
	}
}

// A page inside its budget is left alone, page by page, so a crowded level does
// not cost the quiet ones their detail.
func TestAPageInsideItsBudgetIsNotFolded(t *testing.T) {
	a, err := BuildAtlas(crowdedCluster(), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	folds, err := FoldAtlas(a, FoldOptions{Budget: 5})
	if err != nil {
		t.Fatal(err)
	}
	touched := map[string]bool{}
	for _, f := range folds {
		touched[f.Diagram] = true
	}
	if touched[RootDiagram] {
		t.Fatal("the root level draws two namespaces and was folded anyway")
	}
	if len(touched) == len(a.Diagrams) {
		t.Fatal("every page was folded, including the ones that fit")
	}
}
