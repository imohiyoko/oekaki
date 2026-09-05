package traces

import (
	"sort"

	"github.com/imohiyoko/oekaki/core"
)

// traceTree is one trace, indexed by who called whom.
type traceTree struct {
	children map[string][]string
	roots    map[string]bool
	seen     map[string]bool
	last     string
}

// Paths folds spans into the routes they walked, and counts how often each
// was walked.
//
// This is the same input Edges reads, one level up. An edge says the frontend
// called checkout; a path says a request arrived at the frontend, went through
// checkout, and ended at the ledger — and that order is what "this route has
// not been used in ninety days" and "this route fired when nothing declared
// it" are statements about.
//
// # What counts as one route
//
// A trace is a tree, not a line: one request can fan out. Each root-to-leaf
// chain is one route, because that is the sentence an operator says — this
// one, then that one, then that one — and a tree flattened into a single walk
// would claim an order between two branches that nothing observed.
//
// The same route in a thousand traces is one path carrying a count of a
// thousand. Traffic must move the number, not the size of the document.
//
// # What is deliberately not read
//
// Nothing here looks at what was sent or what came back. A route is who, in
// what order, how often and when last — which is everything the unused list,
// the silence and the unexpected route need, and none of it is customer data.
func (d *Document) Paths() ([]core.Path, []core.Observation) {
	traces := map[string]*traceTree{}
	order := []string{}

	for _, s := range d.Spans {
		t := traces[s.TraceID]
		if t == nil {
			t = &traceTree{children: map[string][]string{}, roots: map[string]bool{}, seen: map[string]bool{}}
			traces[s.TraceID] = t
			order = append(order, s.TraceID)
		}
		t.seen[s.Service] = true
		if at := normalizeTime(s.ObservedAt); at > t.last {
			t.last = at
		}
		if s.ParentService == "" || s.ParentService == s.Service {
			// A span that names no caller is where the request arrived. That
			// is a fact the trace records, and it is the only honest way to
			// know: a service can be both an entry and something another
			// service called back into — a retry through a gateway — so
			// "nothing named it as a child" is not the same question.
			t.roots[s.Service] = true
			continue
		}
		t.seen[s.ParentService] = true
		t.children[s.ParentService] = append(t.children[s.ParentService], s.Service)
	}

	walked := map[string]int{}
	nodes := map[string][]string{}
	last := map[string]string{}
	keys := []string{}

	for _, id := range order {
		t := traces[id]
		for _, chain := range chains(t) {
			if len(chain) < 2 {
				continue
			}
			key := core.PathKey(chain)
			if walked[key] == 0 {
				keys = append(keys, key)
				nodes[key] = chain
			}
			walked[key]++
			if t.last > last[key] {
				last[key] = t.last
			}
		}
	}
	sort.Strings(keys)

	paths := make([]core.Path, 0, len(keys))
	observations := make([]core.Observation, 0, len(keys))
	for _, key := range keys {
		count := float64(walked[key])
		paths = append(paths, core.Path{
			Nodes: nodes[key],
			Kind:  core.EdgeObserved,
			Claim: &core.Claim{Origin: core.OriginParser, Note: "request traces"},
		})
		// The count is an observation rather than an attribute because it is a
		// measurement over a window, and everything that already knows how to
		// read a measurement — a threshold, a cutoff in the viewer, a
		// disagreement between two collectors — knows how to read this one.
		observations = append(observations, core.Observation{
			Subject:    key,
			Metric:     "path_requests",
			Value:      &count,
			Unit:       "requests",
			ObservedAt: last[key],
			Evidence:   &core.Claim{Origin: core.OriginParser, Note: "request traces"},
		})
	}
	return paths, observations
}

// chains returns every root-to-leaf walk in one trace, in a stable order.
//
// A root is a service with a span that names no caller. A trace whose spans
// all name one — which happens when the entry span was dropped or sampled
// away — has no root, and is skipped rather than rooted at whichever service
// sorted first. Guessing where a request entered is exactly the kind of
// invention this project refuses.
func chains(t *traceTree) [][]string {
	roots := make([]string, 0, len(t.roots))
	for service := range t.roots {
		roots = append(roots, service)
	}
	sort.Strings(roots)

	var out [][]string
	var walk func(at string, sofar []string, visited map[string]bool)
	walk = func(at string, sofar []string, visited map[string]bool) {
		sofar = append(sofar, at)
		kids := append([]string(nil), t.children[at]...)
		sort.Strings(kids)

		// A trace can record a cycle — a retry through a gateway, or two
		// services that call each other. The walk stops the second time it
		// arrives somewhere rather than following it forever, and what it has
		// is a real route as far as it goes.
		next := kids[:0]
		for _, kid := range kids {
			if !visited[kid] {
				next = append(next, kid)
			}
		}
		if len(next) == 0 {
			chain := make([]string, len(sofar))
			copy(chain, sofar)
			out = append(out, chain)
			return
		}
		for _, kid := range dedupe(next) {
			visited[kid] = true
			walk(kid, sofar, visited)
			delete(visited, kid)
		}
	}
	for _, root := range roots {
		walk(root, nil, map[string]bool{root: true})
	}
	return out
}

func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || in[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}
