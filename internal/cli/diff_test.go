package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func pair(t *testing.T) (string, string) {
	t.Helper()
	before := core.New()
	before.Nodes = []core.Node{
		{ID: "svc:api", Type: "service", Name: "api", Attrs: map[string]any{"instance_type": "t3.micro"}},
		{ID: "db:orders", Type: "database", Name: "orders"},
	}
	before.Edges = []core.Edge{
		{From: "svc:api", To: "db:orders", Kind: core.EdgeIACRef, Relation: "reads"},
	}
	before.Normalize()

	after := core.New()
	after.Nodes = []core.Node{
		{ID: "svc:api", Type: "service", Name: "api", Attrs: map[string]any{"instance_type": "t3.large"}},
		{ID: "db:orders", Type: "database", Name: "orders"},
	}
	after.Edges = before.Edges
	after.Normalize()

	return graphFile(t, before), graphFile(t, after)
}

// The line a reviewer reads: what moved, and what it used to be.
func TestTheTableSaysBothSidesOfWhatMoved(t *testing.T) {
	before, after := pair(t)
	r := mustRun(t, "", "diff", before, after)

	for _, want := range []string{"changed", "svc:api", "attr:instance_type", "t3.micro", "t3.large", "→"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the listing does not carry %q: %q", want, r.stdout)
		}
	}
	if !strings.Contains(r.stderr, "1 change") {
		t.Errorf("the run does not say how much moved: %q", r.stderr)
	}
}

// Nothing changed says so, rather than printing an empty page somebody has to
// interpret.
func TestNothingChangedSaysSo(t *testing.T) {
	before, _ := pair(t)
	r := mustRun(t, "", "diff", before, before)
	if strings.TrimSpace(r.stdout) != "" {
		t.Errorf("something was reported: %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "nothing changed") {
		t.Errorf("the run does not say the graphs agree: %q", r.stderr)
	}
}

// A pipeline that should stop asks for it, the way `alerts` does.
func TestAChangedGraphCanStopAPipeline(t *testing.T) {
	before, after := pair(t)
	if r := run(t, "", "diff", before, after); r.code != 0 {
		t.Fatalf("a diff without --exit-code failed: %d", r.code)
	}
	if r := run(t, "", "diff", before, after, "--exit-code"); r.code == 0 {
		t.Error("a changed graph did not stop the pipeline")
	}
	if r := run(t, "", "diff", before, before, "--exit-code"); r.code != 0 {
		t.Errorf("an unchanged graph stopped the pipeline: %d", r.code)
	}
}

// The machine-readable side carries the same answer, with the subject a caller
// can look up rather than only the label a person reads.
func TestTheJSONCarriesTheSubjectAndTheFields(t *testing.T) {
	before, after := pair(t)
	r := mustRun(t, "", "diff", before, after, "-f", "json")

	var got struct {
		Changes []struct {
			Kind    string `json:"kind"`
			What    string `json:"what"`
			Subject string `json:"subject"`
			Fields  []struct {
				Field string `json:"field"`
				From  string `json:"from"`
				To    string `json:"to"`
			} `json:"fields"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("%v: %q", err, r.stdout)
	}
	if len(got.Changes) != 1 {
		t.Fatalf("got %d changes: %#v", len(got.Changes), got.Changes)
	}
	c := got.Changes[0]
	if c.Kind != "changed" || c.What != "node" || c.Subject != "svc:api" {
		t.Errorf("the change does not say what it is about: %#v", c)
	}
	if len(c.Fields) != 1 || c.Fields[0].Field != "attr:instance_type" {
		t.Fatalf("the fields are %#v", c.Fields)
	}
	if c.Fields[0].From != `"t3.micro"` || c.Fields[0].To != `"t3.large"` {
		t.Errorf("both sides are not in the document: %#v", c.Fields[0])
	}
}

// Two graphs, not one and not three: the command exists to compare a before
// with an after, and being handed one of them is a mistake worth naming.
func TestDiffNeedsTwoGraphs(t *testing.T) {
	before, _ := pair(t)
	if r := run(t, "", "diff", before); r.code == 0 {
		t.Error("one graph was accepted as a comparison")
	}
}
