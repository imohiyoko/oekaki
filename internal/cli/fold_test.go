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

// The page gets the graph and the record; every other format gets the folded
// graph, because a picture cannot be opened.
func TestAFoldedPageCanBeUnfolded(t *testing.T) {
	page := mustRun(t, "", "render", crowdedGraph(t), "-f", "html", "--fold", "--fold-budget", "5").stdout

	if !strings.Contains(page, `id="oekaki-folds"`) {
		t.Fatal("the page carries no record of what was folded, so nothing can be put back")
	}
	if !strings.Contains(page, `"pod:worker-c"`) {
		t.Fatal("the page does not carry the boxes that were folded")
	}
}

// An atlas draws a page per level, and each page is folded on its own terms: a
// crowd on a level is not a crowd on a detail page, and a budget spent against
// the whole estate would land on a page holding three of a fold's twelve
// members.
func TestFoldingAnAtlasHappensPerPage(t *testing.T) {
	r := mustRun(t, "", "render", crowdedGraph(t), "-f", "html", "--fold", "--fold-budget", "5", "--atlas")

	if !strings.Contains(r.stdout, `id="oekaki-folds"`) {
		t.Fatal("an atlas page carries no record of what was folded")
	}
	if !strings.Contains(r.stdout, `"diagram"`) {
		t.Fatal("a fold does not say which page it belongs to")
	}
	if !strings.Contains(r.stderr, "pages") {
		t.Errorf("the run does not say what it folded: %q", r.stderr)
	}
}

// A drawing that did not need help and one that needed it and could not be
// given any are different facts, and the reader meets the mat of boxes either
// way.
func TestNothingFoldableSaysSoDifferently(t *testing.T) {
	over := run(t, "", "render", crowdedGraph(t), "-f", "json", "--fold",
		"--fold-budget", "2", "--fold-rules", "chain")
	if !strings.Contains(over.stderr, "no rule applies") {
		t.Errorf("a drawing over its budget with nothing foldable was called fine: %q", over.stderr)
	}
	under := mustRun(t, "", "render", crowdedGraph(t), "-f", "json", "--fold", "--fold-budget", "100")
	if !strings.Contains(under.stderr, "already inside the budget") {
		t.Errorf("a drawing inside its budget was reported as unfoldable: %q", under.stderr)
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

// Every format but the page has no pages for an atlas to be, so there is
// nothing to fold per page and the estate is folded as a whole. A flag that
// quietly does nothing is the one outcome worth avoiding: a reader who asked
// for a readable drawing and got the mat of boxes cannot tell that the
// combination was the reason.
func TestFoldStillWorksWhenAnAtlasCannotBeDrawn(t *testing.T) {
	r := mustRun(t, "", "render", crowdedGraph(t), "-f", "json", "--fold", "--fold-budget", "5", "--atlas")

	var g core.Graph
	if err := json.Unmarshal([]byte(r.stdout), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("got %d boxes: --atlas turned --fold into a flag that does nothing", len(g.Nodes))
	}
	if !strings.Contains(r.stderr, "folded to") {
		t.Errorf("the run does not say what it folded: %q", r.stderr)
	}
	// And the flag that could not do anything says so, rather than being
	// dropped on the floor.
	if !strings.Contains(r.stderr, "ignored here") {
		t.Errorf("--atlas was ignored without saying so: %q", r.stderr)
	}
}
