package cli

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// The flag has to reach the one condition that cannot do without a moment.
// Filling it in after the document was checked meant the check refused the
// document first, so --since could never be the answer to what it complained
// about.
func TestSinceReachesAQuietRule(t *testing.T) {
	graph := watchedGraph(t)
	rules := rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"a route stopped","about":{"kind":"path"},
		 "when":{"is":"quiet","metric":"path_requests"}}]}`)

	if bad := run(t, "", "alerts", graph, "--rules", rules); bad.code == 0 {
		t.Error("a quiet rule with no moment anywhere was accepted")
	}
	r := mustRun(t, "", "alerts", graph, "--rules", rules, "--since", "2026-08-01T00:00:00Z")
	if !strings.Contains(r.stdout, "a route stopped") {
		t.Fatalf("the moment the run was given did not reach the rule: %q %q", r.stdout, r.stderr)
	}
}

// A span in a document is refused where it is written. Left unchecked it
// reaches a comparison that falls back to comparing the text, every subject
// fires, and under --exit-code that stops a pipeline for nothing.
func TestASpanInARuleIsRefusedBeforeItCanFireEverything(t *testing.T) {
	rules := rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"a route stopped","when":{"is":"quiet","metric":"path_requests","since":"30d"}}]}`)

	bad := run(t, "", "alerts", watchedGraph(t), "--rules", rules)
	if bad.code == 0 {
		t.Fatal("a span was accepted where a moment belongs")
	}
	if !strings.Contains(bad.stderr, "command line") {
		t.Errorf("the refusal does not say where a span belongs: %q", bad.stderr)
	}
}

// Silence here reads as "everything observed is a surprise", which is what a
// rule about unexpected routes then says — one alert per route, none of them
// about anything that happened.
func TestARunThatCouldDeriveNoRoutesSaysSo(t *testing.T) {
	// An estate whose only way in is called by something else: there is
	// nowhere for a derived route to start.
	g := core.New()
	for _, id := range []string{"a", "b"} {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	g.Edges = []core.Edge{
		{From: "a", To: "b", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "b", To: "a", Kind: core.EdgeIACRef, Relation: "calls"},
	}
	g.Paths = []core.Path{{Nodes: []string{"a", "b"}, Kind: core.EdgeObserved}}
	g.Normalize()

	r := mustRun(t, "", "alerts", graphFile(t, g), "--rules",
		rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
			{"name":"unannounced","when":{"is":"unexpected"}}]}`))

	if !strings.Contains(r.stderr, "no declared routes could be derived") {
		t.Errorf("the run does not say why everything is a surprise: %q", r.stderr)
	}
}

// What was measured and when, which is what the path listing prints and what
// somebody deciding whether to act needs.
func TestTheTableSaysWhenAndHowMuch(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "ledger", Type: "service", Name: "ledger"}}
	value := 4000.0
	g.Observations = []core.Observation{
		{Subject: "ledger", Metric: "request_rate", Value: &value, ObservedAt: "2026-09-05T00:00:00Z"},
	}
	g.Normalize()

	r := mustRun(t, "", "alerts", graphFile(t, g), "--rules",
		rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
			{"name":"too busy","when":{"is":"above","metric":"request_rate","value":1000}}]}`))

	if !strings.Contains(r.stdout, "last 2026-09-05T00:00:00Z") || !strings.Contains(r.stdout, "4000") {
		t.Errorf("the listing does not say when it was measured or what it said: %q", r.stdout)
	}
}
