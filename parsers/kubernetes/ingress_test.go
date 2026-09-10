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
		// Compared entry by entry. `/checkout` is a substring of
		// `/checkout/v2`, so a containment test passes on the version that
		// dropped it — which is the exact failure this test is here to catch.
		rules, _ := e.Attrs["rules"].([]string)
		if len(rules) != 2 ||
			rules[0] != "shop.example.com/checkout" ||
			rules[1] != "shop.example.com/checkout/v2" {
			t.Errorf("the rules are %q", rules)
		}
		via, _ := e.Attrs["via"].(string)
		if want := "shop.example.com/checkout, shop.example.com/checkout/v2"; via != want {
			t.Errorf("the words read %q, want %q", via, want)
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
	var first []string
	for range 5 {
		res := parseString(t, body)
		for _, e := range res.Graph.Edges {
			if e.Relation != "routes" {
				continue
			}
			rules, _ := e.Attrs["rules"].([]string)
			if first == nil {
				first = rules
			}
			if len(rules) != len(first) {
				t.Fatalf("two runs disagree: %q and %q", first, rules)
			}
			for i := range rules {
				if rules[i] != first[i] {
					t.Fatalf("two runs disagree: %q and %q", first, rules)
				}
			}
		}
	}
	if len(first) != 2 || first[0] != "a.example.com/a" || first[1] != "b.example.com/z" {
		t.Errorf("the rules are not in order: %q", first)
	}
}

// A default backend is a way in, and it is not an API path. It stays in the
// words a person reads and out of the list a consumer matches against: an entry
// nothing can be matched against is not a smaller answer, it is a wrong one.
func TestADescriptionIsNotAnAPIPath(t *testing.T) {
	res := parseString(t, `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: shop
  namespace: shop
spec:
  defaultBackend:
    service:
      name: checkout
      port:
        number: 80
  rules:
    - http:
        paths:
          - path: ""
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

	for _, e := range res.Graph.Edges {
		if e.Relation != "routes" {
			continue
		}
		via, _ := e.Attrs["via"].(string)
		for _, want := range []string{"default backend", "any host and path"} {
			if !strings.Contains(via, want) {
				t.Errorf("the words lost %q: %q", want, via)
			}
		}
		if rules, ok := e.Attrs["rules"]; ok {
			t.Errorf("a rule that matched on nothing was given a name: %#v", rules)
		}
	}
}

// A value is whole, however many commas are in it.
//
// A label selector is `app=web,tier=front` — one value that happens to contain
// a comma — and splitting it to merge part by part dropped `tier=front` and
// left a string that was neither selector. Anything here that really holds
// several things is a list, and lists merge as sets.
func TestAValueThatContainsACommaIsStillOneValue(t *testing.T) {
	into := map[string]any{"selector": "app=web,tier=front"}
	widen(into, map[string]any{"selector": "app=api,tier=front"})

	want := "app=web,tier=front, app=api,tier=front"
	if got, _ := into["selector"].(string); got != want {
		t.Errorf("the selectors read %q, want %q", got, want)
	}
}

// A list merges as a set: everything either reading saw, once each, in an
// order two runs agree on.
func TestAListMergesAsASet(t *testing.T) {
	into := map[string]any{"rules": []string{"b.example.com/two"}}
	widen(into, map[string]any{"rules": []string{"a.example.com/one"}})
	widen(into, map[string]any{"rules": []string{"a.example.com/one"}})

	rules, _ := into["rules"].([]string)
	if len(rules) != 2 || rules[0] != "a.example.com/one" || rules[1] != "b.example.com/two" {
		t.Errorf("the rules are %q", rules)
	}
}

// A rule that matched on a host alone names that host, which is a way in
// somebody can tell from another. What has no name is a rule that matched on
// nothing — a default backend, a rule with neither host nor path — and that is
// the line: `rules` holds what the rule matched on.
func TestARuleThatMatchedOnAHostAloneIsStillNamed(t *testing.T) {
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
          - backend:
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

	for _, e := range res.Graph.Edges {
		if e.Relation != "routes" {
			continue
		}
		rules, _ := e.Attrs["rules"].([]string)
		if len(rules) != 1 || rules[0] != "shop.example.com" {
			t.Errorf("the host is not named as the way in: %q", rules)
		}
	}
}
