package kubernetes

import (
	"strings"
	"testing"
)

func edgeAttrs(res *Result, from, to, relation string) map[string]any {
	for _, e := range res.Graph.Edges {
		if e.From == from && e.To == to && e.Relation == relation {
			return e.Attrs
		}
	}
	return nil
}

func nodeAttr(res *Result, id, key string) any {
	if n, ok := res.Graph.Node(id); ok {
		return n.Attrs[key]
	}
	return nil
}

const policyFixture = `
apiVersion: v1
kind: Namespace
metadata:
  name: shop
  labels:
    tier: app
---
apiVersion: v1
kind: Namespace
metadata:
  name: ops
  labels:
    tier: platform
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: db, namespace: shop}
spec:
  template:
    metadata:
      labels: {app: db, tier: data}
    spec:
      containers: [{name: db, image: registry.example/db:1}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: shop}
spec:
  template:
    metadata:
      labels: {app: web}
    spec:
      containers: [{name: web, image: registry.example/web:1}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: scraper, namespace: ops}
spec:
  template:
    metadata:
      labels: {app: scraper}
    spec:
      containers: [{name: scraper, image: registry.example/scraper:1}]
`

// A policy isolates the pods its selector matches, and nothing else. Reading
// the selector wrong points the restriction at the wrong workloads while the
// diagram looks exactly as confident either way.
func TestPolicyRestrictsOnlyWhatItSelects(t *testing.T) {
	res := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: db-only, namespace: shop}
spec:
  podSelector:
    matchLabels: {app: db}
  ingress:
  - from:
    - podSelector:
        matchLabels: {app: web}
    ports:
    - {protocol: TCP, port: 5432}
`)
	const np = "networkpolicy/shop/db-only"

	if !hasEdge(res.Graph, np, "deployment/shop/db", "restricts") {
		t.Error("the policy did not restrict the workload its selector matches")
	}
	if hasEdge(res.Graph, np, "deployment/shop/web", "restricts") {
		t.Error("the policy restricted a workload its selector does not match")
	}
	attrs := edgeAttrs(res, np, "deployment/shop/web", "allows-ingress")
	if attrs == nil {
		t.Fatal("the rule's peer did not become an allows edge")
	}
	if attrs["ports"] != "TCP/5432" {
		t.Errorf("allows attrs = %v", attrs)
	}
}

// Within one peer, a pod selector and a namespace selector are an AND. Read as
// alternatives they admit every pod in the namespace plus every namespace's
// matching pods, which is a far wider policy wearing the same colour.
func TestPeerSelectorsAreAndedNotOred(t *testing.T) {
	res := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: from-platform-scrapers, namespace: shop}
spec:
  podSelector:
    matchLabels: {app: db}
  ingress:
  - from:
    - namespaceSelector:
        matchLabels: {tier: platform}
      podSelector:
        matchLabels: {app: scraper}
`)
	const np = "networkpolicy/shop/from-platform-scrapers"

	if !hasEdge(res.Graph, np, "deployment/ops/scraper", "allows-ingress") {
		t.Error("the anded peer did not reach the one workload that satisfies both")
	}
	// web is in shop, which the namespace selector does not match, and is not
	// a scraper either.
	if hasEdge(res.Graph, np, "deployment/shop/web", "allows-ingress") {
		t.Error("the peer was read as an alternative rather than a conjunction")
	}
}

// A namespace selector can only be evaluated against Namespace objects that
// were sent. Silently matching nothing would make a policy with reach look
// like a policy with none.
func TestUnresolvableNamespaceSelectorIsRecorded(t *testing.T) {
	res := parseString(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: db, namespace: shop}
spec:
  template:
    metadata: {labels: {app: db}}
    spec:
      containers: [{name: db, image: registry.example/db:1}]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: from-elsewhere, namespace: shop}
spec:
  podSelector: {matchLabels: {app: db}}
  ingress:
  - from:
    - namespaceSelector: {matchLabels: {tier: platform}}
`)
	got, _ := nodeAttr(res, "networkpolicy/shop/from-elsewhere", "ingress_unresolved").(string)
	if !strings.Contains(got, "namespace labels are not in this input") {
		t.Errorf("ingress_unresolved = %q, want the unevaluated namespace selector", got)
	}
}

// matchExpressions is a language this parser does not evaluate. Reading such a
// selector as its matchLabels alone produces a wider selector that looks exact.
func TestMatchExpressionsRefusesRatherThanWidens(t *testing.T) {
	res := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: expressive, namespace: shop}
spec:
  podSelector:
    matchExpressions:
    - {key: app, operator: In, values: [db, web]}
  ingress:
  - from:
    - podSelector: {matchLabels: {app: web}}
`)
	const np = "networkpolicy/shop/expressive"

	for _, to := range []string{"deployment/shop/db", "deployment/shop/web"} {
		if hasEdge(res.Graph, np, to, "restricts") {
			t.Errorf("a selector that was not evaluated still restricted %s", to)
		}
	}
	got, _ := nodeAttr(res, np, "restricts_unresolved").(string)
	if !strings.Contains(got, "matchExpressions") {
		t.Errorf("restricts = %q, want the reason it was not resolved", got)
	}
}

// An egress section makes the policy isolate egress too, whether or not
// policyTypes says so. Missing that leaves a workload's outbound restriction
// out of the graph entirely.
func TestEgressIsInferredAndRead(t *testing.T) {
	res := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: web-egress, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  egress:
  - to:
    - podSelector: {matchLabels: {app: db}}
  - to:
    - ipBlock: {cidr: 0.0.0.0/0, except: [10.0.0.0/8]}
`)
	const np = "networkpolicy/shop/web-egress"

	if !hasEdge(res.Graph, np, "deployment/shop/db", "allows-egress") {
		t.Error("the egress peer was not read")
	}
	restrict := edgeAttrs(res, np, "deployment/shop/web", "restricts")
	if restrict == nil || restrict["direction"] != "Ingress,Egress" {
		t.Errorf("policyTypes were not inferred from the egress section: %v", restrict)
	}
	// An open block is the same idea the reachable enricher draws as the
	// internet, and a second name for it would split one thing in two.
	open := edgeAttrs(res, np, external, "allows-egress")
	if open == nil || open["cidr"] != "0.0.0.0/0" || open["except"] == nil {
		t.Errorf("the open ipBlock did not reach the internet node: %v", open)
	}
}

// A declared policy type with no rules under it is a closed door, and that is
// different from a door nobody wrote about.
func TestDeclaredDirectionWithNoRulesIsClosed(t *testing.T) {
	res := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: deny-all, namespace: shop}
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
`)
	const np = "networkpolicy/shop/deny-all"

	// An empty pod selector selects everything in the namespace.
	for _, to := range []string{"deployment/shop/db", "deployment/shop/web"} {
		if !hasEdge(res.Graph, np, to, "restricts") {
			t.Errorf("the empty selector did not restrict %s", to)
		}
	}
	if hasEdge(res.Graph, np, "deployment/ops/scraper", "restricts") {
		t.Error("the policy reached outside its own namespace")
	}
	if nodeAttr(res, np, "ingress_allows") != "nothing" || nodeAttr(res, np, "egress_allows") != "nothing" {
		t.Error("a policy that allows nothing does not say so")
	}
}

// A rule with ports and no peers allows every source on those ports. It is one
// of the most ordinary policies there is, and the peer it needs has to exist:
// an edge pointing at a node nobody made fails validation and takes the whole
// command down with it.
func TestPortsOnlyRuleReachesTheInternetNode(t *testing.T) {
	res := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: any-source, namespace: shop}
spec:
  podSelector: {matchLabels: {app: db}}
  ingress:
  - ports: [{protocol: TCP, port: 8080}]
`)
	if err := res.Graph.Validate(); err != nil {
		t.Fatalf("a ports-only rule produced an invalid graph: %v", err)
	}
	n, ok := res.Graph.Node(external)
	if !ok {
		t.Fatal("the rule's implicit peer has no node")
	}
	// The id is shared with the enrichers, so the definition has to be too:
	// two shapes for one id is a conflict as soon as graphs are combined.
	if n.Type != "external_endpoint" || n.Name != "Internet" || n.Provider != "external" {
		t.Errorf("internet node = %+v, want the shape the enrichers build", n)
	}
}

// One policy can allow the same peer in both directions, and two rules can
// allow one peer on different ports. Both used to arrive as a single edge
// keeping whichever was read first.
func TestBothDirectionsAndBothPortsSurvive(t *testing.T) {
	res := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: both-ways, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Ingress, Egress]
  ingress:
  - from: [{podSelector: {matchLabels: {app: db}}}]
    ports: [{protocol: TCP, port: 8080}]
  - from: [{podSelector: {matchLabels: {app: db}}}]
    ports: [{protocol: TCP, port: 9090}]
  egress:
  - to: [{podSelector: {matchLabels: {app: db}}}]
    ports: [{protocol: TCP, port: 5432}]
`)
	const np = "networkpolicy/shop/both-ways"

	in := edgeAttrs(res, np, "deployment/shop/db", "allows-ingress")
	out := edgeAttrs(res, np, "deployment/shop/db", "allows-egress")
	if in == nil || out == nil {
		t.Fatalf("a direction was lost: ingress=%v egress=%v", in, out)
	}
	if out["ports"] != "TCP/5432" {
		t.Errorf("egress ports = %v", out["ports"])
	}
	ports, _ := in["ports"].(string)
	if !strings.Contains(ports, "TCP/8080") || !strings.Contains(ports, "TCP/9090") {
		t.Errorf("ingress ports = %q, want both rules' ports", ports)
	}
}

// The same rule shape on the way out. An egress section with no `to` allows
// every destination, and the direction it is recorded under is what decides
// whether the derivation ever looks at it.
func TestPeerlessEgressRuleIsRecordedOutbound(t *testing.T) {
	res := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: outbound, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  egress:
  - ports: [{protocol: TCP, port: 443}]
`)
	const np = "networkpolicy/shop/outbound"

	attrs := edgeAttrs(res, np, external, "allows-egress")
	if attrs == nil {
		t.Fatal("a peerless egress rule was not recorded outbound")
	}
	if attrs["peer"] != "any source" {
		t.Errorf("attrs = %v, want the marker the derivation expands", attrs)
	}
	if err := res.Graph.Validate(); err != nil {
		t.Fatalf("invalid graph: %v", err)
	}
}

// An empty peer list is the same statement as no peer list: every source.
// Reading one and not the other would make two spellings of one policy draw
// different pictures.
func TestAnEmptyPeerListIsTheSameAsNone(t *testing.T) {
	written := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: p, namespace: shop}
spec:
  podSelector: {matchLabels: {app: db}}
  ingress: [{from: [], ports: [{port: 80}]}]
`)
	omitted := parseString(t, policyFixture+`
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: p, namespace: shop}
spec:
  podSelector: {matchLabels: {app: db}}
  ingress: [{ports: [{port: 80}]}]
`)
	for _, res := range []*Result{written, omitted} {
		attrs := edgeAttrs(res, "networkpolicy/shop/p", external, "allows-ingress")
		if attrs == nil || attrs["peer"] != "any source" {
			t.Errorf("attrs = %v, want the same marker for both spellings", attrs)
		}
	}
}
