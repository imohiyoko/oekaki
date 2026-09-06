package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A graph with a route something walked that nothing declares — the alert this
// whole thing exists for — and a declared route nothing has walked.
func watchedGraph(t *testing.T) string {
	t.Helper()
	g := core.New()
	for _, id := range []string{"gateway", "checkout", "ledger", "reports", "archive"} {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	g.Edges = []core.Edge{
		{From: "gateway", To: "checkout", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "checkout", To: "ledger", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "reports", To: "archive", Kind: core.EdgeIACRef, Relation: "calls"},
	}
	g.Paths = []core.Path{
		{Nodes: []string{"gateway", "checkout", "ledger"}, Kind: core.EdgeIACRef},
		{Nodes: []string{"reports", "archive"}, Kind: core.EdgeIACRef},
		{Nodes: []string{"gateway", "ledger"}, Kind: core.EdgeObserved},
	}
	g.Normalize()

	raw, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "watched.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func rulesFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

const unexpectedRule = `{"kind":"oekaki.rules","version":"0.1","rules":[
	{"name":"a route fired that nothing declares","severity":"page","when":{"is":"unexpected"}}]}`

func TestAlertsRunARuleAgainstAGraph(t *testing.T) {
	r := mustRun(t, "", "alerts", watchedGraph(t), "--rules", rulesFile(t, unexpectedRule))

	if !strings.Contains(r.stdout, "a route fired that nothing declares") {
		t.Fatalf("the rule did not fire: %q", r.stdout)
	}
	if !strings.Contains(r.stdout, "gateway → ledger") {
		t.Errorf("the alert does not say what it is about: %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "1 rule, 1 fired") {
		t.Errorf("the run does not say what happened: %q", r.stderr)
	}
}

// A pipeline that should stop asks for it. Reporting and carrying on is the
// default, because a listing is also something people read while nothing is
// wrong.
func TestAlertsMoveTheExitCodeOnlyWhenAsked(t *testing.T) {
	graph, rules := watchedGraph(t), rulesFile(t, unexpectedRule)

	if r := run(t, "", "alerts", graph, "--rules", rules); r.code != 0 {
		t.Errorf("a rule firing stopped a run that did not ask to be stopped: %d", r.code)
	}
	if r := run(t, "", "alerts", graph, "--rules", rules, "--exit-code"); r.code == 0 {
		t.Error("--exit-code did not move the exit code when a rule fired")
	}
}

func TestAlertsWriteJSONWithTheMomentItAsked(t *testing.T) {
	r := mustRun(t, "", "alerts", watchedGraph(t), "--rules", rulesFile(t, unexpectedRule),
		"-f", "json", "--since", "2026-08-01T00:00:00Z")

	var doc struct {
		Since  string `json:"since"`
		Alerts []struct {
			Rule     string `json:"rule"`
			Severity string `json:"severity"`
			Subject  string `json:"subject"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatalf("%v: %s", err, r.stdout)
	}
	if doc.Since != "2026-08-01T00:00:00Z" {
		t.Errorf("the document does not carry the moment it was asking about: %q", doc.Since)
	}
	if len(doc.Alerts) == 0 || doc.Alerts[0].Severity != "page" {
		t.Fatalf("got %#v", doc.Alerts)
	}
	if !strings.HasPrefix(doc.Alerts[0].Subject, "path:") {
		t.Errorf("the alert does not name its subject the way the document does: %#v", doc.Alerts[0])
	}
}

// A rules document is one of the things this project publishes a contract for,
// so it goes through the same front door as the others.
func TestValidateKnowsARulesDocument(t *testing.T) {
	r := mustRun(t, "", "validate", rulesFile(t, unexpectedRule))
	if !strings.Contains(r.stdout, "1 rules") {
		t.Errorf("validate does not recognise a rules document: %q", r.stdout)
	}

	broken := rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"x","when":{"is":"above","metric":"m"}}]}`)
	if bad := run(t, "", "validate", broken); bad.code == 0 {
		t.Error("a rule with a bound and nothing to compare against was accepted")
	}
}

func TestAlertsNeedsRules(t *testing.T) {
	if r := run(t, "", "alerts", watchedGraph(t)); r.code == 0 {
		t.Error("alerts ran with nothing to check against")
	}
}
