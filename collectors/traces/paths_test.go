package traces

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func walks(t *testing.T, doc string) ([]core.Path, []core.Observation) {
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

// A trace is a tree, not a line. Each root-to-leaf chain is one route, because
// flattening a fan-out into a single walk would claim an order between two
// branches that nothing observed.
func TestATraceThatFansOutIsTwoRoutes(t *testing.T) {
	paths, _ := walks(t, `{"version":"1","spans":[
		{"trace_id":"t1","service":"gateway"},
		{"trace_id":"t1","service":"checkout","parent_service":"gateway"},
		{"trace_id":"t1","service":"ledger","parent_service":"checkout"},
		{"trace_id":"t1","service":"search","parent_service":"gateway"}
	]}`)
	got := routes(paths)
	want := map[string]bool{"gateway>checkout>ledger": true, "gateway>search": true}
	if len(got) != 2 {
		t.Fatalf("got %v, want the two root-to-leaf walks", got)
	}
	for _, route := range got {
		if !want[route] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// Traffic must move the number, not the size of the document.
func TestTheSameRouteInManyTracesIsOnePathAndACount(t *testing.T) {
	paths, counts := walks(t, `{"version":"1","spans":[
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

// A trace whose entry span was sampled away has no root. Rooting it at
// whichever service sorted first would invent where a request arrived.
func TestATraceWithNoRootIsNotGuessedAt(t *testing.T) {
	paths, _ := walks(t, `{"version":"1","spans":[
		{"trace_id":"t1","service":"b","parent_service":"a"},
		{"trace_id":"t1","service":"a","parent_service":"b"}
	]}`)
	if len(paths) != 0 {
		t.Fatalf("a route was invented for a trace with no entry: %v", routes(paths))
	}
}

// A retry through a gateway is a real trace. The walk stops the second time it
// arrives somewhere rather than following it forever.
func TestACycleInATraceTerminates(t *testing.T) {
	paths, _ := walks(t, `{"version":"1","spans":[
		{"trace_id":"t1","service":"gateway"},
		{"trace_id":"t1","service":"worker","parent_service":"gateway"},
		{"trace_id":"t1","service":"gateway","parent_service":"worker"}
	]}`)
	if len(paths) != 1 || strings.Join(paths[0].Nodes, ">") != "gateway>worker" {
		t.Fatalf("got %v", routes(paths))
	}
}

// The same input read twice has to produce the same document.
func TestFoldingIsDeterministic(t *testing.T) {
	doc := `{"version":"1","spans":[
		{"trace_id":"t1","service":"a"},
		{"trace_id":"t1","service":"c","parent_service":"a"},
		{"trace_id":"t1","service":"b","parent_service":"a"},
		{"trace_id":"t2","service":"a"},
		{"trace_id":"t2","service":"b","parent_service":"a"}
	]}`
	first, _ := walks(t, doc)
	for range 5 {
		again, _ := walks(t, doc)
		if strings.Join(routes(again), "|") != strings.Join(routes(first), "|") {
			t.Fatalf("%v then %v", routes(first), routes(again))
		}
	}
}
