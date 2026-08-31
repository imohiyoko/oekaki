package reachable

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// policyGraph builds what the manifest parser emits, so the contract between
// the two halves is written down here rather than assumed.
func policyGraph(edges ...core.Edge) *core.Graph {
	g := core.New()
	g.Nodes = []core.Node{
		{ID: "deployment/shop/web", Type: "deployment", Name: "web"},
		{ID: "deployment/shop/db", Type: "deployment", Name: "db"},
		{ID: "deployment/shop/stray", Type: "deployment", Name: "stray"},
		{ID: "networkpolicy/shop/p", Type: "networkpolicy", Name: "p"},
		{ID: "networkpolicy/shop/q", Type: "networkpolicy", Name: "q"},
	}
	g.Edges = edges
	return g
}

func restricts(policy, target, direction string) core.Edge {
	return core.Edge{From: policy, To: target, Kind: core.EdgeIACRef, Relation: "restricts",
		Attrs: map[string]any{"direction": direction}}
}

func allows(policy, peer, direction string) core.Edge {
	return core.Edge{From: policy, To: peer, Kind: core.EdgeIACRef, Relation: "allows",
		Attrs: map[string]any{"direction": direction, "ports": "TCP/5432"}}
}

func reachableEdge(g *core.Graph, from, to string) *core.Edge {
	for i := range g.Edges {
		e := &g.Edges[i]
		if e.Kind == core.EdgeReachable && e.From == from && e.To == to {
			return e
		}
	}
	return nil
}

// The plain case: a restricted workload, and the one peer let through.
func TestPolicyAllowanceBecomesReachable(t *testing.T) {
	g := policyGraph(
		restricts("networkpolicy/shop/p", "deployment/shop/db", "Ingress"),
		allows("networkpolicy/shop/p", "deployment/shop/web", "Ingress"),
	)
	if _, err := (Enricher{}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	e := reachableEdge(g, "deployment/shop/web", "deployment/shop/db")
	if e == nil {
		t.Fatal("the allowance did not become a reachable path")
	}
	if e.Attrs["ports"] != "TCP/5432" || e.Attrs["policy"] != "networkpolicy/shop/p" {
		t.Errorf("attrs = %v, want the ports and the policy that decided it", e.Attrs)
	}
}

// A namespace with no policy lets every pod reach every other. Drawing that is
// drawing the complete graph, which says nothing; the fact worth having is
// which ends are restricted, and the parser's restricts edges carry it.
func TestUnrestrictedWorkloadsGetNoEdges(t *testing.T) {
	g := policyGraph(
		restricts("networkpolicy/shop/p", "deployment/shop/db", "Ingress"),
		allows("networkpolicy/shop/p", "deployment/shop/web", "Ingress"),
	)
	if _, err := (Enricher{}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	for _, pair := range [][2]string{
		{"deployment/shop/web", "deployment/shop/stray"},
		{"deployment/shop/stray", "deployment/shop/web"},
		{"deployment/shop/stray", "deployment/shop/db"},
	} {
		if reachableEdge(g, pair[0], pair[1]) != nil {
			t.Errorf("%s -> %s was drawn, but nothing restricts either end", pair[0], pair[1])
		}
	}
}

// Both ends have to permit the path. A sender whose egress is restricted and
// does not name the destination cannot reach it, however open the destination
// is — and reading only the receiving end would draw the path anyway.
func TestBothEndsMustPermit(t *testing.T) {
	g := policyGraph(
		restricts("networkpolicy/shop/p", "deployment/shop/db", "Ingress"),
		allows("networkpolicy/shop/p", "deployment/shop/web", "Ingress"),
		// web may only talk to stray, so its allowance into db is dead.
		restricts("networkpolicy/shop/q", "deployment/shop/web", "Egress"),
		allows("networkpolicy/shop/q", "deployment/shop/stray", "Egress"),
	)
	if _, err := (Enricher{}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	if reachableEdge(g, "deployment/shop/web", "deployment/shop/db") != nil {
		t.Error("a path the sender's egress policy does not allow was drawn")
	}
	// The sender's own allowance still holds: stray accepts everything, and
	// web is restricted, so this path is both permitted and worth drawing.
	if reachableEdge(g, "deployment/shop/web", "deployment/shop/stray") == nil {
		t.Error("an egress allowance into an unrestricted destination was not drawn")
	}
}

// A policy with peers this input could not resolve permits more than what is
// drawn. Saying nothing would let the drawn subset read as the whole.
func TestPartiallyReadPolicyIsReported(t *testing.T) {
	g := policyGraph(
		restricts("networkpolicy/shop/p", "deployment/shop/db", "Ingress"),
		allows("networkpolicy/shop/p", "deployment/shop/web", "Ingress"),
	)
	g.Nodes[3].Attrs = map[string]any{
		"ingress_unresolved": "a namespace selector matched nothing",
	}
	r, err := (Enricher{}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}

	if len(r.Unmatched) != 1 || r.Unmatched[0].Selector["policy"] != "networkpolicy/shop/p" {
		t.Fatalf("Unmatched = %v, want the policy whose reach is partly unknown", r.Unmatched)
	}
}

// A graph with no policies in it must come out of this untouched, since the
// enricher runs on Terraform estates too.
func TestAGraphWithoutPoliciesIsUnchanged(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "a", Type: "deployment", Name: "a"}, {ID: "b", Type: "deployment", Name: "b"}}
	before := len(g.Edges)
	if _, err := (Enricher{}).Enrich(g); err != nil {
		t.Fatal(err)
	}
	if len(g.Edges) != before {
		t.Errorf("edges = %d, want %d", len(g.Edges), before)
	}
}
