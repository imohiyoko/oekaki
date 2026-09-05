package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A service in front of twenty identical workers: the shape that makes a
// drawing unreadable without anything about the estate being unusual.
func crowdedGraph(t *testing.T) string {
	t.Helper()
	g := core.New()
	g.Nodes = []core.Node{{ID: "svc:api", Type: "service", Name: "api"}}
	for i := range 20 {
		id := "pod:worker-" + string(rune('a'+i))
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "pod", Name: "worker"})
		g.Edges = append(g.Edges, core.Edge{From: "svc:api", To: id, Kind: core.EdgeIACRef, Relation: "selects"})
	}
	g.Normalize()
	raw, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "crowded.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFoldDrawsFewerBoxesAndSaysSo(t *testing.T) {
	path := crowdedGraph(t)
	r := mustRun(t, "", "render", path, "-f", "json", "--fold", "--fold-budget", "5")

	var g core.Graph
	if err := json.Unmarshal([]byte(r.stdout), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("got %d boxes, want the service and one standing for the workers", len(g.Nodes))
	}
	// A drawing that quietly stands for more than it shows is one a reader can
	// be wrong about without knowing.
	if !strings.Contains(r.stderr, "folded to") || !strings.Contains(r.stderr, "standing for 20") {
		t.Errorf("the run does not say what it folded: %q", r.stderr)
	}
}

// Rules run until the drawing is inside the budget and then stop.
func TestFoldStopsWhenTheDrawingFits(t *testing.T) {
	r := mustRun(t, "", "render", crowdedGraph(t), "-f", "json", "--fold", "--fold-budget", "100")
	if !strings.Contains(r.stderr, "nothing folded") {
		t.Errorf("a drawing already inside its budget was folded: %q", r.stderr)
	}
}

func TestFoldKeepsWhatTheReaderIsLookingAt(t *testing.T) {
	r := mustRun(t, "", "render", crowdedGraph(t), "-f", "json", "--fold", "--fold-budget", "5",
		"--fold-keep", "pod:worker-c")

	if !strings.Contains(r.stdout, `"pod:worker-c"`) {
		t.Error("the box the reader asked to keep was folded away")
	}
}

func TestUnknownFoldRuleIsRefused(t *testing.T) {
	if r := run(t, "", "render", crowdedGraph(t), "-f", "json", "--fold", "--fold-rules", "squash"); r.code == 0 {
		t.Error("an unknown fold rule was accepted")
	}
}

// Without the flag nothing changes, because turning folding on by default
// would rewrite every diagram anybody has committed.
func TestFoldingIsOptIn(t *testing.T) {
	path := crowdedGraph(t)
	plain := mustRun(t, "", "render", path, "-f", "json")
	var g core.Graph
	if err := json.Unmarshal([]byte(plain.stdout), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 21 {
		t.Fatalf("got %d boxes, want all of them", len(g.Nodes))
	}
}
