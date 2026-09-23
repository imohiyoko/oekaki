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
