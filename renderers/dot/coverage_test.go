package dot

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func conf(v float64) *float64 { return &v }

func coverageGraph(states ...core.CoverageState) *core.Graph {
	g := &core.Graph{
		Version: core.Version,
		Axes:    []core.Axis{{ID: core.AxisNetwork}},
		Nodes: []core.Node{
			{ID: "logsink:app", Type: "oekaki_log_sink", Name: "app"},
		},
	}
	for i, st := range states {
		n := core.Node{
			ID:       string(st) + "_node",
			Type:     "aws_ecs_service",
			Name:     string(st),
			Coverage: &core.Coverage{State: st, Reason: "because"},
		}
		switch st {
		case core.CoverageFlowing, core.CoverageSilent, core.CoverageUndeclared:
			n.Coverage.Evidence = []core.Evidence{{Kind: core.EvidenceObserved, Sink: "logsink:app"}}
		case core.CoverageBlind:
			n.Coverage.Evidence = []core.Evidence{{Kind: core.EvidenceNone}}
		}
		_ = i
		g.Nodes = append(g.Nodes, n)
	}
	g.Normalize()
	return g
}

func renderGraph(t *testing.T, g *core.Graph, opts Options) string {
	t.Helper()

	out, err := Render(g, opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

// The healthy state must add nothing at all. A map where everything is fine
// has to read as a map where nothing is flagged, or nobody will read it twice.
func TestFlowingGetsNoDecoration(t *testing.T) {
	with := renderGraph(t, coverageGraph(core.CoverageFlowing), Options{})

	plain := coverageGraph()
	plain.Nodes = append(plain.Nodes, core.Node{ID: "flowing_node", Type: "aws_ecs_service", Name: "flowing"})
	plain.Normalize()

	line := nodeLine(t, with, "flowing_node")
	if strings.Contains(line, "dashed") || strings.Contains(line, "penwidth") {
		t.Errorf("a healthy node is decorated: %s", line)
	}
	if strings.Contains(line, "·") {
		t.Errorf("a healthy node carries a badge: %s", line)
	}
}

func TestCoverageStatesAreVisuallyDistinct(t *testing.T) {
	g := coverageGraph(core.CoverageSilent, core.CoverageBlind, core.CoverageUndeclared, core.CoverageUnknown)
	out := renderGraph(t, g, Options{})

	seen := map[string]bool{}
	for _, st := range []core.CoverageState{core.CoverageSilent, core.CoverageBlind, core.CoverageUndeclared, core.CoverageUnknown} {
		line := nodeLine(t, out, string(st)+"_node")
		colour := attr(line, "color=")
		if seen[colour] {
			t.Errorf("state %q reuses the stroke colour %s of another state", st, colour)
		}
		seen[colour] = true
	}
}

// The word is what survives a monochrome print, a projector and a colour
// deficiency, and a coverage map is exactly the kind of picture that gets
// screenshotted into a chat window.
func TestStatesCarryAWordNotJustAColour(t *testing.T) {
	g := coverageGraph(core.CoverageSilent, core.CoverageBlind, core.CoverageUndeclared)
	out := renderGraph(t, g, Options{})

	for _, want := range []string{"silent", "no logs", "unmodelled"} {
		if !strings.Contains(out, want) {
			t.Errorf("the diagram never says %q", want)
		}
	}
}

func TestCoverageReasonAndEvidenceAreInTheTooltip(t *testing.T) {
	g := coverageGraph(core.CoverageBlind)
	out := renderGraph(t, g, Options{})

	line := nodeLine(t, out, "blind_node")
	if !strings.Contains(line, "because") {
		t.Errorf("the tooltip does not carry the reason: %s", line)
	}
	if !strings.Contains(line, "none") {
		t.Errorf("the tooltip does not carry the evidence: %s", line)
	}
}

func TestLegendListsOnlyThePresentCoverageStates(t *testing.T) {
	g := coverageGraph(core.CoverageBlind)
	out := renderGraph(t, g, Options{Legend: true})

	if !strings.Contains(out, "no log destination") {
		t.Error("the legend does not explain the state the graph contains")
	}
	if strings.Contains(out, "logs from nothing declared") {
		t.Error("the legend advertises a state the graph has no examples of")
	}
}

func TestAGraphWithNoCoverageGetsNoCoverageLegend(t *testing.T) {
	out := renderGraph(t, fixture(), Options{Legend: true})

	if strings.Contains(out, "not assessed") {
		t.Error("a graph carrying no coverage still advertises coverage in its legend")
	}
}

func claimedEdgeGraph() *core.Graph {
	g := &core.Graph{
		Version: core.Version,
		Axes:    []core.Axis{{ID: core.AxisNetwork}},
		Nodes: []core.Node{
			{ID: "a", Type: "aws_ecs_service", Name: "a"},
			{ID: "b", Type: "aws_db_instance", Name: "b"},
			{ID: "c", Type: "aws_db_instance", Name: "c"},
		},
		Edges: []core.Edge{
			{From: "a", To: "b", Kind: core.EdgeObserved,
				Claim: &core.Claim{Origin: core.OriginAI, Author: "assistant", Confidence: conf(0.6)}},
			{From: "a", To: "c", Kind: core.EdgeIACRef,
				Suppressed: true, Claim: &core.Claim{Origin: core.OriginHuman}},
		},
	}
	g.Normalize()
	return g
}

func TestClaimedEdgesUseAHollowArrowhead(t *testing.T) {
	out := renderGraph(t, claimedEdgeGraph(), Options{})

	line := edgeLine(t, out, `"a" -> "b"`)
	if !strings.Contains(line, "arrowhead=onormal") {
		t.Errorf("an asserted edge is drawn like a derived one: %s", line)
	}
	if !strings.Contains(line, "confidence") {
		t.Errorf("the tooltip does not say how sure the claimant was: %s", line)
	}
}

// The edge stays in the picture: a reader who cannot see it cannot judge the
// claim that it is not real.
func TestSuppressedEdgesAreDrawnFaintly(t *testing.T) {
	out := renderGraph(t, claimedEdgeGraph(), Options{})

	line := edgeLine(t, out, `"a" -> "c"`)
	if !strings.Contains(line, "style=dotted") {
		t.Errorf("a suppressed edge is not drawn faintly: %s", line)
	}
	if !strings.Contains(line, "asserted not to exist") {
		t.Errorf("the tooltip does not explain why it is faint: %s", line)
	}
}

// A conflict the document had to display one side of must still be visible,
// or the graph looks exactly like one where nobody disagreed.
func TestContestedThingsAreVisible(t *testing.T) {
	g := claimedEdgeGraph()
	g.Conflicts = []core.Conflict{{
		TargetKind: core.ConflictTargetEntity,
		Target:     "a",
		Field:      "name",
		Claims: []core.ClaimedValue{
			{Value: "a", Claim: core.Claim{Origin: core.OriginHuman}},
			{Value: "old", Claim: core.Claim{Origin: core.OriginParser}},
		},
	}}
	out := renderGraph(t, g, Options{})

	line := nodeLine(t, out, "a")
	if !strings.Contains(line, "penwidth=2.6") {
		t.Errorf("a contested node is drawn like an uncontested one: %s", line)
	}
	if !strings.Contains(line, "disagree") {
		t.Errorf("the tooltip does not say the claims disagree: %s", line)
	}
}

func TestContestedTargetKindsDoNotCrossHighlight(t *testing.T) {
	edgeTarget := core.EdgeKey("a", "b", core.EdgeIACRef)
	claims := []core.ClaimedValue{
		{Value: "one", Claim: core.Claim{Origin: core.OriginHuman}},
		{Value: "two", Claim: core.Claim{Origin: core.OriginParser}},
	}
	graph := func(kind core.ConflictTargetKind) *core.Graph {
		g := &core.Graph{
			Version: core.Version,
			Axes:    []core.Axis{{ID: core.AxisNetwork}},
			Nodes: []core.Node{
				{ID: "a", Type: "service", Name: "a"},
				{ID: "b", Type: "service", Name: "b"},
				{ID: edgeTarget, Type: "service", Name: "same spelling"},
			},
			Edges: []core.Edge{{From: "a", To: "b", Kind: core.EdgeIACRef}},
			Conflicts: []core.Conflict{{
				TargetKind: kind,
				Target:     edgeTarget,
				Field:      "name",
				Claims:     claims,
			}},
		}
		g.Normalize()
		return g
	}

	t.Run("entity conflict does not highlight edge", func(t *testing.T) {
		out := renderGraph(t, graph(core.ConflictTargetEntity), Options{})
		if line := nodeLine(t, out, edgeTarget); !strings.Contains(line, "penwidth=2.6") {
			t.Fatalf("same-spelled entity was not highlighted: %s", line)
		}
		if line := edgeLine(t, out, `"a" -> "b"`); strings.Contains(line, "penwidth=2.6") {
			t.Fatalf("entity conflict cross-highlighted edge: %s", line)
		}
	})

	t.Run("edge conflict does not highlight entity", func(t *testing.T) {
		out := renderGraph(t, graph(core.ConflictTargetEdge), Options{})
		if line := edgeLine(t, out, `"a" -> "b"`); !strings.Contains(line, "penwidth=2.6") {
			t.Fatalf("same-spelled edge was not highlighted: %s", line)
		}
		if line := nodeLine(t, out, edgeTarget); strings.Contains(line, "penwidth=2.6") {
			t.Fatalf("edge conflict cross-highlighted entity: %s", line)
		}
	})
}

func nodeLine(t *testing.T, out, id string) string {
	t.Helper()

	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, `"`+id+`" [`) {
			return trimmed
		}
	}
	t.Fatalf("node %q is not in the output", id)
	return ""
}

func edgeLine(t *testing.T, out, prefix string) string {
	t.Helper()

	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return trimmed
		}
	}
	t.Fatalf("edge %q is not in the output", prefix)
	return ""
}

// attr reads one DOT attribute. The delimiter matters: searching for "color="
// alone would find "fillcolor=" first and quietly compare the wrong channel.
func attr(line, key string) string {
	for _, prefix := range []string{" " + key, "[" + key} {
		i := strings.Index(line, prefix)
		if i < 0 {
			continue
		}
		rest := line[i+len(prefix):]
		if !strings.HasPrefix(rest, `"`) {
			continue
		}
		if j := strings.Index(rest[1:], `"`); j >= 0 {
			return rest[1 : j+1]
		}
	}
	return ""
}
