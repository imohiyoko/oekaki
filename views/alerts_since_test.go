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

// "Twice what it usually is" needs a usual, and nothing here computes one. The
// baseline arrives the way every other outside fact does — a collector works it
// out and writes it back as an ordinary observation — and the rule says which
// reading that is.
func TestABoundCanBeAnotherReading(t *testing.T) {
	g := watched()
	measured(g, "checkout", "request_rate", 4000, "2026-09-09T10:00:00Z")
	measured(g, "checkout", "request_rate_avg", 900, "2026-09-09T10:00:00Z")
	measured(g, "ledger", "request_rate", 1200, "2026-09-09T10:00:00Z")
	measured(g, "ledger", "request_rate_avg", 1100, "2026-09-09T10:00:00Z")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"spike","when":{"is":"above","metric":"request_rate",
		 "than":{"metric":"request_rate_avg","times":2}}}]}`)

	fired := firing(t, g, doc)
	at, ok := fired["spike|checkout"]
	if !ok {
		t.Fatalf("four thousand against a usual of nine hundred did not fire: %#v", fired)
	}
	// The sentence says what the bound was made of: "above 3000" and "above
	// twice the usual, which was 900" are different things to be told at three
	// in the morning.
	for _, want := range []string{"request_rate is 4000", "2× request_rate_avg", "900"} {
		if !strings.Contains(at.Reason, want) {
			t.Errorf("the reason does not carry %q: %q", want, at.Reason)
		}
	}
	// And a subject inside its own usual is not a spike, whatever the number.
	if _, fired := fired["spike|ledger"]; fired {
		t.Error("twelve hundred against a usual of eleven hundred fired")
	}
}

// A factor of one is the plain case: the collector wrote the threshold itself,
// and the rule only says which reading it is.
func TestABaselineNeedsNoFactor(t *testing.T) {
	g := watched()
	measured(g, "checkout", "request_rate", 4000, "2026-09-09T10:00:00Z")
	measured(g, "checkout", "request_rate_upper", 3000, "2026-09-09T10:00:00Z")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"spike","when":{"is":"above","metric":"request_rate",
		 "than":{"metric":"request_rate_upper"}}}]}`)

	at, ok := firing(t, g, doc)["spike|checkout"]
	if !ok {
		t.Fatal("a reading over the threshold somebody measured did not fire")
	}
	if strings.Contains(at.Reason, "×") {
		t.Errorf("a factor of one is written out: %q", at.Reason)
	}
	if !strings.Contains(at.Reason, "request_rate_upper (3000)") {
		t.Errorf("the reason does not say what the bound was: %q", at.Reason)
	}
}

// A reading with no baseline is a reading nothing can judge. Firing would be a
// comparison against nothing; passing quietly would say it was fine.
func TestAReadingWithNoBaselineIsNotJudged(t *testing.T) {
	g := watched()
	measured(g, "checkout", "request_rate", 4000, "2026-09-09T10:00:00Z")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"spike","when":{"is":"above","metric":"request_rate",
		 "than":{"metric":"request_rate_avg","times":2}}}]}`)

	alerts, unanswered, err := Alerts(g, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 0 {
		t.Fatalf("a reading was judged against a baseline nobody measured: %#v", alerts)
	}
	if len(unanswered) != 1 || !strings.Contains(unanswered[0], "checkout") {
		t.Fatalf("the rule judged nothing and said nothing: %#v", unanswered)
	}
	if !strings.Contains(unanswered[0], "request_rate_avg") {
		t.Errorf("the notice does not say what was missing: %q", unanswered[0])
	}
}

// The baseline is read through the same window as the reading it bounds. A
// usual from another era is not the usual.
func TestTheBaselineIsReadThroughTheSameWindow(t *testing.T) {
	g := watched()
	measured(g, "checkout", "request_rate", 4000, "2026-09-09T10:00:00Z")
	measured(g, "checkout", "request_rate_avg", 900, "2020-01-01T00:00:00Z")
	g.Normalize()

	doc := rulesFrom(t, `{"kind":"oekaki.rules","version":"0.1","rules":[
		{"name":"spike","when":{"is":"above","metric":"request_rate",
		 "than":{"metric":"request_rate_avg","times":2},
		 "since":"2026-09-01T00:00:00Z"}}]}`)

	alerts, unanswered, err := Alerts(g, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 0 {
		t.Fatalf("a reading was judged against a usual from six years ago: %#v", alerts)
	}
	if len(unanswered) != 1 {
		t.Fatalf("the rule judged nothing and said nothing: %#v", unanswered)
	}
}

// A rule that names two bounds does not say which it meant, and one that names
// itself as its own baseline can never be true.
func TestARuleThatCannotMeanABoundIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"both": `{"name":"x","when":{"is":"above","metric":"m","value":1,
			"than":{"metric":"m_avg"}}}`,
		"itself": `{"name":"x","when":{"is":"above","metric":"m",
			"than":{"metric":"m"}}}`,
		"no factor": `{"name":"x","when":{"is":"above","metric":"m",
			"than":{"metric":"m_avg","times":0}}}`,
		"neither": `{"name":"x","when":{"is":"above","metric":"m"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRules([]byte(`{"kind":"oekaki.rules","version":"0.1","rules":[`+body+`]}`), "")
			if err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
