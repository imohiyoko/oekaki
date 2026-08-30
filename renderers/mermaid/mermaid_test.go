package mermaid

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func ptr(s string) *string { return &s }

func fixture() *core.Graph {
	g := &core.Graph{
		Version: core.Version,
		Axes:    []core.Axis{{ID: core.AxisNetwork}},
		Groups: []core.Group{
			{ID: "aws_vpc.main", Axis: core.AxisNetwork, Type: "vpc", Label: "main", Parent: nil},
			{ID: "aws_subnet.a", Axis: core.AxisNetwork, Type: "subnet", Label: "public-a", Parent: ptr("aws_vpc.main")},
		},
		Nodes: []core.Node{
			{ID: "aws_instance.web", Type: "aws_instance", Name: "web", Groups: map[string]string{core.AxisNetwork: "aws_vpc.main/aws_subnet.a"}},
			{ID: "aws_db_instance.main", Type: "aws_db_instance", Name: "main", Groups: map[string]string{core.AxisNetwork: "aws_vpc.main"}},
			{ID: "aws_ecs_cluster.main", Type: "aws_ecs_cluster", Name: "main"},
		},
		Edges: []core.Edge{
			{From: "aws_instance.web", To: "aws_db_instance.main", Kind: core.EdgeIACRef},
			{From: "aws_instance.web", To: "aws_ecs_cluster.main", Kind: core.EdgeReachable},
			{From: "aws_db_instance.main", To: "aws_ecs_cluster.main", Kind: core.EdgeObserved},
		},
	}
	g.Normalize()
	return g
}

func render(t *testing.T, opts Options) string {
	t.Helper()

	out, err := Render(fixture(), opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

func TestFlowchartHeader(t *testing.T) {
	if !strings.HasPrefix(render(t, Options{}), "flowchart LR\n") {
		t.Error("output does not start with a flowchart declaration")
	}
	if !strings.Contains(render(t, Options{Direction: "TB"}), "flowchart TB") {
		t.Error("direction was not honoured")
	}
}

func TestSubgraphsNest(t *testing.T) {
	out := render(t, Options{})

	vpc := strings.Index(out, `["vpc: main"]`)
	subnet := strings.Index(out, `["subnet: public-a"]`)
	if vpc < 0 || subnet < 0 {
		t.Fatalf("subgraphs missing:\n%s", out)
	}
	if subnet < vpc {
		t.Error("the subnet subgraph is not nested inside the VPC subgraph")
	}
	if strings.Count(out, "\n  end") < 1 {
		t.Error("subgraphs are not closed")
	}
}

// Terraform addresses contain dots and brackets, which Mermaid's parser will
// not accept as identifiers.
func TestIdentifiersAreSanitised(t *testing.T) {
	out := render(t, Options{})

	for line := range strings.SplitSeq(out, "\n") {
		for _, arrow := range []string{"-.->", "==>", "-->"} {
			from, to, isEdge := strings.Cut(strings.TrimSpace(line), arrow)
			if !isEdge {
				continue
			}
			for _, endpoint := range []string{strings.TrimSpace(from), strings.TrimSpace(to)} {
				if strings.ContainsAny(endpoint, ".[]") {
					t.Errorf("edge endpoint %q is not a safe Mermaid identifier", endpoint)
				}
			}
			break
		}
	}
	if !strings.Contains(out, `n0[`) {
		t.Error("nodes were not given synthetic ids")
	}
}

func TestEachEdgeKindGetsItsOwnArrow(t *testing.T) {
	out := render(t, Options{})

	for _, arrow := range []string{"-->", "-.->", "==>"} {
		if !strings.Contains(out, arrow) {
			t.Errorf("no edge drawn with %q", arrow)
		}
	}
}

func TestLinkStylesCoverEveryEdge(t *testing.T) {
	out := render(t, Options{})

	if !strings.Contains(out, "linkStyle") {
		t.Fatal("no link styling emitted")
	}
	// Three edges of three kinds means three separate linkStyle lines.
	if got := strings.Count(out, "linkStyle "); got != 3 {
		t.Errorf("got %d linkStyle lines, want 3", got)
	}
}

func TestClassesColourNodesByCategory(t *testing.T) {
	out := render(t, Options{})

	for _, want := range []string{"classDef compute", "classDef database", "class "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFencedOutputIsPasteable(t *testing.T) {
	out := render(t, Options{Fenced: true})

	if !strings.HasPrefix(out, "```mermaid\n") || !strings.HasSuffix(out, "```\n") {
		t.Error("fenced output is not wrapped in a Markdown code fence")
	}
}

func TestUnfencedByDefault(t *testing.T) {
	if strings.Contains(render(t, Options{}), "```") {
		t.Error("a code fence appeared without being asked for")
	}
}

func TestKindFilter(t *testing.T) {
	out := render(t, Options{Kinds: []core.EdgeKind{core.EdgeIACRef}})

	if strings.Contains(out, "-.->") || strings.Contains(out, "==>") {
		t.Error("an unrequested edge kind was drawn")
	}
	if !strings.Contains(out, "-->") {
		t.Error("the requested edge kind was dropped")
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	first := render(t, Options{})
	for range 10 {
		if got := render(t, Options{}); got != first {
			t.Fatal("two renders of the same graph differ")
		}
	}
}

func TestEscapeProtectsLabels(t *testing.T) {
	if got := escape(`a "quoted" name`); strings.Contains(got, `"`) {
		t.Errorf("escape left a bare quote in %q, which would end the label early", got)
	}
	if got := escape("two\nlines"); !strings.Contains(got, "<br/>") {
		t.Errorf("escape did not turn a newline into a line break: %q", got)
	}
}

func TestDistinctGroupIDsCannotCollideAfterRendering(t *testing.T) {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork}}
	g.Groups = []core.Group{
		{ID: "team-a", Axis: core.AxisNetwork, Type: "group", Label: "hyphen"},
		{ID: "team_a", Axis: core.AxisNetwork, Type: "group", Label: "underscore"},
	}
	g.Nodes = []core.Node{
		{ID: "a", Type: "service", Name: "a", Groups: map[string]string{core.AxisNetwork: "team-a"}},
		{ID: "b", Type: "service", Name: "b", Groups: map[string]string{core.AxisNetwork: "team_a"}},
	}
	g.Edges = []core.Edge{{From: "team-a", To: "team_a", Kind: core.EdgeIACRef}}
	g.Normalize()
	out, err := Render(g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "subgraph g0") != 1 || strings.Count(out, "subgraph g1") != 1 {
		t.Fatalf("group ids were not unique:\n%s", out)
	}
	if !strings.Contains(out, "g0 --> g1") {
		t.Fatalf("group edge did not use the synthetic ids:\n%s", out)
	}
}
