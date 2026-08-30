package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// Three documents that deliberately interfere with each other: one asserts an
// edge, one says an edge the parser found is not real, one describes the same
// subject's logging from a different angle.
func interferingDocs(t *testing.T) []*Document {
	t.Helper()

	bodies := []string{
		`{"kind":"oekaki.overlay","version":"0.1",
		  "metadata":{"origin":"human","author":"operator","window":"last-7d"},
		  "sinks":[{"id":"s","type":"log_group","name":"/platform/app"}],
		  "assertions":[
		    {"assert":"logs.declared","subject":{"service":"api"},"sink":"s","via":"task definition"},
		    {"assert":"edge.suppress","from":{"node":"aws_ecs_service.api"},
		     "to":{"node":"aws_db_instance.orders"},"kind":"iac_ref"}]}`,

		`{"kind":"oekaki.overlay","version":"0.1",
		  "metadata":{"origin":"ai","author":"assistant"},
		  "sinks":[{"id":"s","type":"log_group","name":"/platform/app"}],
		  "assertions":[
		    {"assert":"logs.observed","subject":{"service":"api"},"sink":"s","records":12},
		    {"assert":"edge","from":{"service":"checkout"},"to":{"node":"aws_db_instance.orders"},
		     "kind":"observed","confidence":0.5}]}`,

		`{"kind":"oekaki.overlay","version":"0.1",
		  "metadata":{"origin":"human","author":"reviewer"},
		  "assertions":[
		    {"assert":"logs.none","subject":{"service":"ghost"},"via":"checked the console"},
		    {"assert":"node","subject":{"node":"aws_lb.api"},"type":"aws_lb","name":"api-public"}]}`,
	}

	docs := make([]*Document, 0, len(bodies))
	for i, body := range bodies {
		d, err := Parse([]byte(body), fmt.Sprintf("overlay-%d.json", i))
		if err != nil {
			t.Fatalf("Parse %d: %v", i, err)
		}
		docs = append(docs, d)
	}
	return docs
}

func permutations(in []*Document) [][]*Document {
	if len(in) <= 1 {
		return [][]*Document{append([]*Document(nil), in...)}
	}
	var out [][]*Document
	for i := range in {
		rest := make([]*Document, 0, len(in)-1)
		rest = append(rest, in[:i]...)
		rest = append(rest, in[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]*Document{in[i]}, p...))
		}
	}
	return out
}

// The determinism test for the merge rules, and the one that would catch a
// lazily written conflict resolver.
//
// Which overlay was named first on a command line is not a fact about the
// infrastructure, so it must not reach the output. Everything that decides an
// outcome here — which claim is displayed, which edges survive, how a state is
// settled — is defined to be order-free, and this holds it to that rather than
// trusting it.
func TestOverlayOrderDoesNotChangeOutput(t *testing.T) {
	perms := permutations(interferingDocs(t))
	if len(perms) != 6 {
		t.Fatalf("got %d permutations, want 6", len(perms))
	}

	var first string
	for i, docs := range perms {
		g := graph()
		if _, err := New(docs, Options{}).Enrich(g); err != nil {
			t.Fatalf("permutation %d: %v", i, err)
		}
		out, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(out)
			continue
		}
		if string(out) != first {
			t.Fatalf("permutation %d produced a different graph:\n%s", i, firstDifference(first, string(out)))
		}
	}
}

// The same has to hold for the report, which is written to a file CI diffs. A
// report that reordered itself would fail a build for a reason nobody could
// act on.
func TestReportDoesNotDependOnOverlayOrder(t *testing.T) {
	var first string
	for i, docs := range permutations(interferingDocs(t)) {
		r, err := New(docs, Options{}).Enrich(graph())
		if err != nil {
			t.Fatal(err)
		}
		// Sources legitimately records the order it was given; everything else
		// in the report is a finding rather than a command line.
		r.Sources = nil

		out, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(out)
			continue
		}
		if string(out) != first {
			t.Fatalf("permutation %d produced a different report:\n%s", i, firstDifference(first, string(out)))
		}
	}
}

func TestEqualRankNodeAndEdgeClaimsDoNotDependOnOverlayOrder(t *testing.T) {
	bodies := []struct {
		source string
		body   string
	}{
		{
			source: "alice.json",
			body:   `{"kind":"oekaki.overlay","version":"0.1","metadata":{"origin":"human","author":"alice"},"assertions":[{"assert":"node","subject":{"node":"aws_lb.api"},"type":"aws_lb","name":"alpha"},{"assert":"edge","from":{"service":"checkout"},"to":{"node":"aws_db_instance.orders"},"kind":"observed"}]}`,
		},
		{
			source: "bob.json",
			body:   `{"kind":"oekaki.overlay","version":"0.1","metadata":{"origin":"human","author":"bob"},"assertions":[{"assert":"node","subject":{"node":"aws_lb.api"},"type":"aws_lb","name":"beta"},{"assert":"edge","from":{"service":"checkout"},"to":{"node":"aws_db_instance.orders"},"kind":"observed"}]}`,
		},
	}
	docs := make([]*Document, 0, len(bodies))
	for _, input := range bodies {
		doc, err := Parse([]byte(input.body), input.source)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, doc)
	}

	var first string
	for i, order := range [][]*Document{docs, {docs[1], docs[0]}} {
		g := graph()
		if _, err := New(order, Options{}).Enrich(g); err != nil {
			t.Fatal(err)
		}
		out, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(out)
		} else if string(out) != first {
			t.Fatalf("equal-rank overlay order changed output:\n%s", firstDifference(first, string(out)))
		}
	}
}

func TestNodeTypeAndNameClaimsAreResolvedIndependently(t *testing.T) {
	docs := []*Document{
		{
			Source: "alice.json", Metadata: &Metadata{Origin: core.OriginHuman, Author: "alice"},
			Assertions: []Assertion{{Assert: AssertNode, Subject: Selector{"node": "aws_lb.api"}, Name: "public-api"}},
		},
		{
			Source: "bob.json", Metadata: &Metadata{Origin: core.OriginHuman, Author: "bob"},
			Assertions: []Assertion{{Assert: AssertNode, Subject: Selector{"node": "aws_lb.api"}, Type: "custom_load_balancer"}},
		},
	}

	var first string
	for i, order := range [][]*Document{{docs[0], docs[1]}, {docs[1], docs[0]}} {
		g := graph()
		if _, err := New(order, Options{}).Enrich(g); err != nil {
			t.Fatal(err)
		}
		node, ok := g.Node("aws_lb.api")
		if !ok {
			t.Fatal("load balancer node disappeared")
		}
		if node.Name != "public-api" || node.Type != "custom_load_balancer" {
			t.Fatalf("order %d: independent fields were blocked: %#v", i, node)
		}
		fieldClaims := map[string][]core.ClaimedValue{}
		for _, conflict := range g.Conflicts {
			if conflict.Target == node.ID {
				fieldClaims[conflict.Field] = conflict.Claims
			}
		}
		if len(fieldClaims["name"]) != 2 || len(fieldClaims["type"]) != 2 {
			t.Fatalf("order %d: per-field histories were not retained: %#v", i, fieldClaims)
		}
		if fieldClaims["name"][0].Value != node.Name || fieldClaims["name"][0].Claim.Author != "alice" {
			t.Fatalf("order %d: displayed name disagrees with conflict winner: %#v", i, fieldClaims["name"])
		}
		if fieldClaims["type"][0].Value != node.Type || fieldClaims["type"][0].Claim.Author != "bob" {
			t.Fatalf("order %d: displayed type disagrees with conflict winner: %#v", i, fieldClaims["type"])
		}
		encoded, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(encoded)
		} else if string(encoded) != first {
			t.Fatalf("node field order changed exact bytes:\n%s", firstDifference(first, string(encoded)))
		}
	}
}

func TestConflictHistoriesKeepEntityAndEdgeNamespacesSeparate(t *testing.T) {
	target := core.EdgeKey("a", "b", core.EdgeIACRef)
	g := &core.Graph{}
	entity := conflictFor(g, core.ConflictTargetEntity, target, "name")
	appendClaimedValue(entity, core.ClaimedValue{Value: "entity", Claim: core.Claim{Origin: core.OriginHuman}})
	edge := conflictFor(g, core.ConflictTargetEdge, target, "name")
	appendClaimedValue(edge, core.ClaimedValue{Value: "edge", Claim: core.Claim{Origin: core.OriginParser}})

	if len(g.Conflicts) != 2 {
		t.Fatalf("same-spelled entity and edge histories merged: %#v", g.Conflicts)
	}
	if g.Conflicts[0].TargetKind != core.ConflictTargetEntity || g.Conflicts[1].TargetKind != core.ConflictTargetEdge {
		t.Fatalf("conflict target kinds were not preserved: %#v", g.Conflicts)
	}
}

func TestNodeFieldHistoryRetainsSameValueClaims(t *testing.T) {
	docs := []*Document{
		{
			Source: "alice.json", Metadata: &Metadata{Origin: core.OriginHuman, Author: "alice"},
			Assertions: []Assertion{{Assert: AssertNode, Subject: Selector{"node": "aws_lb.api"}, Type: "aws_lb"}},
		},
		{
			Source: "bob.json", Metadata: &Metadata{Origin: core.OriginHuman, Author: "bob"},
			Assertions: []Assertion{{Assert: AssertNode, Subject: Selector{"node": "aws_lb.api"}, Type: "custom_load_balancer"}},
		},
	}
	var first string
	for i, order := range [][]*Document{{docs[0], docs[1]}, {docs[1], docs[0]}} {
		g := graph()
		if _, err := New(order, Options{}).Enrich(g); err != nil {
			t.Fatal(err)
		}
		node, _ := g.Node("aws_lb.api")
		if node == nil || node.Type != "aws_lb" {
			t.Fatalf("order %d: field winner = %#v", i, node)
		}
		var claims []core.ClaimedValue
		for _, conflict := range g.Conflicts {
			if conflict.Target == "aws_lb.api" && conflict.Field == "type" {
				claims = conflict.Claims
			}
		}
		if len(claims) != 3 || claims[0].Claim.Author != "alice" || claims[0].Value != node.Type {
			t.Fatalf("order %d: same-value baseline history was lost: %#v", i, claims)
		}
		encoded, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(encoded)
		} else if string(encoded) != first {
			t.Fatalf("same-value field history changed exact bytes:\n%s", firstDifference(first, string(encoded)))
		}
	}
}

func TestEdgeSuppressionHistoryRetainsEveryClaimInEitherOrder(t *testing.T) {
	positive := edgeDocument(t, "human-positive.json", AssertEdge, core.OriginHuman, "operator", "api")
	suppression := edgeDocument(t, "ai-suppression.json", AssertEdgeSuppress, core.OriginAI, "assistant", "api")
	orders := [][]*Document{{positive, suppression}, {suppression, positive}}

	var first string
	for i, order := range orders {
		g := graph()
		if _, err := New(order, Options{}).Enrich(g); err != nil {
			t.Fatal(err)
		}
		var edge *core.Edge
		for j := range g.Edges {
			if g.Edges[j].From == "aws_ecs_service.api" && g.Edges[j].To == "aws_db_instance.orders" && g.Edges[j].Kind == core.EdgeIACRef {
				edge = &g.Edges[j]
				break
			}
		}
		if edge == nil || !edge.Suppressed || edge.Claim == nil || edge.Claim.Origin != core.OriginAI {
			t.Fatalf("order %d: fail-safe suppression is inconsistent: %#v", i, edge)
		}

		var conflict *core.Conflict
		for j := range g.Conflicts {
			if g.Conflicts[j].Target == core.EdgeKey(edge.From, edge.To, edge.Kind, edge.Relation) && g.Conflicts[j].Field == "suppressed" {
				conflict = &g.Conflicts[j]
				break
			}
		}
		if conflict == nil || len(conflict.Claims) != 3 {
			t.Fatalf("order %d: complete three-claim history was not retained: %#v", i, conflict)
		}
		got := map[string]string{}
		for _, claimed := range conflict.Claims {
			got[string(claimed.Claim.Origin)+"/"+claimed.Claim.Author] = claimed.Value
		}
		want := map[string]string{"parser/": "false", "human/operator": "false", "ai/assistant": "true"}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("order %d: conflict claims = %v, want %v", i, got, want)
		}

		encoded, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(encoded)
		} else if string(encoded) != first {
			t.Fatalf("edge history order changed exact bytes:\n%s", firstDifference(first, string(encoded)))
		}
	}
}

func edgeDocument(t *testing.T, source, assertion string, origin core.Origin, author, service string) *Document {
	t.Helper()
	body := fmt.Sprintf(`{"kind":"oekaki.overlay","version":"0.1","metadata":{"origin":%q,"author":%q},"assertions":[{"assert":%q,"from":{"service":%q},"to":{"node":"aws_db_instance.orders"},"kind":"iac_ref"}]}`, origin, author, assertion, service)
	doc, err := Parse([]byte(body), source)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestSinkHandlesAreScopedToTheirDocument(t *testing.T) {
	inputs := []struct {
		source, subject, sinkName string
	}{
		{source: "team-a.json", subject: "api", sinkName: "/logs/team-a"},
		{source: "team-b.json", subject: "checkout", sinkName: "/logs/team-b"},
	}
	var docs []*Document
	for _, input := range inputs {
		body := fmt.Sprintf(`{"kind":"oekaki.overlay","version":"0.1","metadata":{"origin":"human"},"sinks":[{"id":"s","type":"log_group","name":%q}],"assertions":[{"assert":"logs.declared","subject":{"service":%q},"sink":"s"}]}`, input.sinkName, input.subject)
		doc, err := Parse([]byte(body), input.source)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, doc)
	}
	g := graph()
	if _, err := New(docs, Options{}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	sinks := map[string]string{}
	for _, node := range g.Nodes {
		if node.Type == "oekaki_log_sink" {
			sinks[node.Name] = node.ID
		}
	}
	if len(sinks) != 2 || sinks["/logs/team-a"] == "" || sinks["/logs/team-b"] == "" || sinks["/logs/team-a"] == sinks["/logs/team-b"] {
		t.Fatalf("document-local sinks were merged: %v", sinks)
	}
}

func TestSyntheticSinkIdentityDoesNotDependOnSourceAddress(t *testing.T) {
	body := `{"kind":"oekaki.overlay","version":"0.1","metadata":{"origin":"human"},"sinks":[{"id":"s","type":"log_group","name":"/logs/app"}],"assertions":[{"assert":"logs.declared","subject":{"service":"api"},"sink":"s"}]}`
	var want string
	for _, source := range []string{"examples/log-coverage/overlay.json", "./overlay.json", "standard input"} {
		doc, err := Parse([]byte(body), source)
		if err != nil {
			t.Fatal(err)
		}
		g := graph()
		if _, err := New([]*Document{doc}, Options{}).Enrich(g); err != nil {
			t.Fatal(err)
		}
		var got string
		for _, node := range g.Nodes {
			if node.Type == "oekaki_log_sink" && node.Name == "/logs/app" {
				got = node.ID
				break
			}
		}
		if got == "" {
			t.Fatalf("source %q did not create the synthetic sink", source)
		}
		if want == "" {
			want = got
		} else if got != want {
			t.Fatalf("source address changed sink id: got %q, want %q", got, want)
		}
	}
}

func TestPositiveAssertionsCannotClearSuppression(t *testing.T) {
	tests := []struct {
		name             string
		positiveOrigin   core.Origin
		suppressOrigin   core.Origin
		wantSuppressed   bool
		wantWinningClaim core.Origin
	}{
		{
			name:             "lower-ranked suppression remains fail-safe",
			positiveOrigin:   core.OriginHuman,
			suppressOrigin:   core.OriginAI,
			wantSuppressed:   true,
			wantWinningClaim: core.OriginAI,
		},
		{
			name:             "higher-ranked suppression survives lower positive assertion",
			positiveOrigin:   core.OriginAI,
			suppressOrigin:   core.OriginHuman,
			wantSuppressed:   true,
			wantWinningClaim: core.OriginHuman,
		},
		{
			name:             "suppression wins identical provenance tie",
			positiveOrigin:   core.OriginHuman,
			suppressOrigin:   core.OriginHuman,
			wantSuppressed:   true,
			wantWinningClaim: core.OriginHuman,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			positive := edgeAssertionDocument(t, "positive.json", AssertEdge, tc.positiveOrigin)
			suppression := edgeAssertionDocument(t, "suppression.json", AssertEdgeSuppress, tc.suppressOrigin)
			orders := [][]*Document{{positive, suppression}, {suppression, positive}}
			var first string
			for i, docs := range orders {
				g := graph()
				if _, err := New(docs, Options{}).Enrich(g); err != nil {
					t.Fatal(err)
				}
				edge := findObservedCheckoutEdge(t, g)
				if edge.Suppressed != tc.wantSuppressed {
					t.Fatalf("order %d: suppressed = %t, want %t", i, edge.Suppressed, tc.wantSuppressed)
				}
				if edge.Claim == nil || edge.Claim.Origin != tc.wantWinningClaim {
					t.Fatalf("order %d: claim = %#v, want origin %q", i, edge.Claim, tc.wantWinningClaim)
				}
				encoded, err := g.MarshalIndent()
				if err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					first = string(encoded)
				} else if string(encoded) != first {
					t.Fatalf("application order changed graph:\n%s", firstDifference(first, string(encoded)))
				}
			}
		})
	}
}

func edgeAssertionDocument(t *testing.T, source, assertion string, origin core.Origin) *Document {
	t.Helper()
	body := fmt.Sprintf(`{"kind":"oekaki.overlay","version":"0.1","metadata":{"origin":%q},"assertions":[{"assert":%q,"from":{"service":"checkout"},"to":{"node":"aws_db_instance.orders"},"kind":"observed"}]}`, origin, assertion)
	doc, err := Parse([]byte(body), source)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func findObservedCheckoutEdge(t *testing.T, g *core.Graph) core.Edge {
	t.Helper()
	for _, edge := range g.Edges {
		if edge.From == "aws_ecs_service.checkout" && edge.To == "aws_db_instance.orders" && edge.Kind == core.EdgeObserved {
			return edge
		}
	}
	t.Fatal("observed checkout edge not found")
	return core.Edge{}
}

// firstDifference names the line that differs. A whole-document diff of two
// graphs is unreadable in a test failure, and the first divergence is almost
// always the one that explains the rest.
func firstDifference(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			return fmt.Sprintf("line %d:\n  first:  %s\n  second: %s", i+1, as[i], bs[i])
		}
	}
	return fmt.Sprintf("one document has %d lines and the other %d", len(as), len(bs))
}
