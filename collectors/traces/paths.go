package traces

import (
	"sort"

	"github.com/imohiyoko/oekaki/core"
)

// Paths folds spans into the routes they walked, and counts how often each was
// walked.
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
// # Why span ids matter
//
// The tree has to be built from span identity, not from service names. A cache
// called by both auth and checkout is one *service* with two callers; joining
// its children to its name produces gateway, checkout, cache, redis when redis
// was only ever reached through auth — a route nobody walked, invented by this
// function, in a document whose whole purpose is telling apart what was
// claimed from what was seen.
//
// So when spans carry ids, the walk follows them. When they do not, the fold
// happens only where service names cannot be ambiguous — no service with two
// different callers — and a trace that cannot be ordered is returned as such
// rather than guessed at. Sampling drops entry spans and instrumentation omits
// ids; both produce a trace this cannot read, and neither is a licence to make
// one up.
//
// # What is deliberately not read
//
// Nothing here looks at what was sent or what came back. A route is who, in
// what order, how often and when last — which is everything the unused list,
// the silence and the unexpected route need, and none of it is customer data.
func (d *Document) Paths() (paths []core.Path, counts []core.Observation, unordered []string) {
	byTrace := map[string][]Span{}
	order := []string{}
	for _, s := range d.Spans {
		if _, seen := byTrace[s.TraceID]; !seen {
			order = append(order, s.TraceID)
		}
		byTrace[s.TraceID] = append(byTrace[s.TraceID], s)
	}

	walked := map[string]int{}
	nodes := map[string][]string{}
	last := map[string]string{}
	keys := []string{}

	for _, id := range order {
		chains, ok := walks(byTrace[id])
		if !ok {
			unordered = append(unordered, id)
			continue
		}
		latest := ""
		for _, s := range byTrace[id] {
			if at := normalizeTime(s.ObservedAt); at > latest {
				latest = at
			}
		}
		for _, chain := range chains {
			key := core.PathKey(chain)
			if walked[key] == 0 {
				keys = append(keys, key)
				nodes[key] = chain
			}
			// Every walk counts, including two identical ones in the same
			// trace: a service called twice was called twice, and a count
			// that folded them would report less traffic than there was.
			walked[key]++
			if latest > last[key] {
				last[key] = latest
			}
		}
	}
	sort.Strings(keys)

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
		counts = append(counts, core.Observation{
			Subject:    key,
			Metric:     "path_requests",
			Value:      &count,
			Unit:       "requests",
			ObservedAt: last[key],
			Evidence:   &core.Claim{Origin: core.OriginParser, Note: "request traces"},
		})
	}
	return paths, counts, unordered
}

// maxChainDepth and maxChainsPerTrace bound one trace.
//
// A trace deep or wide enough to meet them is not one anybody will read as a
// route anyway, and the bound is what keeps a pathological span dump from
// turning a render into a hang.
const (
	maxChainDepth     = 24
	maxChainsPerTrace = 256
)

// walks returns the routes one trace walked, and whether it could be read at
// all.
func walks(spans []Span) ([][]string, bool) {
	if identified(spans) {
		return bySpanID(spans)
	}
	return byServiceName(spans)
}

// identified reports whether every span in the trace carries its own id. A
// trace where only some do cannot be assembled: the ones without ids have no
// place in the tree, and putting them somewhere is the invention this avoids.
func identified(spans []Span) bool {
	for _, s := range spans {
		if s.SpanID == "" {
			return false
		}
	}
	return len(spans) > 0
}

// bySpanID builds the real tree.
//
// A span with no parent is where the request arrived. A span whose parent is
// not in this trace is an orphan — the parent was sampled away — and its
// subtree is left out rather than rooted at the orphan, because where that
// request entered is exactly what was lost.
func bySpanID(spans []Span) ([][]string, bool) {
	byID := make(map[string]Span, len(spans))
	for _, s := range spans {
		if _, clash := byID[s.SpanID]; clash {
			// Two spans sharing an id are not a tree: nothing here can tell
			// which children belong to which.
			return nil, false
		}
		byID[s.SpanID] = s
	}

	children := map[string][]string{}
	var roots []string
	for _, s := range spans {
		if s.ParentSpanID == "" || s.ParentSpanID == s.SpanID {
			roots = append(roots, s.SpanID)
			continue
		}
		if _, ok := byID[s.ParentSpanID]; !ok {
			continue // an orphan: its ancestry left with the dropped span
		}
		children[s.ParentSpanID] = append(children[s.ParentSpanID], s.SpanID)
	}
	if len(roots) == 0 {
		return nil, false
	}
	sort.Strings(roots)
	for id := range children {
		sort.Strings(children[id])
	}

	var out [][]string
	for _, root := range roots {
		out = append(out, chainsFrom(root, children, func(id string) string { return byID[id].Service })...)
		if len(out) >= maxChainsPerTrace {
			return out[:maxChainsPerTrace], true
		}
	}
	return out, true
}

// byServiceName is the fallback for spans that carry no identity of their own.
//
// It folds only where names cannot be ambiguous. One service with two
// different callers is where a name-keyed tree starts inventing orders, so a
// trace containing one is reported as unreadable instead.
func byServiceName(spans []Span) ([][]string, bool) {
	children := map[string][]string{}
	parents := map[string]map[string]bool{}
	roots := map[string]bool{}

	for _, s := range spans {
		if s.Service == "" {
			return nil, false
		}
		if s.ParentService == "" {
			// A span that names no caller is where the request arrived. A
			// span naming *itself* is a retry or an internal segment, not an
			// entry: treating it as one would start a second route in the
			// middle of a request and count it as traffic of its own.
			roots[s.Service] = true
			continue
		}
		if s.ParentService == s.Service {
			continue
		}
		if parents[s.Service] == nil {
			parents[s.Service] = map[string]bool{}
		}
		parents[s.Service][s.ParentService] = true
		if len(parents[s.Service]) > 1 {
			return nil, false
		}
		children[s.ParentService] = append(children[s.ParentService], s.Service)
	}
	if len(roots) == 0 {
		return nil, false
	}

	entries := make([]string, 0, len(roots))
	for service := range roots {
		entries = append(entries, service)
	}
	sort.Strings(entries)
	for id := range children {
		sort.Strings(children[id])
	}

	var out [][]string
	for _, root := range entries {
		out = append(out, chainsFrom(root, children, func(id string) string { return id })...)
		if len(out) >= maxChainsPerTrace {
			return out[:maxChainsPerTrace], true
		}
	}
	return out, true
}

// chainsFrom walks one tree and returns every root-to-leaf route through it,
// named by whatever each step is called.
//
// Consecutive steps in the same service collapse: a segment of a service
// calling another segment of itself is one participant, and "checkout then
// checkout" is not a route anybody means. A route needs two participants after
// that, so a request that never left the service it arrived at is not one.
func chainsFrom(root string, children map[string][]string, name func(string) string) [][]string {
	var out [][]string

	var walk func(at string, sofar []string, depth int, visiting map[string]bool)
	walk = func(at string, sofar []string, depth int, visiting map[string]bool) {
		if len(out) >= maxChainsPerTrace {
			return
		}
		service := name(at)
		if len(sofar) == 0 || sofar[len(sofar)-1] != service {
			sofar = append(sofar, service)
		}

		var next []string
		for _, kid := range children[at] {
			// A trace can record a cycle: a retry that comes back through the
			// gateway, or two services that call each other. The walk stops
			// the second time it arrives somewhere rather than following it
			// forever, and what it has is a real route as far as it goes.
			if !visiting[kid] && depth < maxChainDepth {
				next = append(next, kid)
			}
		}
		if len(next) == 0 {
			if len(sofar) < 2 {
				return
			}
			chain := make([]string, len(sofar))
			copy(chain, sofar)
			out = append(out, chain)
			return
		}
		for _, kid := range next {
			visiting[kid] = true
			walk(kid, sofar, depth+1, visiting)
			delete(visiting, kid)
		}
	}
	walk(root, nil, 0, map[string]bool{root: true})
	return out
}
