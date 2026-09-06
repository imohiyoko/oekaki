package views

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func rulesFrom(t *testing.T, body string) *Rules {
	t.Helper()
	doc, err := ParseRules([]byte(body), "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	return doc
}

func measured(g *core.Graph, subject, metric string, value float64, at string) {
	v := value
	g.Observations = append(g.Observations, core.Observation{
		Subject: subject, Metric: metric, Value: &v, ObservedAt: at,
	})
}

// The estate the rules are about: a gateway calling a checkout that calls a
// ledger — walked — and a reporting job that writes to an archive, which
// nothing has ever walked.
//
// The second route starts somewhere else on purpose. A declared route whose
// beginning something *did* walk is not unused but partial, which is a
// different finding, and a fixture that cannot tell the two apart would be
// testing neither.
func watched() *core.Graph {
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
		{Nodes: []string{"gateway", "checkout", "ledger"}, Kind: core.EdgeObserved},
		{Nodes: []string{"reports", "archive"}, Kind: core.EdgeIACRef},
	}
	g.Normalize()
	return g
}

func firing(t *testing.T, g *core.Graph, doc *Rules) map[string]Alert {
	t.Helper()
	alerts, _, err := Alerts(g, doc)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Alert{}
	for _, a := range alerts {
		out[a.Rule+"|"+a.Subject] = a
	}
	return out
}

// The spike: a bound on the newest reading, and only the newest — a service
// that was over its limit last week and is not now is not something to wake
// somebody for.
func TestABoundIsAboutTheNewestReading(t *testing.T) {
	g := watched()
	measured(g, "checkout", "request_rate", 900, "2026-09-01T00:00:00Z")
	measured(g, "checkout", "request_rate", 12, "2026-09-05T00:00:00Z")
	measured(g, "ledger", "request_rate", 4000, "2026-09-05T00:00:00Z")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"too busy","severity":"page","when":{"is":"above","metric":"request_rate","value":1000}}]}`)

	got := firing(t, g, doc)
	if _, fired := got["too busy|checkout"]; fired {
		t.Error("a service that was over its limit last week fired on this week's reading")
	}
	alert, fired := got["too busy|ledger"]
	if !fired {
		t.Fatal("the one over its limit did not fire")
	}
	if alert.Severity != "page" || !strings.Contains(alert.Reason, "4000") {
		t.Fatalf("the alert does not say what happened: %#v", alert)
	}
}

// The silence a bound reads as healthy, because no number arrived at all.
func TestQuietFindsWhatNeverReported(t *testing.T) {
	g := watched()
	measured(g, "checkout", "request_rate", 12, "2026-09-05T00:00:00Z")
	measured(g, "ledger", "request_rate", 3, "2026-01-01T00:00:00Z")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"stopped reporting","about":{"type":"service"},
		 "when":{"is":"quiet","metric":"request_rate","since":"2026-08-01T00:00:00Z"}}]}`)

	got := firing(t, g, doc)
	if _, fired := got["stopped reporting|checkout"]; fired {
		t.Error("a service that reported last week was called quiet")
	}
	if a, fired := got["stopped reporting|ledger"]; !fired || a.LastSeen != "2026-01-01T00:00:00Z" {
		t.Fatalf("a service that stopped reporting in January: %#v", a)
	}
	// And one that never reported at all, which is the case a bound cannot
	// see: nothing arrived, so nothing was compared.
	if a, fired := got["stopped reporting|gateway"]; !fired || a.LastSeen != "" {
		t.Fatalf("a service that has never reported: %#v", a)
	}
}

// A rule about routes needs no metric and no baseline: the finding is the
// comparison, and it is the same comparison the listing makes.
func TestARuleCanBeAboutRoutes(t *testing.T) {
	g := watched()
	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"nothing walks this","severity":"cleanup","when":{"is":"unused"}}]}`)

	got := firing(t, g, doc)
	want := core.PathKey([]string{"reports", "archive"})
	if a, fired := got["nothing walks this|"+want]; !fired || a.Label == "" {
		t.Fatalf("the route nothing walks did not fire: %#v", got)
	}
	if _, fired := got["nothing walks this|"+core.PathKey([]string{"gateway", "checkout", "ledger"})]; fired {
		t.Error("a route something walked was reported as unused")
	}
}

// A rule may be about the routes through one thing, which is how somebody says
// "the payment ones" without listing them.
func TestARuleCanBeAboutWhatPassesThroughSomething(t *testing.T) {
	g := watched()
	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"through the ledger","when":{"is":"unused"},"about":{"through":"ledger"}}]}`)

	if got := firing(t, g, doc); len(got) != 0 {
		t.Fatalf("nothing through the ledger is unused, yet: %#v", got)
	}

	doc = rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"through the archive","when":{"is":"unused"},"about":{"through":"archive"}}]}`)
	if got := firing(t, g, doc); len(got) != 1 {
		t.Fatalf("the unused route through the archive: %#v", got)
	}
}

// A schema says which fields exist. Which of them each condition needs is
// knowledge this package has, and a rule that cannot mean anything should be
// refused where it is written rather than quietly never firing.
func TestARuleThatCannotMeanAnythingIsRefused(t *testing.T) {
	for _, body := range []string{
		`{"kind":"oekaki.rules","version":"0.1","rules":[{"name":"x","when":{"is":"above","metric":"m"}}]}`,
		`{"kind":"oekaki.rules","version":"0.1","rules":[{"name":"x","when":{"is":"above","value":1}}]}`,
		`{"kind":"oekaki.rules","version":"0.1","rules":[{"name":"x","when":{"is":"quiet","metric":"m"}}]}`,
		`{"kind":"oekaki.rules","version":"0.1","rules":[{"name":"x","when":{"is":"unused","metric":"m"}}]}`,
		`{"kind":"oekaki.rules","version":"0.1","rules":[{"name":"x","when":{"is":"sideways"}}]}`,
		`{"kind":"oekaki.roles","version":"0.1","rules":[{"name":"x","when":{"is":"unused"}}]}`,
	} {
		if _, err := ParseRules([]byte(body), ""); err == nil {
			t.Errorf("accepted a rule that cannot mean anything: %s", body)
		}
	}
}

// The order is the document's own: a person reading a list wants their most
// important rule at the top, and they said which that was by writing it first.
func TestTheOrderIsTheOneSomebodyWroteAndIsStable(t *testing.T) {
	g := watched()
	measured(g, "ledger", "request_rate", 4000, "2026-09-05T00:00:00Z")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"first","when":{"is":"unused"}},
		{"name":"second","when":{"is":"above","metric":"request_rate","value":10}}]}`)

	first, _, err := Alerts(g, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 2 || first[0].Rule != "first" {
		t.Fatalf("the rule written first is not at the top: %#v", first)
	}
	for range 5 {
		again, _, err := Alerts(g, doc)
		if err != nil {
			t.Fatal(err)
		}
		for i := range first {
			if again[i].Rule != first[i].Rule || again[i].Subject != first[i].Subject {
				t.Fatalf("alert %d moved between runs", i)
			}
		}
	}
}
