package views

import (
	"strings"
	"testing"
)

// The moment arrives with the document. A rule that asks what has gone quiet
// cannot be judged without one, so filling it in afterwards meant the flag
// could never reach the one condition that needs it: the document was refused
// before the flag was applied.
func TestTheMomentGivenToTheRunReachesTheRuleThatNeedsIt(t *testing.T) {
	body := `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"stopped","about":{"kind":"path"},"when":{"is":"quiet","metric":"path_requests"}}]}`

	if _, err := ParseRules([]byte(body), ""); err == nil {
		t.Error("a quiet rule with no moment anywhere was accepted")
	}
	doc, err := ParseRules([]byte(body), "2026-08-01T00:00:00Z")
	if err != nil {
		t.Fatalf("the moment the run was given did not reach the rule: %v", err)
	}
	if doc.Rules[0].When.Since != "2026-08-01T00:00:00Z" {
		t.Fatalf("the rule is asking about %q", doc.Rules[0].When.Since)
	}
}

// A rule that named its own moment keeps it: a document that says "since the
// first of August" means it whoever runs it and whenever.
func TestARuleKeepsTheMomentItNamed(t *testing.T) {
	body := `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"stopped","when":{"is":"quiet","metric":"m","since":"2026-01-01T00:00:00Z"}}]}`

	doc, err := ParseRules([]byte(body), "2026-08-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Rules[0].When.Since != "2026-01-01T00:00:00Z" {
		t.Fatalf("the run's moment overwrote the rule's own: %q", doc.Rules[0].When.Since)
	}
}

// A document has no clock, so a moment written into one is a moment and not a
// span. Left unchecked, "30d" reaches a comparison that falls back to comparing
// the text — where every timestamp sorts before "d", so every subject fires.
func TestASpanInADocumentIsRefused(t *testing.T) {
	body := `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"stopped","when":{"is":"quiet","metric":"m","since":"30d"}}]}`

	_, err := ParseRules([]byte(body), "")
	if err == nil {
		t.Fatal("a span was accepted where a moment belongs")
	}
	if !strings.Contains(err.Error(), "command line") {
		t.Errorf("the error does not say where a span belongs: %v", err)
	}
}

// A window is about readings that say when they were taken. One that does not
// is not old — it is undated, and dropping it silently takes every reading from
// a collector that records no time out of every bound.
func TestAnUndatedReadingIsNotOld(t *testing.T) {
	g := watched()
	measured(g, "ledger", "request_rate", 4000, "")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"too busy","when":{"is":"above","metric":"request_rate","value":1000,"since":"2026-08-01T00:00:00Z"}}]}`)

	if _, fired := firing(t, g, doc)["too busy|ledger"]; !fired {
		t.Fatal("a reading with no time on it was dropped from a rule with a window")
	}
}

// A caller reading the JSON should not have to tell "nothing fired" from "this
// field is missing".
func TestNothingFiringIsAnEmptyList(t *testing.T) {
	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"quiet estate","when":{"is":"above","metric":"nothing_measures_this","value":1}}]}`)

	alerts, _, err := Alerts(watched(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if alerts == nil {
		t.Fatal("an empty result is absent rather than empty")
	}
}

// The other half of "an undated reading is not old", which the window filter
// got and the silence check did not.
//
// Quiet is the claim that nothing arrived. A reading with no time on it says
// something arrived and says nothing about when, so it is not silence and it
// cannot be placed inside the window either. Treating it as older than every
// moment — right everywhere else — made every subject of a collector that
// records no time fire, every run, which is the exact collector this whole
// change was about.
func TestAnUndatedReadingIsNotSilence(t *testing.T) {
	g := watched()
	measured(g, "ledger", "beat", 5, "")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"stopped","about":{"subject":"ledger"},
		 "when":{"is":"quiet","metric":"beat","since":"2026-08-01T00:00:00Z"}}]}`)

	alerts, unanswered, err := Alerts(g, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 0 {
		t.Fatalf("a reading with no time on it was read as silence: %#v", alerts)
	}
	// And it is not a silent pass either: a rule that could not answer the
	// question says so, or an operator reads "nothing fired" as "all well".
	if len(unanswered) != 1 || !strings.Contains(unanswered[0], "ledger") {
		t.Fatalf("the rule answered nothing and said nothing: %#v", unanswered)
	}
}

// A subject whose readings are undated is undecidable; one that was measured
// and stopped is quiet. Both can be true in the same run, and the undated one
// must not take the other down with it.
func TestSilenceIsStillFoundBesideAnUndatedReading(t *testing.T) {
	g := watched()
	measured(g, "ledger", "beat", 5, "")
	measured(g, "checkout", "beat", 5, "2026-01-01T00:00:00Z")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"stopped","about":{"subject":"checkout"},
		 "when":{"is":"quiet","metric":"beat","since":"2026-08-01T00:00:00Z"}}]}`)

	fired := firing(t, g, doc)
	at, ok := fired["stopped|checkout"]
	if !ok {
		t.Fatalf("the subject that went quiet did not fire: %#v", fired)
	}
	// The moment is in the reason, so the caller has it without the value
	// having to be restated beside it.
	if !strings.Contains(at.Reason, "2026-01-01T00:00:00Z") {
		t.Errorf("the reason does not say when it was last measured: %q", at.Reason)
	}
	if at.Metric != "beat" {
		t.Errorf("the alert does not say what was measured: %q", at.Metric)
	}
}
