package dot

import (
	"math"
	"regexp"
	"strconv"
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
			{ID: "aws_subnet.empty", Axis: core.AxisNetwork, Type: "subnet", Label: "unused", Parent: ptr("aws_vpc.main")},
		},
		Nodes: []core.Node{
			{ID: "aws_instance.web", Type: "aws_instance", Name: "web", Groups: map[string]string{core.AxisNetwork: "aws_vpc.main/aws_subnet.a"},
				Attrs:  map[string]any{"instance_type": "t3.micro", "count": float64(3)},
				Source: &core.Source{File: "main.tf", Line: 12}},
			{ID: "aws_ecs_cluster.main", Type: "aws_ecs_cluster", Name: "main"},
		},
		Edges: []core.Edge{
			{From: "aws_instance.web", To: "aws_ecs_cluster.main", Kind: core.EdgeIACRef,
				Attrs: map[string]any{"attribute": "cluster"}},
			{From: "aws_ecs_cluster.main", To: "aws_instance.web", Kind: core.EdgeReachable},
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

func TestNestedClustersAreEmitted(t *testing.T) {
	out := render(t, Options{})

	if !strings.Contains(out, `subgraph "cluster_aws_vpc.main"`) {
		t.Error("the VPC cluster is missing")
	}
	if !strings.Contains(out, `subgraph "cluster_aws_subnet.a"`) {
		t.Error("the subnet cluster is missing")
	}

	// The subnet has to open after the VPC does, or it is not nested inside it.
	vpc := strings.Index(out, `subgraph "cluster_aws_vpc.main"`)
	subnet := strings.Index(out, `subgraph "cluster_aws_subnet.a"`)
	if subnet < vpc {
		t.Error("the subnet cluster is not nested inside the VPC cluster")
	}
}

// Graphviz drops a cluster with no contents, which would silently erase an
// empty subnet from the diagram.
func TestEmptyClustersSurvive(t *testing.T) {
	out := render(t, Options{})

	if !strings.Contains(out, `subgraph "cluster_aws_subnet.empty"`) {
		t.Fatal("the empty subnet cluster is missing")
	}
	if !strings.Contains(out, `"anchor_aws_subnet.empty"`) {
		t.Error("the empty cluster has no anchor, so Graphviz will discard it")
	}
}

func TestNodesLandInTheirCluster(t *testing.T) {
	out := render(t, Options{})

	subnetStart := strings.Index(out, `subgraph "cluster_aws_subnet.a"`)
	web := strings.Index(out, `"aws_instance.web" [label=`)
	if web < subnetStart {
		t.Error("the instance was emitted outside its subnet cluster")
	}

	// A node with no group belongs at the top level, after every cluster.
	clusterEnd := strings.LastIndex(out, "}\n  }")
	cluster := strings.Index(out, `"aws_ecs_cluster.main" [label=`)
	if cluster < clusterEnd {
		t.Error("an ungrouped node was emitted inside a cluster")
	}
}

func TestEdgeKindsAreVisuallyDistinct(t *testing.T) {
	out := render(t, Options{})

	if !strings.Contains(out, `"aws_instance.web" -> "aws_ecs_cluster.main" [color="#8a9099", tooltip="cluster"]`) {
		t.Error("the iac_ref edge is not styled as expected")
	}
	if !strings.Contains(out, `style=dashed`) {
		t.Error("the reachable edge should be dashed so it survives a black-and-white print")
	}
}

func TestKindFilter(t *testing.T) {
	out := render(t, Options{Kinds: []core.EdgeKind{core.EdgeIACRef}})

	if !strings.Contains(out, `"aws_instance.web" -> "aws_ecs_cluster.main"`) {
		t.Error("the requested kind was dropped")
	}
	if strings.Contains(out, `"aws_ecs_cluster.main" -> "aws_instance.web"`) {
		t.Error("an unrequested kind was drawn")
	}
}

func TestLegendListsOnlyThePresentKinds(t *testing.T) {
	out := render(t, Options{Legend: true, Kinds: []core.EdgeKind{core.EdgeIACRef}})

	if !strings.Contains(out, "cluster_legend") {
		t.Fatal("no legend was emitted")
	}
	// A v0.1 graph has no observed traffic, so the legend must not claim any.
	if strings.Contains(out, "observed traffic") {
		t.Error("the legend advertises an edge kind the graph does not contain")
	}
}

func TestNoLegendByDefault(t *testing.T) {
	if strings.Contains(render(t, Options{}), "cluster_legend") {
		t.Error("a legend appeared without being asked for")
	}
}

func TestTooltipCarriesAddressAndSource(t *testing.T) {
	out := render(t, Options{})

	if !strings.Contains(out, `main.tf:12`) {
		t.Error("the source location is not in the tooltip")
	}
	if !strings.Contains(out, `instance_type = t3.micro`) {
		t.Error("scalar attributes are not in the tooltip")
	}
}

func TestTitleIsOptional(t *testing.T) {
	if strings.Contains(render(t, Options{}), "labelloc") {
		t.Error("a title appeared without being asked for")
	}
	if !strings.Contains(render(t, Options{Title: "my stack"}), `label="my stack"`) {
		t.Error("the title was not drawn")
	}
}

func TestRankDirDefaultsToLR(t *testing.T) {
	if !strings.Contains(render(t, Options{}), `rankdir="LR"`) {
		t.Error("rankdir did not default to LR")
	}
	if !strings.Contains(render(t, Options{RankDir: "TB"}), `rankdir="TB"`) {
		t.Error("rankdir was not honoured")
	}
}

// Orthogonal splines mangle edges that cross cluster boundaries, which in this
// graph is most of them.
func TestOrthogonalSplinesAreNotUsed(t *testing.T) {
	if strings.Contains(render(t, Options{}), "splines=ortho") {
		t.Error("splines=ortho produces broken edges across clusters")
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	first := render(t, Options{Legend: true})
	for range 10 {
		if got := render(t, Options{Legend: true}); got != first {
			t.Fatal("two renders of the same graph differ")
		}
	}
}

func TestQuoteEscapes(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`plain`, `"plain"`},
		{`say "hi"`, `"say \"hi\""`},
		{`back\slash`, `"back\\slash"`},
		// Graphviz reads \n inside a quoted string as a line break, which is
		// exactly what a two-line node label needs.
		{"two\nlines", `"two\nlines"`},
	}

	for _, tt := range tests {
		if got := quote(tt.in); got != tt.want {
			t.Errorf("quote(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestLabelsDropTheProviderPrefix(t *testing.T) {
	out := render(t, Options{})

	if !strings.Contains(out, `web\ninstance`) {
		t.Error(`expected the label to read "web" over "instance", without the aws_ prefix`)
	}
}

// A font list is not a font. Graphviz cannot look one up in its metric tables,
// so it sizes boxes from a fallback that does not match what any browser will
// draw with — and the list's trailing generic invites a wider substitute on
// top of that. This is the regression that shipped once already; the geometry
// is checked in renderers/graphviz, and this names the cause.
func TestFontNameIsASingleFamily(t *testing.T) {
	out := render(t, Options{})

	if strings.Contains(out, `fontname="Helvetica,`) {
		t.Error("fontname is a CSS font stack again; Graphviz cannot measure a list")
	}
	if !strings.Contains(out, `fontname="Helvetica"`) {
		t.Error("fontname is not the single family Graphviz has metrics for")
	}
	if !strings.Contains(out, `margin="0.28,0.10"`) {
		t.Error("node margin lost the horizontal slack that absorbs font substitution")
	}
}

// Correct metrics were not enough on their own: a substituted face is a
// percentage wider, and a fixed margin cannot absorb a percentage. Without a
// stated minimum, long labels spill however well the font is measured.
func TestLongLabelsGetAnExplicitMinimumWidth(t *testing.T) {
	// Rendered rather than computed. Asserting on minWidthInches alone would
	// pass even if writeNode emitted one constant width for every node, which
	// is the bug most likely to be introduced here and the one that would
	// bring the spilling straight back.
	g := &core.Graph{
		Version: core.Version,
		Axes:    []core.Axis{{ID: core.AxisNetwork}},
		Nodes: []core.Node{
			{ID: "aws_instance.web", Type: "aws_instance", Name: "web"},
			{ID: "aws_s3_bucket_server_side_encryption_configuration.main",
				Type: "aws_s3_bucket_server_side_encryption_configuration", Name: "main"},
		},
	}
	g.Normalize()

	out, err := Render(g, Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	short := emittedWidth(t, out, "aws_instance.web")
	long := emittedWidth(t, out, "aws_s3_bucket_server_side_encryption_configuration.main")

	if long <= short {
		t.Errorf("the long label asks for %.2fin and the short one %.2fin: the width does not follow the label",
			long, short)
	}
	// Looked up by id rather than by index: Normalize sorts the nodes, so an
	// index here would silently pair a width with the wrong label — which is
	// how this test was wrong the first time it was written.
	for _, c := range []struct {
		id  string
		got float64
	}{
		{"aws_instance.web", short},
		{"aws_s3_bucket_server_side_encryption_configuration.main", long},
	} {
		n, ok := g.Node(c.id)
		if !ok {
			t.Fatalf("node %q is not in the graph", c.id)
		}
		label := nodeLabel(n)
		want := minWidthInches(label)
		// The attribute is written to two decimals, so compare at that precision.
		if math.Abs(c.got-want) > 0.005 {
			t.Errorf("%s was emitted with width=%.2f, want %.2f for label %q", c.id, c.got, want, label)
		}
	}
}

// emittedWidth reads the width a node was actually written with.
func emittedWidth(t *testing.T, dot, id string) float64 {
	t.Helper()

	for _, line := range strings.Split(dot, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, `"`+id+`" [`) {
			continue
		}
		m := regexp.MustCompile(`width=([0-9.]+)`).FindStringSubmatch(trimmed)
		if m == nil {
			t.Fatalf("node %q was emitted without a width", id)
		}
		w, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			t.Fatalf("node %q has an unparseable width %q", id, m[1])
		}
		return w
	}
	t.Fatalf("node %q is not in the output", id)
	return 0
}
