package traces

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func folded(t *testing.T, doc string) ([]core.Path, []core.Observation, []string) {
	t.Helper()
	d, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return d.Paths()
}

func routes(paths []core.Path) []string {
	var out []string
	for _, p := range paths {
		out = append(out, strings.Join(p.Nodes, ">"))
	}
	return out
}

func want(t *testing.T, got []string, wanted ...string) {
	t.Helper()
	if len(got) != len(wanted) {
		t.Fatalf("got %v, want %v", got, wanted)
	}
	have := map[string]bool{}
	for _, route := range got {
		have[route] = true
	}
	for _, route := range wanted {
		if !have[route] {
			t.Fatalf("got %v, want %v", got, wanted)
		}
	}
}

// A trace is a tree, not a line. Each root-to-leaf chain is one route, because
// flattening a fan-out into a single walk would claim an order between two
// branches that nothing observed.
func TestATraceThatFansOutIsTwoRoutes(t *testing.T) {
	paths, _, unordered := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","span_id":"1","service":"gateway"},
		{"trace_id":"t1","span_id":"2","parent_span_id":"1","service":"checkout"},
		{"trace_id":"t1","span_id":"3","parent_span_id":"2","service":"ledger"},
		{"trace_id":"t1","span_id":"4","parent_span_id":"1","service":"search"}
	]}`)
	if len(unordered) != 0 {
		t.Fatalf("a readable trace was reported unreadable: %v", unordered)
	}
	want(t, routes(paths), "gateway>checkout>ledger", "gateway>search")
}

// The reason span ids are read at all. A cache reached from two callers is one
// service with two parents; joining its children to its *name* produces a
// route through it that nobody walked.
func TestAServiceWithTwoCallersDoesNotInventARoute(t *testing.T) {
	withIDs := `{"version":"1","spans":[
		{"trace_id":"t1","span_id":"1","service":"gateway"},
		{"trace_id":"t1","span_id":"2","parent_span_id":"1","service":"auth"},
		{"trace_id":"t1","span_id":"3","parent_span_id":"1","service":"checkout"},
		{"trace_id":"t1","span_id":"4","parent_span_id":"2","service":"cache"},
		{"trace_id":"t1","span_id":"5","parent_span_id":"3","service":"cache"},
		{"trace_id":"t1","span_id":"6","parent_span_id":"4","service":"redis"}
	]}`
	paths, _, unordered := folded(t, withIDs)
	if len(unordered) != 0 {
		t.Fatalf("a trace with ids was reported unreadable: %v", unordered)
	}
	want(t, routes(paths), "gateway>auth>cache>redis", "gateway>checkout>cache")
	for _, route := range routes(paths) {
		if route == "gateway>checkout>cache>redis" {
			t.Fatal("redis was reached through auth; the drawing says it was reached through checkout")
		}
	}

	// Without ids the same shape cannot be read at all, and saying so is the
	// only honest answer.
	withoutIDs := `{"version":"1","spans":[
		{"trace_id":"t1","service":"gateway"},
		{"trace_id":"t1","service":"auth","parent_service":"gateway"},
		{"trace_id":"t1","service":"checkout","parent_service":"gateway"},
		{"trace_id":"t1","service":"cache","parent_service":"auth"},
		{"trace_id":"t1","service":"cache","parent_service":"checkout"},
		{"trace_id":"t1","service":"redis","parent_service":"cache"}
	]}`
	paths, _, unordered = folded(t, withoutIDs)
	if len(paths) != 0 || len(unordered) != 1 {
		t.Fatalf("an ambiguous trace produced %v and reported %v", routes(paths), unordered)
	}
}

// Traffic must move the number, not the size of the document — and a service
// called twice was called twice.
func TestTheSameRouteIsOnePathAndACount(t *testing.T) {
	paths, counts, _ := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","service":"a"},
		{"trace_id":"t1","service":"b","parent_service":"a","observed_at":"2026-09-01T00:00:00Z"},
		{"trace_id":"t2","service":"a"},
		{"trace_id":"t2","service":"b","parent_service":"a","observed_at":"2026-09-03T00:00:00Z"},
		{"trace_id":"t3","service":"a"},
		{"trace_id":"t3","service":"b","parent_service":"a","observed_at":"2026-09-02T00:00:00Z"}
	]}`)
	if len(paths) != 1 || len(counts) != 1 {
		t.Fatalf("got %d paths and %d readings, want one of each: %v", len(paths), len(counts), routes(paths))
	}
	if counts[0].Value == nil || *counts[0].Value != 3 {
		t.Fatalf("the count is %v, want 3", counts[0].Value)
	}
	if counts[0].Subject != core.PathKey([]string{"a", "b"}) {
		t.Fatalf("the reading is about %q", counts[0].Subject)
	}
	// The latest walk, not the last span that happened to be parsed.
	if counts[0].ObservedAt != "2026-09-03T00:00:00Z" {
		t.Fatalf("last walked %q", counts[0].ObservedAt)
	}
}

// Two calls to the same service inside one trace are two walks. Folding them
// reports less traffic than there was.
func TestOneTraceCallingTwiceCountsTwice(t *testing.T) {
	_, counts, _ := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","span_id":"1","service":"gateway"},
		{"trace_id":"t1","span_id":"2","parent_span_id":"1","service":"ledger"},
		{"trace_id":"t1","span_id":"3","parent_span_id":"1","service":"ledger"}
	]}`)
	if len(counts) != 1 || counts[0].Value == nil || *counts[0].Value != 2 {
		t.Fatalf("got %#v, want one route walked twice", counts)
	}
}

// A trace whose entry span was sampled away has no root. Rooting it at
// whichever service sorted first would invent where a request arrived.
func TestATraceWithNoEntryIsReportedNotGuessedAt(t *testing.T) {
	paths, _, unordered := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","service":"b","parent_service":"a"},
		{"trace_id":"t1","service":"a","parent_service":"b"}
	]}`)
	if len(paths) != 0 {
		t.Fatalf("a route was invented for a trace with no entry: %v", routes(paths))
	}
	if len(unordered) != 1 {
		t.Fatalf("the unreadable trace was not reported: %v", unordered)
	}
}

// A span naming itself is a retry or an internal segment, not an entry.
// Treating it as one starts a second route in the middle of the request and
// counts it as traffic of its own.
func TestASelfCallIsNotAnEntryPoint(t *testing.T) {
	paths, counts, _ := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","service":"gateway"},
		{"trace_id":"t1","service":"checkout","parent_service":"gateway"},
		{"trace_id":"t1","service":"checkout","parent_service":"checkout"},
		{"trace_id":"t1","service":"ledger","parent_service":"checkout"}
	]}`)
	want(t, routes(paths), "gateway>checkout>ledger")
	if len(counts) != 1 || *counts[0].Value != 1 {
		t.Fatalf("the self call was counted as its own traffic: %#v", counts)
	}
}

// Two segments of one service in a span tree are one participant.
func TestConsecutiveSegmentsOfOneServiceAreOneParticipant(t *testing.T) {
	paths, _, _ := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","span_id":"1","service":"gateway"},
		{"trace_id":"t1","span_id":"2","parent_span_id":"1","service":"gateway"},
		{"trace_id":"t1","span_id":"3","parent_span_id":"2","service":"ledger"}
	]}`)
	want(t, routes(paths), "gateway>ledger")
}

// A retry that comes back through the gateway is a real trace. The walk stops
// the second time it arrives somewhere rather than following it forever.
func TestACycleInATraceTerminates(t *testing.T) {
	paths, _, _ := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","service":"gateway"},
		{"trace_id":"t1","service":"worker","parent_service":"gateway"},
		{"trace_id":"t1","service":"gateway","parent_service":"worker"}
	]}`)
	want(t, routes(paths), "gateway>worker")
}

// A trace where only some spans carry ids cannot be assembled: the ones
// without have no place in the tree.
func TestHalfIdentifiedSpansFallBackToNames(t *testing.T) {
	paths, _, unordered := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","span_id":"1","service":"gateway"},
		{"trace_id":"t1","service":"checkout","parent_service":"gateway"}
	]}`)
	if len(unordered) != 0 {
		t.Fatalf("a trace readable by name was reported unreadable: %v", unordered)
	}
	want(t, routes(paths), "gateway>checkout")
}

// The same input read twice has to produce the same document.
func TestFoldingIsDeterministic(t *testing.T) {
	doc := `{"version":"1","spans":[
		{"trace_id":"t1","span_id":"1","service":"a"},
		{"trace_id":"t1","span_id":"2","parent_span_id":"1","service":"c"},
		{"trace_id":"t1","span_id":"3","parent_span_id":"1","service":"b"},
		{"trace_id":"t2","span_id":"4","service":"a"},
		{"trace_id":"t2","span_id":"5","parent_span_id":"4","service":"b"}
	]}`
	first, _, _ := folded(t, doc)
	for range 5 {
		again, _, _ := folded(t, doc)
		if strings.Join(routes(again), "|") != strings.Join(routes(first), "|") {
			t.Fatalf("%v then %v", routes(first), routes(again))
		}
	}
}

// A pathological span dump must not turn a render into a hang.
func TestAWideTraceIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"version":"1","spans":[{"trace_id":"t1","span_id":"root","service":"gateway"}`)
	for i := range 400 {
		b.WriteString(`,{"trace_id":"t1","span_id":"s`)
		b.WriteString(string(rune('a'+i%26)) + strings.Repeat("x", i/26+1))
		b.WriteString(`","parent_span_id":"root","service":"leaf`)
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(`"}`)
	}
	b.WriteString(`]}`)

	paths, _, _ := folded(t, b.String())
	if len(paths) > maxChainsPerTrace {
		t.Fatalf("got %d routes from one trace", len(paths))
	}
}
