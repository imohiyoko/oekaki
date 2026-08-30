package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

const overlayBody = `{
  "kind": "oekaki.overlay",
  "version": "0.1",
  "metadata": { "origin": "human", "author": "operator", "window": "last-7d" },
  "sinks": [{ "id": "sink.app", "type": "log_group", "name": "/platform/app" }],
  "assertions": [
    { "assert": "logs.declared", "subject": { "service": "api" }, "sink": "sink.app" },
    { "assert": "logs.observed", "subject": { "service": "api" }, "sink": "sink.app", "records": 42 },
    { "assert": "logs.none", "subject": { "node": "aws_instance.bastion" } },
    { "assert": "logs.observed", "subject": { "service": "ghost" }, "sink": "sink.app", "records": 7 }
  ]
}`

func overlayFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHideSuppressedDropsConflictsForHiddenEdges(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "a", Type: "service"}, {ID: "b", Type: "service"}}
	g.Edges = []core.Edge{{From: "a", To: "b", Kind: core.EdgeIACRef, Suppressed: true}}
	g.Conflicts = []core.Conflict{{
		TargetKind: core.ConflictTargetEdge,
		Target:     core.EdgeKey("a", "b", core.EdgeIACRef),
		Field:      "suppressed",
		Claims:     []core.ClaimedValue{{Value: "false", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "true", Claim: core.Claim{Origin: core.OriginHuman}}},
	}}
	g.Normalize()

	out := hideSuppressed(g)
	if len(out.Edges) != 0 || len(out.Conflicts) != 0 {
		t.Fatalf("hidden graph retained edge provenance: edges=%#v conflicts=%#v", out.Edges, out.Conflicts)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("hidden graph is invalid: %v", err)
	}
}

func TestHideSuppressedDoesNotMutateInputConflicts(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "a", Type: "service"}, {ID: "b", Type: "service"}}
	g.Edges = []core.Edge{{From: "a", To: "b", Kind: core.EdgeIACRef, Suppressed: true}}
	g.Conflicts = []core.Conflict{
		{TargetKind: core.ConflictTargetEdge, Target: core.EdgeKey("a", "b", core.EdgeIACRef), Field: "suppressed", Claims: []core.ClaimedValue{{Value: "false", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "true", Claim: core.Claim{Origin: core.OriginHuman}}}},
		{TargetKind: core.ConflictTargetEntity, Target: "a", Field: "name", Claims: []core.ClaimedValue{{Value: "a", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "A", Claim: core.Claim{Origin: core.OriginHuman}}}},
	}
	g.Normalize()
	before, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}

	out := hideSuppressed(g)
	if len(out.Conflicts) != 1 || out.Conflicts[0].Target != "a" {
		t.Fatalf("filtered conflicts = %#v", out.Conflicts)
	}
	after, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("hideSuppressed mutated its input:\n%s\n---\n%s", before, after)
	}
}

func TestHideSuppressedKeepsConflictForVisibleEdge(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "a", Type: "service"}, {ID: "b", Type: "service"}}
	g.Edges = []core.Edge{{From: "a", To: "b", Kind: core.EdgeObserved, Relation: "calls"}}
	g.Conflicts = []core.Conflict{{
		TargetKind: core.ConflictTargetEdge,
		Target:     core.EdgeKey("a", "b", core.EdgeObserved, "calls"),
		Field:      "claim",
		Claims:     []core.ClaimedValue{{Value: "parser", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "human", Claim: core.Claim{Origin: core.OriginHuman}}},
	}}
	g.Normalize()

	out := hideSuppressed(g)
	if len(out.Edges) != 1 || len(out.Conflicts) != 1 || out.Conflicts[0].TargetKind != core.ConflictTargetEdge {
		t.Fatalf("visible edge provenance was dropped: edges=%#v conflicts=%#v", out.Edges, out.Conflicts)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("visible graph is invalid: %v", err)
	}
}

func TestOverlayAddsCoverage(t *testing.T) {
	r := mustRun(t, "", "graph", plan, "--overlay", overlayFile(t, overlayBody))

	var g struct {
		Nodes []struct {
			ID       string `json:"id"`
			Coverage *struct {
				State string `json:"state"`
			} `json:"coverage"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &g); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, n := range g.Nodes {
		if n.Coverage != nil {
			got[n.ID] = n.Coverage.State
		}
	}
	for id, want := range map[string]string{
		"aws_ecs_service.api":  "flowing",
		"aws_instance.bastion": "blind",
	} {
		if got[id] != want {
			t.Errorf("%s is %q, want %q", id, got[id], want)
		}
	}
}

// The composition proof. If rendering a graph that already carries coverage
// differs from rendering the plan with the overlay applied, then the IR is not
// carrying everything the renderer needs — which is the precondition for any
// later consumer, an HTML view included, being a pure function of the file.
func TestGraphWithOverlayThenRenderMatchesDirectRender(t *testing.T) {
	path := overlayFile(t, overlayBody)
	dir := t.TempDir()

	graphFile := filepath.Join(dir, "g.json")
	mustRun(t, "", "graph", plan, "--overlay", path, "-o", graphFile)

	viaIR := mustRun(t, "", "render", graphFile, "-f", "dot").stdout
	direct := mustRun(t, "", "render", plan, "--overlay", path, "-f", "dot").stdout

	if viaIR != direct {
		t.Error("rendering through the IR differs from rendering directly, so the IR is losing something")
	}
}

// Nothing may disappear quietly: the summary is printed on every run.
func TestUnmatchedEvidenceIsReportedOnStderr(t *testing.T) {
	r := mustRun(t, "", "graph", plan, "--overlay", overlayFile(t, overlayBody))

	if !strings.Contains(r.stderr, "matched nothing") {
		t.Errorf("stderr does not mention the unmatched subject: %q", r.stderr)
	}
	if !strings.Contains(r.stderr, "ghost") {
		t.Errorf("stderr does not name the unmatched subject: %q", r.stderr)
	}
	if !strings.Contains(r.stderr, "last-7d") {
		t.Errorf("stderr does not say what period the numbers cover: %q", r.stderr)
	}
}

func TestOverlayUnmatchedErrorExitsNonZero(t *testing.T) {
	r := run(t, "", "graph", plan, "--overlay", overlayFile(t, overlayBody), "--overlay-unmatched", "error")

	if r.code == 0 {
		t.Fatal("strict mode accepted an unmatched subject")
	}
	if !strings.Contains(r.stderr, "ghost") {
		t.Errorf("stderr does not name the subject: %q", r.stderr)
	}
}

func TestOverlayReportIsWritten(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.json")
	mustRun(t, "", "graph", plan, "--overlay", overlayFile(t, overlayBody), "--overlay-report", out)

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Applied   int `json:"applied"`
		Unmatched []struct {
			Action string `json:"action"`
		} `json:"unmatched"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Applied == 0 {
		t.Error("the report claims nothing was applied")
	}
	if len(report.Unmatched) != 1 || report.Unmatched[0].Action != "adopted" {
		t.Errorf("the report does not record the adoption: %s", data)
	}
}

// The download flow produces overlay.json, overlay (1).json and so on, so the
// flag has to be repeatable — and it has to survive the argument permuter
// that lets flags follow the positional.
func TestRepeatableOverlayFlagSurvivesArgPermutation(t *testing.T) {
	first := overlayFile(t, overlayBody)
	second := overlayFile(t, `{
	  "kind": "oekaki.overlay", "version": "0.1",
	  "metadata": { "origin": "ai", "author": "assistant" },
	  "assertions": [
	    { "assert": "edge", "from": { "node": "aws_instance.bastion" },
	      "to": { "node": "aws_db_instance.main" }, "kind": "observed", "confidence": 0.6 }
	  ]
	}`)

	r := mustRun(t, "", "graph", plan, "--overlay", first, "--overlay", second)

	if !strings.Contains(r.stdout, `"origin": "ai"`) {
		t.Error("the second overlay was not applied")
	}
	if !strings.Contains(r.stdout, `"state": "flowing"`) {
		t.Error("the first overlay was not applied")
	}
}

func TestOverlayCanBeReadFromStdin(t *testing.T) {
	r := mustRun(t, overlayBody, "graph", plan, "--overlay", "-")

	if !strings.Contains(r.stdout, `"state": "flowing"`) {
		t.Error("an overlay on standard input was not applied")
	}
}

func TestHideSuppressedLeavesTheEdgeInTheIR(t *testing.T) {
	body := `{
	  "kind": "oekaki.overlay", "version": "0.1",
	  "metadata": { "origin": "human" },
	  "assertions": [
	    { "assert": "edge.suppress", "from": { "node": "aws_lb_listener.http" },
	      "to": { "node": "aws_lb.public" }, "kind": "iac_ref" }
	  ]
	}`
	path := overlayFile(t, body)

	shown := mustRun(t, "", "render", plan, "--overlay", path, "-f", "dot").stdout
	hidden := mustRun(t, "", "render", plan, "--overlay", path, "-f", "dot", "--hide-suppressed").stdout
	if len(hidden) >= len(shown) {
		t.Error("--hide-suppressed did not remove anything from the picture")
	}

	graph := mustRun(t, "", "graph", plan, "--overlay", path).stdout
	if !strings.Contains(graph, `"suppressed": true`) {
		t.Error("suppression was lost from the IR; the record of the disagreement has to survive")
	}
}

func TestValidateAcceptsAnOverlay(t *testing.T) {
	r := mustRun(t, overlayBody, "validate", "-")

	if !strings.Contains(r.stdout, "assertions") {
		t.Errorf("validate did not report on the overlay: %q", r.stdout)
	}
}

func TestValidateRejectsABrokenOverlay(t *testing.T) {
	r := run(t, `{"kind":"oekaki.overlay","version":"0.1","assertions":[{"assert":"logs.none","subject":{"svc":"api"}}]}`,
		"validate", "-")

	if r.code == 0 {
		t.Fatal("an overlay with an unknown selector key was accepted")
	}
	if !strings.Contains(r.stderr, "service") {
		t.Errorf("the error does not offer the known keys: %q", r.stderr)
	}
}

func TestSchemaPrintsTheOverlayContract(t *testing.T) {
	r := mustRun(t, "", "schema", "--overlay")

	if !strings.Contains(r.stdout, "oekaki.overlay") {
		t.Errorf("schema --overlay did not print the overlay contract: %q", r.stdout[:min(200, len(r.stdout))])
	}
}
