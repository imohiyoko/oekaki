package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func testGraph() *core.Graph {
	g := core.New()
	g.Nodes = []core.Node{
		{ID: "service:a", Type: "service", Name: "a"},
		{ID: "db:orders", Type: "database", Name: "orders"},
		{ID: "service:b", Type: "service", Name: "b"},
	}
	g.Edges = []core.Edge{
		{From: "service:a", To: "db:orders", Kind: core.EdgeIACRef, Relation: "writes"},
		{From: "service:a", To: "service:b", Kind: core.EdgeObserved, Relation: "calls"},
		{From: "service:a", To: "service:b", Kind: core.EdgeIACRef, Relation: "calls"},
	}
	g.Normalize()
	return g
}

func TestERViewKeepsDatabaseRelationship(t *testing.T) {
	g, err := Apply(testGraph(), Options{Name: ER})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Fatalf("got %d nodes, %d edges", len(g.Nodes), len(g.Edges))
	}
}

func TestRequestPathRequiresRootAndTraverses(t *testing.T) {
	if _, err := Apply(testGraph(), Options{Name: RequestPath}); err == nil {
		t.Fatal("expected missing root error")
	}
	g, err := Apply(testGraph(), Options{Name: RequestPath, Root: "service:a", Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 3 {
		t.Fatalf("got %d nodes", len(g.Nodes))
	}
}

func TestTraversalViewsRejectFileFocus(t *testing.T) {
	for _, name := range []string{RequestPath, Reachability} {
		if _, err := Apply(testGraph(), Options{Name: name, Root: "service:a", Depth: 1, File: "main.go"}); err == nil {
			t.Errorf("view %q accepted --file with --root/--depth", name)
		}
	}
}

func TestFileViewExpandsRelatedEntities(t *testing.T) {
	g := testGraph()
	files := map[string]string{
		"service:a": "services/a/main.go", "db:orders": "services/db/schema.sql", "service:b": "services/b/main.go",
	}
	for i := range g.Nodes {
		g.Nodes[i].Source = &core.Source{File: files[g.Nodes[i].ID]}
	}
	out, err := Apply(g, Options{Name: CodeDependency, File: "services/a/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 3 {
		t.Fatalf("got %d nodes", len(out.Nodes))
	}
}

func TestFileFocusRestrictsDefaultAndNamedViews(t *testing.T) {
	g := testGraph()
	g.Nodes[0].Source = &core.Source{File: "services/a/main.go"}
	g.Nodes[1].Source = &core.Source{File: "services/db/schema.sql"}
	g.Nodes[2].Source = &core.Source{File: "services/b/main.go"}
	g.Nodes = append(g.Nodes, core.Node{ID: "service:unrelated", Type: "service", Name: "unrelated", Source: &core.Source{File: "services/c/main.go"}})
	g.Normalize()

	for _, view := range []string{"", Architecture, Network, CodeDependency} {
		out, err := Apply(g, Options{Name: view, File: "services/a/main.go"})
		if err != nil {
			t.Fatalf("view %q: %v", view, err)
		}
		if _, ok := out.Node("service:unrelated"); ok {
			t.Errorf("view %q retained an unrelated file", view)
		}
		if _, ok := out.Node("service:a"); !ok {
			t.Errorf("view %q omitted the focused entity", view)
		}
	}
}

func TestFileFocusRejectsUnknownFileInDefaultView(t *testing.T) {
	if _, err := Apply(testGraph(), Options{File: "missing.go"}); err == nil {
		t.Fatal("default view accepted a file that names no entity")
	}
}

func TestFileFocusExpandsExactlyOneEdgeHop(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{
		{ID: "a", Type: "service", Name: "a", Source: &core.Source{File: "selected.go"}},
		{ID: "b", Type: "service", Name: "b", Source: &core.Source{File: "b.go"}},
		{ID: "c", Type: "service", Name: "c", Source: &core.Source{File: "c.go"}},
	}
	g.Edges = []core.Edge{
		{From: "a", To: "b", Kind: core.EdgeObserved, Relation: "calls"},
		{From: "b", To: "c", Kind: core.EdgeObserved, Relation: "calls"},
	}

	for _, reverse := range []bool{false, true} {
		if reverse {
			g.Edges[0], g.Edges[1] = g.Edges[1], g.Edges[0]
		}
		out, err := Apply(g, Options{Name: CodeDependency, File: "selected.go"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := out.Node("b"); !ok {
			t.Fatal("adjacent entity was omitted")
		}
		if _, ok := out.Node("c"); ok {
			t.Fatalf("edge order %v expanded beyond one hop", reverse)
		}
	}
}

func TestFileFocusKeepsSelectedGroupAncestors(t *testing.T) {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork}}
	parent := "vpc"
	g.Groups = []core.Group{
		{ID: parent, Axis: core.AxisNetwork, Type: "vpc", Label: "vpc", Source: &core.Source{File: "parent.tf"}},
		{ID: "subnet", Axis: core.AxisNetwork, Type: "subnet", Label: "subnet", Parent: &parent, Source: &core.Source{File: "selected.tf"}},
	}

	out, err := Apply(g, Options{File: "selected.tf"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Groups) != 2 {
		t.Fatalf("focused hierarchy = %#v, want child and parent", out.Groups)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("focused hierarchy is invalid: %v", err)
	}
}

func TestProjectionDropsConflictsForOmittedTargets(t *testing.T) {
	g := testGraph()
	g.Conflicts = []core.Conflict{
		{TargetKind: core.ConflictTargetEntity, Target: "service:b", Field: "name", Claims: []core.ClaimedValue{{Value: "b", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "B", Claim: core.Claim{Origin: core.OriginHuman}}}},
		{TargetKind: core.ConflictTargetEdge, Target: core.EdgeKey("service:a", "service:b", core.EdgeObserved, "calls"), Field: "suppressed", Claims: []core.ClaimedValue{{Value: "false", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "true", Claim: core.Claim{Origin: core.OriginHuman}}}},
		{TargetKind: core.ConflictTargetEntity, Target: "db:orders", Field: "name", Claims: []core.ClaimedValue{{Value: "orders", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "Orders", Claim: core.Claim{Origin: core.OriginHuman}}}},
	}

	out, err := Apply(g, Options{Name: ER})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Conflicts) != 1 || out.Conflicts[0].Target != "db:orders" {
		t.Fatalf("projected conflicts = %#v", out.Conflicts)
	}
}

func TestReachabilityViewExcludesInfrastructureOnlyEdges(t *testing.T) {
	in := testGraph()
	in.Conflicts = []core.Conflict{{
		TargetKind: core.ConflictTargetEdge,
		Target:     core.EdgeKey("service:a", "service:b", core.EdgeObserved, "calls"),
		Field:      "suppressed",
		Claims:     []core.ClaimedValue{{Value: "false", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "true", Claim: core.Claim{Origin: core.OriginHuman}}},
	}}
	g, err := Apply(in, Options{Name: Reachability, Root: "service:a", Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Edges) != 1 || g.Edges[0].Kind != core.EdgeObserved {
		t.Fatalf("reachability view kept non-reachability edges: %#v", g.Edges)
	}
	if len(g.Conflicts) != 1 || g.Conflicts[0].TargetKind != core.ConflictTargetEdge {
		t.Fatalf("visible edge conflict was dropped: %#v", g.Conflicts)
	}
}

func TestProjectionKeepsEvidenceOnlyForVisibleNodes(t *testing.T) {
	g := testGraph()
	g.Observations = []core.Observation{
		{Subject: "db:orders", Metric: "visible"},
		{Subject: "service:b", Metric: "hidden"},
	}
	g.LogRecords = []core.LogRecordSummary{
		{ID: "log-visible", Source: "db:orders"},
		{ID: "log-hidden", Source: "service:b"},
	}
	out, err := Apply(g, Options{Name: ER})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Observations) != 1 || out.Observations[0].Subject != "db:orders" {
		t.Fatalf("unexpected projected observations: %+v", out.Observations)
	}
	if len(out.LogRecords) != 1 || out.LogRecords[0].Source != "db:orders" {
		t.Fatalf("unexpected projected log records: %+v", out.LogRecords)
	}
}
