package cli

import (
	"encoding/json"
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

// The suffix carries what the reason does not. A bound already names its value
// and a silence already names its moment, so appending both unconditionally
// printed the same fact twice on one line — which a reader parses as two
// facts, then goes looking for the difference between them.
func TestTheTableDoesNotSayTheSameThingTwice(t *testing.T) {
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

	if n := strings.Count(r.stdout, "4000"); n != 1 {
		t.Errorf("the value is printed %d times on one line: %q", n, r.stdout)
	}
}

// Nothing measured, nothing said about when: the rule cannot answer the
// question. It does not fire, and it does not pass in silence either — a run
// that says "nothing fired" about a rule that was never applied is telling the
// operator all is well.
func TestARuleThatCouldNotAnswerSaysSo(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "ledger", Type: "service", Name: "ledger"}}
	value := 5.0
	g.Observations = []core.Observation{{Subject: "ledger", Metric: "beat", Value: &value}}
	g.Normalize()

	r := mustRun(t, "", "alerts", graphFile(t, g), "--rules",
		rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
			{"name":"stopped","about":{"subject":"ledger"},
			 "when":{"is":"quiet","metric":"beat"}}]}`),
		"--since", "2026-08-01T00:00:00Z")

	if strings.Contains(r.stdout, "stopped") {
		t.Errorf("a reading with no time on it fired a silence rule: %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "no time on the reading") {
		t.Errorf("the run does not say the rule could not answer: %q", r.stderr)
	}
}

// A graph with no declared call to follow is the ordinary shape of one built
// from traces alone, and it is not the cycle the other message describes.
func TestNothingToFollowIsNotACycle(t *testing.T) {
	g := core.New()
	for _, id := range []string{"a", "b"} {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	g.Paths = []core.Path{{Nodes: []string{"a", "b"}, Kind: core.EdgeObserved}}
	g.Normalize()

	r := mustRun(t, "", "alerts", graphFile(t, g), "--rules",
		rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
			{"name":"unannounced","when":{"is":"unexpected"}}]}`))

	if !strings.Contains(r.stderr, "records no declared calls to follow") {
		t.Errorf("the run blames a cycle that is not there: %q", r.stderr)
	}
}

// Which half of the suffix the reason already carries is a property of the
// condition that wrote it, not of whether the digits happen to appear in the
// sentence. A quiet reason names a moment, and a moment contains 0, 1, 2 and
// 2026 — so looking for the value in the text dropped exactly the reading
// somebody most wants to see under a rule called "stopped".
func TestAHeartbeatOfZeroIsPrinted(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "ledger", Type: "service", Name: "ledger"}}
	zero := 0.0
	g.Observations = []core.Observation{
		{Subject: "ledger", Metric: "beat", Value: &zero, ObservedAt: "2026-01-01T00:00:00Z"},
	}
	g.Normalize()

	r := mustRun(t, "", "alerts", graphFile(t, g), "--rules",
		rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
			{"name":"stopped","about":{"subject":"ledger"},
			 "when":{"is":"quiet","metric":"beat"}}]}`),
		"--since", "2026-08-01T00:00:00Z")

	if !strings.Contains(r.stdout, "(0)") {
		t.Errorf("the reading the rule is about is not on the line: %q", r.stdout)
	}
}

// The same for a route: its reason names neither the moment nor the count, so
// both belong in the suffix — and the count is named, because a bare number
// after a route leaves the reader guessing what was counted.
func TestARouteAlertKeepsItsCount(t *testing.T) {
	g := core.New()
	for _, id := range []string{"gateway", "reports", "archive"} {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	g.Edges = []core.Edge{
		{From: "gateway", To: "reports", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "reports", To: "archive", Kind: core.EdgeIACRef, Relation: "calls"},
	}
	g.Paths = []core.Path{
		{Nodes: []string{"gateway", "reports", "archive"}, Kind: core.EdgeIACRef},
		{Nodes: []string{"gateway", "reports"}, Kind: core.EdgeObserved},
	}
	one := 1.0
	g.Observations = []core.Observation{{
		Subject: core.PathKey([]string{"gateway", "reports"}), Metric: "path_requests",
		Value: &one, Unit: "requests", ObservedAt: "2026-05-01T10:00:00Z",
	}}
	g.Normalize()

	r := mustRun(t, "", "alerts", graphFile(t, g), "--rules",
		rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
			{"name":"stops short","when":{"is":"partial"}}]}`),
		"--since", "2026-08-01T00:00:00Z")

	for _, want := range []string{"last 2026-05-01T10:00:00Z", "path_requests 1"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the line does not carry %q: %q", want, r.stdout)
		}
	}
}

// A bound names its value in the reason and a silence names its moment. Saying
// either again puts one fact on the line twice, which reads as two facts.
func TestTheSuffixDoesNotRepeatTheReason(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "ledger", Type: "service", Name: "ledger"}}
	busy := 4000.0
	g.Observations = []core.Observation{
		{Subject: "ledger", Metric: "request_rate", Value: &busy, ObservedAt: "2026-09-05T00:00:00Z"},
	}
	g.Normalize()

	r := mustRun(t, "", "alerts", graphFile(t, g), "--rules",
		rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
			{"name":"too busy","when":{"is":"above","metric":"request_rate","value":1000}}]}`))

	if n := strings.Count(r.stdout, "4000"); n != 1 {
		t.Errorf("the value is on the line %d times: %q", n, r.stdout)
	}
	if !strings.Contains(r.stdout, "last 2026-09-05T00:00:00Z") {
		t.Errorf("the moment the reason does not carry is missing: %q", r.stdout)
	}
}

// A consumer reading only `alerts` reads an empty list as "all well", and a
// rule that could not be applied produces exactly that empty list. Saying so
// on stderr fixes the table and leaves the machine-readable side telling the
// same lie the table used to.
func TestTheJSONSaysWhatCouldNotBeAnswered(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "ledger", Type: "service", Name: "ledger"}}
	five := 5.0
	g.Observations = []core.Observation{{Subject: "ledger", Metric: "beat", Value: &five}}
	g.Normalize()

	r := mustRun(t, "", "alerts", graphFile(t, g), "-f", "json", "--rules",
		rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
			{"name":"stopped","about":{"subject":"ledger"},
			 "when":{"is":"quiet","metric":"beat"}}]}`),
		"--since", "2026-08-01T00:00:00Z")

	var got struct {
		Unanswered []string `json:"unanswered"`
		Alerts     []struct {
			Is     string `json:"is"`
			Metric string `json:"metric"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("%v: %q", err, r.stdout)
	}
	if len(got.Unanswered) != 1 || !strings.Contains(got.Unanswered[0], "ledger") {
		t.Fatalf("the JSON does not say the rule could not answer: %#v", got.Unanswered)
	}
	if len(got.Alerts) != 0 {
		t.Fatalf("something fired: %#v", got.Alerts)
	}
}

// An alert says which condition wrote it, because the reason is a sentence
// written for that condition and everything else about the alert is read in
// its light.
func TestAnAlertSaysWhichConditionFired(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "ledger", Type: "service", Name: "ledger"}}
	busy := 4000.0
	g.Observations = []core.Observation{
		{Subject: "ledger", Metric: "request_rate", Value: &busy, ObservedAt: "2026-09-05T00:00:00Z"},
	}
	g.Normalize()

	r := mustRun(t, "", "alerts", graphFile(t, g), "-f", "json", "--rules",
		rulesFile(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
			{"name":"too busy","when":{"is":"above","metric":"request_rate","value":1000}}]}`))

	var got struct {
		Alerts []struct {
			Is     string `json:"is"`
			Metric string `json:"metric"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("%v: %q", err, r.stdout)
	}
	if len(got.Alerts) != 1 || got.Alerts[0].Is != "above" || got.Alerts[0].Metric != "request_rate" {
		t.Fatalf("the alert does not say what fired or what was measured: %#v", got.Alerts)
	}
}
