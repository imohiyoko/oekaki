package kubernetes

import (
	"strings"
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
		via, _ := e.Attrs["via"].(string)
		for _, want := range []string{"shop.example.com/checkout", "shop.example.com/checkout/v2"} {
			if !strings.Contains(via, want) {
				t.Errorf("the rule %q was dropped: %q", want, via)
			}
		}
	}
	if found != 1 {
		t.Fatalf("got %d routing edges, want the one edge both rules are about", found)
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
