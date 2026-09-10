package kubernetes

import (
	"testing"
)

// Two paths to one backend is the ordinary way an API is versioned. They are
// one edge — the same Ingress to the same Service — but two facts about it,
// and keeping only the last silently dropped /checkout the moment /checkout/v2
// was added beside it. That is the half somebody is asking about when they ask
// what is unused.
func TestEveryRuleThatReachesAServiceIsKept(t *testing.T) {
	res := parseString(t, `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: shop
  namespace: shop
spec:
  rules:
    - host: shop.example.com
      http:
        paths:
          - path: /checkout
            pathType: Prefix
            backend:
              service:
                name: checkout
                port:
                  number: 80
          - path: /checkout/v2
            pathType: Prefix
            backend:
              service:
                name: checkout
                port:
                  number: 80
---
apiVersion: v1
kind: Service
metadata:
  name: checkout
  namespace: shop
`)

	found := 0
	for _, e := range res.Graph.Edges {
		if e.Relation != "routes" {
			continue
		}
		found++
		// Compared whole. `/checkout` is a substring of `/checkout/v2`, so a
		// containment test passes on the version that dropped it — which is
		// the exact failure this test is here to catch.
		via, _ := e.Attrs["via"].(string)
		if want := "shop.example.com/checkout, shop.example.com/checkout/v2"; via != want {
			t.Errorf("the rules read %q, want %q", via, want)
		}
		rules, _ := e.Attrs["rules"].([]string)
		if len(rules) != 2 || rules[0] != "shop.example.com/checkout" || rules[1] != "shop.example.com/checkout/v2" {
			t.Errorf("the rules as data are %q", rules)
		}
	}
	if found != 1 {
		t.Fatalf("got %d routing edges, want the one edge both rules are about", found)
	}
}

// A second reading of the same edge widens both halves of it. Leaving the list
// at the first reading was the failure widen's own comment describes — an edge
// narrowed to a rule that is only half of it — and worse here, because the two
// halves would then disagree: the words saying two rules and the data saying
// one, with the data being what a filter reads.
func TestASecondReadingOfAnEdgeWidensTheRulesToo(t *testing.T) {
	into := map[string]any{
		"via":   "a.example.com/one",
		"rules": []string{"a.example.com/one"},
	}
	widen(into, map[string]any{
		"via":   "b.example.com/two",
		"rules": []string{"b.example.com/two"},
	})

	if via, _ := into["via"].(string); via != "a.example.com/one, b.example.com/two" {
		t.Errorf("the words are %q", via)
	}
	rules, _ := into["rules"].([]string)
	if len(rules) != 2 || rules[0] != "a.example.com/one" || rules[1] != "b.example.com/two" {
		t.Fatalf("the data is %q, and no longer says what the words say", rules)
	}

	// And a rule read twice is one rule, like every other value here.
	widen(into, map[string]any{
		"via":   "a.example.com/one",
		"rules": []string{"a.example.com/one"},
	})
	if rules, _ := into["rules"].([]string); len(rules) != 2 {
		t.Errorf("a rule read twice became two: %q", rules)
	}
}

// The rules are listed in a stable order, because the graph is regenerated on
// every change and a comparison between two of them has to be about the
// estate.
func TestTheRulesAreListedInAStableOrder(t *testing.T) {
	body := `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: shop
  namespace: shop
spec:
  rules:
    - host: b.example.com
      http:
        paths:
          - path: /z
            backend:
              service:
                name: checkout
                port:
                  number: 80
    - host: a.example.com
      http:
        paths:
          - path: /a
            backend:
              service:
                name: checkout
                port:
                  number: 80
---
apiVersion: v1
kind: Service
metadata:
  name: checkout
  namespace: shop
`
	first := ""
	for range 5 {
		res := parseString(t, body)
		for _, e := range res.Graph.Edges {
			if e.Relation != "routes" {
				continue
			}
			via, _ := e.Attrs["via"].(string)
			if first == "" {
				first = via
			}
			if via != first {
				t.Fatalf("two runs disagree: %q and %q", first, via)
			}
		}
	}
	if first != "a.example.com/a, b.example.com/z" {
		t.Errorf("the rules are not in order: %q", first)
	}
}
