package overlay

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

// serving is a graph with both halves of the join in it: code something read,
// and an operation a document declared. The shared fixture has neither, and
// this claim is about nothing else.
func serving() *core.Graph {
	g := &core.Graph{
		Version: core.Version,
		Axes:    []core.Axis{{ID: core.AxisNetwork}, {ID: "api", Label: "API"}},
		Groups: []core.Group{
			{ID: "api:checkout", Type: "api_surface", Label: "Checkout", Axis: "api"},
		},
		Nodes: []core.Node{
			{ID: "service/shop/checkout", Type: "kubernetes_service", Name: "checkout"},
			{ID: "file:handler/http.go#HandleOrder", Type: "code_function", Name: "HandleOrder",
				Attrs: map[string]any{"repository": "/src"}},
			{ID: "file:handler/http.go", Type: "code_file", Name: "handler/http.go",
				Attrs: map[string]any{"repository": "/src"}},
			{ID: "api/checkout/get/orders/{id}", Type: "api", Name: "GET /orders/{id}",
				Groups: map[string]string{"api": "api:checkout"},
				Attrs:  map[string]any{"method": "GET", "path": "/orders/{id}"}},
		},
	}
	g.Normalize()
	return g
}

func serve(t *testing.T, body string, opts Options) (*core.Graph, *enrichers.Report) {
	t.Helper()

	doc, err := Parse([]byte(body), "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	g := serving()
	r, err := New([]*Document{doc}, opts).Enrich(g)
	if err != nil && opts.Unmatched != PolicyError {
		t.Fatalf("Enrich: %v", err)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("the enriched graph does not validate: %v", err)
	}
	return g, r
}

func servesEdge(g *core.Graph) (core.Edge, bool) {
	for _, e := range g.Edges {
		if e.Relation == "serves" {
			return e, true
		}
	}
	return core.Edge{}, false
}

func TestWhatAFunctionServesIsWrittenDownAsAnEdge(t *testing.T) {
	g, report := serve(t, doc(`
	  {"assert":"serves",
	   "subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`), Options{})

	if report.Applied != 1 {
		t.Fatalf("applied %d assertions, report %+v", report.Applied, report)
	}
	e, ok := servesEdge(g)
	if !ok {
		t.Fatalf("no serves edge in %+v", g.Edges)
	}
	if e.From != "file:handler/http.go#HandleOrder" || e.To != "api/checkout/get/orders/{id}" {
		t.Fatalf("the edge runs %s -> %s", e.From, e.To)
	}
	// A claim about what is meant to happen, not about what anything watched.
	if e.Kind != core.EdgeIACRef {
		t.Fatalf("the edge is %q", e.Kind)
	}
	if e.Claim == nil || e.Claim.Author != "operator" || e.Claim.Origin != core.OriginHuman {
		t.Fatalf("the edge carries %+v", e.Claim)
	}
}

func TestAServesClaimNamesTheEndItGotWrong(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		mention string
	}{
		{
			name: "the subject is not a function",
			body: `{"assert":"serves",
			        "subject":{"node":"file:handler/http.go"},
			        "operation":{"node":"api/checkout/get/orders/{id}"}}`,
			mention: "code_function",
		},
		{
			name: "what it serves is not an operation",
			body: `{"assert":"serves",
			        "subject":{"node":"file:handler/http.go#HandleOrder"},
			        "operation":{"node":"service/shop/checkout"}}`,
			mention: "--api",
		},
		{
			name: "what it serves is the surface rather than an operation on it",
			body: `{"assert":"serves",
			        "subject":{"node":"file:handler/http.go#HandleOrder"},
			        "operation":{"group":"api:checkout"}}`,
			mention: "a surface holds operations",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, report := serve(t, doc(tc.body), Options{})
			if _, ok := servesEdge(g); ok {
				t.Fatalf("the edge was drawn anyway: %+v", g.Edges)
			}
			if report.Applied != 0 {
				t.Fatalf("applied %d", report.Applied)
			}
			if len(report.Unmatched) != 1 {
				t.Fatalf("reported %+v", report.Unmatched)
			}
			if got := report.Unmatched[0]; got.Action != "dropped" || !strings.Contains(got.Reason, tc.mention) {
				t.Fatalf("reported %+v, which does not mention %q", got, tc.mention)
			}
		})
	}
}

// The subject of a serves claim is never adopted, whatever the policy. A
// function nobody parsed is not code, and adopting one would put a box on the
// code map that no reading of the repository produced.
func TestAFunctionNobodyParsedIsNotInventedForTheClaim(t *testing.T) {
	g, report := serve(t, doc(`
	  {"assert":"serves",
	   "subject":{"node":"file:handler/http.go#HandleRefund"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`), Options{})

	if len(report.Adopted) != 0 {
		t.Fatalf("adopted %v", report.Adopted)
	}
	for _, n := range g.Nodes {
		if strings.Contains(n.ID, "HandleRefund") || n.Type == "oekaki_asserted" {
			t.Fatalf("a box was invented for it: %+v", n)
		}
	}
	if len(report.Unmatched) != 1 || report.Unmatched[0].Action != "dropped" {
		t.Fatalf("reported %+v", report.Unmatched)
	}
}

func TestAnAmbiguousEndIsNotGuessedAt(t *testing.T) {
	g, report := serve(t, doc(`
	  {"assert":"serves",
	   "subject":{"name":"HandleOrder"},
	   "operation":{"name":"GET /orders/{id}"},
	   "note":"the name is not unique once a second repository is read"}`), Options{})

	// The fixture's names are unique, so this one applies. The point of the
	// test is the pair below it, which shares a name with the first.
	if _, ok := servesEdge(g); !ok {
		t.Fatalf("the unambiguous form did not apply: %+v", report)
	}

	g2 := serving()
	g2.Nodes = append(g2.Nodes, core.Node{
		ID: "repo-2:file:api/http.go#HandleOrder", Type: "code_function", Name: "HandleOrder",
		Attrs: map[string]any{"repository": "/other"},
	})
	g2.Normalize()

	d, err := Parse([]byte(doc(`
	  {"assert":"serves",
	   "subject":{"name":"HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`)), "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	report2, err := New([]*Document{d}, Options{}).Enrich(g2)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if _, ok := servesEdge(g2); ok {
		t.Fatalf("one of the two was picked: %+v", g2.Edges)
	}
	if len(report2.Ambiguous) != 1 || len(report2.Ambiguous[0].Candidates) != 2 {
		t.Fatalf("reported %+v", report2.Ambiguous)
	}
}

func TestAServesClaimTakesNoEvidenceKind(t *testing.T) {
	_, err := Parse([]byte(doc(`
	  {"assert":"serves",
	   "subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"},
	   "kind":"observed"}`)), "test.json")
	if err == nil {
		t.Fatal("an evidence kind was accepted")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Fatalf("the refusal does not name the field: %v", err)
	}
}

func TestBothEndsAreRequired(t *testing.T) {
	for _, body := range []string{
		`{"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"}}`,
		`{"assert":"serves","operation":{"node":"api/checkout/get/orders/{id}"}}`,
	} {
		if _, err := Parse([]byte(doc(body)), "test.json"); err == nil {
			t.Fatalf("accepted a one-ended claim: %s", body)
		}
	}
}

// The relation an assertion now carries must not have narrowed what an edge
// assertion can reach. Suppressing a call is exactly an edge assertion landing
// on a line that has a relation on it, and it says nothing about the relation.
func TestAnEdgeAssertionStillReachesALineWithARelationOnIt(t *testing.T) {
	g := serving()
	g.Edges = append(g.Edges, core.Edge{
		From: "file:handler/http.go#HandleOrder", To: "file:handler/http.go",
		Kind: core.EdgeIACRef, Relation: "calls",
	})
	g.Normalize()

	d, err := Parse([]byte(doc(`
	  {"assert":"edge.suppress",
	   "from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"file:handler/http.go"},
	   "kind":"iac_ref"}`)), "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := New([]*Document{d}, Options{}).Enrich(g); err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	var found int
	for _, e := range g.Edges {
		if e.Relation != "calls" {
			continue
		}
		found++
		if !e.Suppressed {
			t.Fatalf("the call was not suppressed: %+v", e)
		}
	}
	if found != 1 {
		t.Fatalf("the assertion made a second edge beside the call: %+v", g.Edges)
	}
}

// A denial and the claim it denies are one line, however the two were
// ordered — inside one document or across two --overlay files.
//
// They are not the same shape of sentence: edge.suppress names two ends and a
// kind, serves names a relation as well. Reaching an unnamed line with a named
// claim is what keeps them together when the denial is written first and makes
// the line before the claim does.
func TestASuppressionAndTheClaimItDeniesAreOneLineEitherWayRound(t *testing.T) {
	denial := `
	  {"assert":"edge.suppress",
	   "from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"api/checkout/get/orders/{id}"},
	   "kind":"iac_ref"}`
	claim := `
	  {"assert":"serves",
	   "subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`

	settled := func(t *testing.T, g *core.Graph) {
		t.Helper()
		var lines []core.Edge
		for _, e := range g.Edges {
			if e.From == "file:handler/http.go#HandleOrder" {
				lines = append(lines, e)
			}
		}
		if len(lines) != 1 {
			t.Fatalf("the two sentences made %d lines: %+v", len(lines), lines)
		}
		if lines[0].Relation != "serves" {
			t.Errorf("the line is %q, so the map cannot tell what it means", lines[0].Relation)
		}
		if !lines[0].Suppressed {
			t.Error("the denial did not reach the claim, and a line somebody denied is drawn")
		}
		if len(g.Conflicts) != 1 {
			t.Errorf("%d conflicts recorded; the disagreement is invisible", len(g.Conflicts))
		}
	}

	t.Run("one document, either order", func(t *testing.T) {
		for _, body := range []string{denial + "," + claim, claim + "," + denial} {
			g, _ := serve(t, doc(body), Options{})
			settled(t, g)
		}
	})

	t.Run("two documents, either order", func(t *testing.T) {
		parse := func(body string) *Document {
			t.Helper()
			d, err := Parse([]byte(doc(body)), "test.json")
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			return d
		}
		for _, docs := range [][]*Document{
			{parse(denial), parse(claim)},
			{parse(claim), parse(denial)},
		} {
			g := serving()
			if _, err := New(docs, Options{}).Enrich(g); err != nil {
				t.Fatalf("Enrich: %v", err)
			}
			if err := g.Validate(); err != nil {
				t.Fatalf("the enriched graph does not validate: %v", err)
			}
			settled(t, g)
		}
	})
}

// A claim is about the line it names and not about another line between the
// same two boxes. Adopting an unnamed one is the exception, and it must not
// have widened into adopting any one.
func TestAClaimDoesNotTakeOverALineThatMeansSomethingElse(t *testing.T) {
	g := serving()
	g.Edges = append(g.Edges, core.Edge{
		From: "file:handler/http.go#HandleOrder", To: "api/checkout/get/orders/{id}",
		Kind: core.EdgeIACRef, Relation: "documents",
	})
	g.Normalize()

	d, err := Parse([]byte(doc(`
	  {"assert":"serves",
	   "subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`)), "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := New([]*Document{d}, Options{}).Enrich(g); err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	var documents, serves int
	for _, e := range g.Edges {
		switch e.Relation {
		case "documents":
			documents++
			if e.Claim != nil {
				t.Errorf("the claim was written onto a line about something else: %+v", e)
			}
		case "serves":
			serves++
		}
	}
	if documents != 1 || serves != 1 {
		t.Fatalf("%d documents lines and %d serves lines: %+v", documents, serves, g.Edges)
	}
}

// Three sentences about two boxes, and the graph still has to validate.
//
// A claim that renames a line renames what is filed under it. A conflict left
// pointing at the line's old name is a conflict about nothing, which core
// refuses — and the whole command then fails on a key, having written no
// output, because of an order nobody thought was significant.
func TestARenamedLineTakesItsConflictWithIt(t *testing.T) {
	g, _ := serve(t, doc(`
	  {"assert":"edge","from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref","author":"auditor"},
	  {"assert":"edge.suppress","from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref"},
	  {"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`), Options{})

	// serve validates; this says what the validation was about, so a failure
	// reads as the finding rather than as a key nobody can decode.
	for _, c := range g.Conflicts {
		if c.TargetKind != core.ConflictTargetEdge {
			continue
		}
		from, to, kind, relation, ok := core.ParseEdgeKey(c.Target)
		if !ok {
			t.Fatalf("conflict target %q is not an edge key", c.Target)
		}
		var found bool
		for _, e := range g.Edges {
			if e.From == from && e.To == to && e.Kind == kind && e.Relation == relation {
				found = true
			}
		}
		if !found {
			t.Errorf("a conflict is filed against a line that is not in the graph: relation %q", relation)
		}
	}
}

// A claim adopts a line nobody drew and everybody denied. It does not adopt a
// line somebody asserted: that sentence is theirs, and relabelling it would
// put this claim's meaning on their name.
func TestAClaimDoesNotRelabelSomebodyElsesAssertion(t *testing.T) {
	g, _ := serve(t, doc(`
	  {"assert":"edge","from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref",
	   "author":"auditor","note":"read off a service map"},
	  {"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`), Options{})

	var theirs, mine int
	for _, e := range g.Edges {
		if e.From != "file:handler/http.go#HandleOrder" {
			continue
		}
		switch e.Relation {
		case "":
			theirs++
			if e.Claim == nil || e.Claim.Author != "auditor" {
				t.Errorf("their line now carries %+v", e.Claim)
			}
		case "serves":
			mine++
		}
	}
	if theirs != 1 || mine != 1 {
		t.Fatalf("%d unnamed lines and %d serves lines: the claim took over the other one", theirs, mine)
	}
}

// Every pair of sentences about one line settles the same way whichever was
// written first. The claim and a denial become one denied line; the claim and
// somebody else's positive assertion stay two lines with their two names on
// them.
func TestTwoSentencesAboutOneLineSettleTheSameWayEitherOrder(t *testing.T) {
	claim := `{"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`
	positive := `{"assert":"edge","from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref","author":"auditor"}`
	denial := `{"assert":"edge.suppress","from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref"}`

	lines := func(t *testing.T, body string) map[string]core.Edge {
		t.Helper()
		g, _ := serve(t, doc(body), Options{})
		out := map[string]core.Edge{}
		for _, e := range g.Edges {
			if e.From == "file:handler/http.go#HandleOrder" {
				out[e.Relation] = e
			}
		}
		return out
	}

	t.Run("a claim and somebody else's assertion", func(t *testing.T) {
		for _, body := range []string{positive + "," + claim, claim + "," + positive} {
			got := lines(t, body)
			if len(got) != 2 {
				t.Fatalf("%d lines, not two: %+v", len(got), got)
			}
			if c := got["serves"].Claim; c == nil || c.Author != "operator" {
				t.Errorf("the claim carries %+v; somebody else's name is on it", c)
			}
			if c := got[""].Claim; c == nil || c.Author != "auditor" {
				t.Errorf("their assertion carries %+v", c)
			}
		}
	})

	t.Run("a claim and its denial", func(t *testing.T) {
		for _, body := range []string{denial + "," + claim, claim + "," + denial} {
			got := lines(t, body)
			if len(got) != 1 {
				t.Fatalf("%d lines, not one: %+v", len(got), got)
			}
			if e, ok := got["serves"]; !ok || !e.Suppressed {
				t.Errorf("the denial did not land on the claim: %+v", got)
			}
		}
	})
}

// A positive assertion with no relation makes its own line rather than taking
// over one that means something.
//
// The rule used to be "a line a parser drew may be claimed, a line an
// assertion made may not", which reads well and does not survive the graph
// being written out and read back in: on the second run every line was in the
// input, and a plain edge assertion would take the serves claim's name off it.
// An overlay has to mean the same thing against the plan and against the graph
// the last run wrote.
func TestAnAssertionWithNoRelationDoesNotClaimALineThatMeansSomething(t *testing.T) {
	g := serving()
	g.Edges = append(g.Edges, core.Edge{
		From: "file:handler/http.go", To: "file:handler/http.go#HandleOrder",
		Kind: core.EdgeIACRef, Relation: "contains",
	})
	g.Normalize()

	d, err := Parse([]byte(doc(`
	  {"assert":"edge","from":{"node":"file:handler/http.go"},
	   "to":{"node":"file:handler/http.go#HandleOrder"},"kind":"iac_ref",
	   "author":"auditor"}`)), "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := New([]*Document{d}, Options{}).Enrich(g); err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	var theirs, contains int
	for _, e := range g.Edges {
		if e.From != "file:handler/http.go" || e.To != "file:handler/http.go#HandleOrder" {
			continue
		}
		switch e.Relation {
		case "contains":
			contains++
			if e.Claim != nil {
				t.Errorf("the parser's line was claimed: %+v", e.Claim)
			}
		case "":
			theirs++
			if e.Claim == nil || e.Claim.Author != "auditor" {
				t.Errorf("their line carries %+v", e.Claim)
			}
		}
	}
	if contains != 1 || theirs != 1 {
		t.Fatalf("%d contains lines and %d asserted lines", contains, theirs)
	}
}

// A denial that names no relation is about every line between those two ends,
// not about whichever one happens to sort first. The graph is normalized, so
// the unnamed line always sorts ahead of the claim — a denial that stopped
// there would never be able to deny a serves claim at all once anything else
// had been asserted between the same boxes.
func TestADenialWithNoRelationReachesEveryLine(t *testing.T) {
	g, _ := serve(t, doc(`
	  {"assert":"edge","from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref","author":"auditor"},
	  {"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}},
	  {"assert":"edge.suppress","from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref"}`), Options{})

	var lines int
	for _, e := range g.Edges {
		if e.From != "file:handler/http.go#HandleOrder" {
			continue
		}
		lines++
		if !e.Suppressed {
			t.Errorf("the denial did not reach the %q line", e.Relation)
		}
	}
	if lines != 2 {
		t.Fatalf("%d lines, so the test is not asking what it means to", lines)
	}
}

// A graph that spells the relation differently is still one line. The enricher
// folds case because views does, and a second line here is a doubled arrow
// there with the denial reaching only one of them.
func TestARelationIsFoldedTheWayTheDrawingFoldsIt(t *testing.T) {
	g := serving()
	g.Edges = append(g.Edges, core.Edge{
		From: "file:handler/http.go#HandleOrder", To: "api/checkout/get/orders/{id}",
		Kind: core.EdgeIACRef, Relation: "Serves",
	})
	g.Normalize()

	d, err := Parse([]byte(doc(`
	  {"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`)), "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := New([]*Document{d}, Options{}).Enrich(g); err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	var lines []string
	for _, e := range g.Edges {
		if e.From == "file:handler/http.go#HandleOrder" {
			lines = append(lines, e.Relation)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("the graph's spelling made a second line: %v", lines)
	}
	// And the graph's own spelling is left alone: it is their document.
	if lines[0] != "Serves" {
		t.Errorf("the line was respelled %q", lines[0])
	}
}

// A denial of a line the same run made does not say nothing made it.
func TestADeniedClaimIsNotToldItWasNeverThere(t *testing.T) {
	for _, body := range []string{
		`{"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"},
		  "operation":{"node":"api/checkout/get/orders/{id}"}},
		 {"assert":"edge.suppress","from":{"node":"file:handler/http.go#HandleOrder"},
		  "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref"}`,
		`{"assert":"edge.suppress","from":{"node":"file:handler/http.go#HandleOrder"},
		  "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref"},
		 {"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"},
		  "operation":{"node":"api/checkout/get/orders/{id}"}}`,
	} {
		g, _ := serve(t, doc(body), Options{})
		for _, e := range g.Edges {
			if e.Relation != "serves" || e.Claim == nil {
				continue
			}
			if strings.Contains(e.Claim.Note, "no such edge was found") {
				t.Errorf("the line says nothing made it: %q", e.Claim.Note)
			}
		}
		// And where the two sides are written down. Taking the sentence off
		// the line and leaving it in the conflict only moves where it is read.
		var recorded int
		for _, c := range g.Conflicts {
			for _, value := range c.Claims {
				recorded++
				if strings.Contains(value.Claim.Note, "no such edge was found") {
					t.Errorf("the disagreement says nothing made the line: %q", value.Claim.Note)
				}
			}
		}
		if recorded == 0 {
			t.Error("no conflict was recorded, so this is not asking what it means to")
		}
	}
}

// The documented way of working is two runs: write the graph out, then apply
// an overlay to the file. A denial the first run applied is in that file as a
// line nothing drew, and the claim in the second run has to be that same line
// — otherwise the denial sits on a phantom and the claim is drawn beside it,
// undenied, which is the picture the author wrote the denial to prevent.
func TestAClaimAdoptsThePhantomAnEarlierRunWroteOut(t *testing.T) {
	g := serving()
	g.Edges = append(g.Edges, core.Edge{
		From: "file:handler/http.go#HandleOrder", To: "api/checkout/get/orders/{id}",
		Kind: core.EdgeIACRef, Suppressed: true,
		Claim: &core.Claim{Origin: core.OriginHuman, Author: "operator",
			Note: "asserted not to exist; no such edge was found"},
	})
	g.Normalize()

	d, err := Parse([]byte(doc(`
	  {"assert":"edge.suppress","from":{"node":"file:handler/http.go#HandleOrder"},
	   "to":{"node":"api/checkout/get/orders/{id}"},"kind":"iac_ref"},
	  {"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`)), "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := New([]*Document{d}, Options{}).Enrich(g); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("the enriched graph does not validate: %v", err)
	}

	var lines []core.Edge
	for _, e := range g.Edges {
		if e.From == "file:handler/http.go#HandleOrder" {
			lines = append(lines, e)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("the second run made %d lines where one run makes 1: %+v", len(lines), lines)
	}
	if lines[0].Relation != "serves" || !lines[0].Suppressed {
		t.Errorf("the line is %q, suppressed=%v", lines[0].Relation, lines[0].Suppressed)
	}
}

// And it adopts only a phantom. A line the input graph draws without denying
// it is somebody's, whoever they were, and a claim that renamed it would be
// putting its meaning on their sentence — the same rule within a run and
// across two, which is the point of not asking where the line came from.
func TestAClaimDoesNotAdoptALineTheGraphDraws(t *testing.T) {
	g := serving()
	g.Edges = append(g.Edges, core.Edge{
		From: "file:handler/http.go#HandleOrder", To: "api/checkout/get/orders/{id}",
		Kind: core.EdgeIACRef,
		Claim: &core.Claim{Origin: core.OriginHuman, Author: "auditor",
			Note: "read off a service map"},
	})
	g.Normalize()

	d, err := Parse([]byte(doc(`
	  {"assert":"serves","subject":{"node":"file:handler/http.go#HandleOrder"},
	   "operation":{"node":"api/checkout/get/orders/{id}"}}`)), "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := New([]*Document{d}, Options{}).Enrich(g); err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	var theirs, mine int
	for _, e := range g.Edges {
		if e.From != "file:handler/http.go#HandleOrder" {
			continue
		}
		switch e.Relation {
		case "":
			theirs++
			if e.Claim == nil || e.Claim.Author != "auditor" {
				t.Errorf("their line now carries %+v", e.Claim)
			}
		case "serves":
			mine++
		}
	}
	if theirs != 1 || mine != 1 {
		t.Fatalf("%d unnamed lines and %d serves lines", theirs, mine)
	}
}
