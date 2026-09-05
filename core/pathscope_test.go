package core

import "testing"

// A scope renames every id in the document. A route is a list of ids and a
// reading about one names it by an encoding of those ids, so both have to be
// rewritten — otherwise the document describes routes through boxes that no
// longer exist, and readings about routes it does not carry.
func TestAScopeRenamesRoutesAndWhatWasMeasuredAboutThem(t *testing.T) {
	g := pathGraph()
	g.Paths = []Path{{Nodes: []string{"gateway", "checkout"}, Kind: EdgeObserved}}
	value := 12.0
	g.Observations = []Observation{
		{Subject: PathKey([]string{"gateway", "checkout"}), Metric: "path_requests", Value: &value},
		{Subject: "gateway", Metric: "request_duration", Value: &value},
	}
	g.Normalize()

	g.ApplyScope("shop")
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatalf("a scoped document does not hold together: %v", err)
	}

	if got := g.Paths[0].Nodes[0]; got != "shop:gateway" {
		t.Fatalf("the route still walks %q", got)
	}
	want := PathKey([]string{"shop:gateway", "shop:checkout"})
	if got := g.Observations[0].Subject; got != want {
		t.Fatalf("the reading is about %q, want %q", got, want)
	}
	if got := g.Observations[1].Subject; got != "shop:gateway" {
		t.Fatalf("a reading about a node is about %q", got)
	}
}

// A path key is an encoding of several ids, not an id. A scope in front of the
// whole string is neither.
func TestQualifyingASubjectRebuildsAPathKey(t *testing.T) {
	scoped := func(id string) string { return "a:" + id }

	key := PathKey([]string{"one", "two"})
	got, isPath := QualifySubject(key, scoped)
	if !isPath {
		t.Fatal("a path key was not recognised as one")
	}
	if want := PathKey([]string{"a:one", "a:two"}); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if nodes, ok := ParsePathKey(got); !ok || nodes[0] != "a:one" {
		t.Fatalf("the rewritten key does not parse: %v", nodes)
	}

	if got, isPath := QualifySubject("plain-id", scoped); isPath || got != "plain-id" {
		t.Fatalf("an ordinary subject was treated as a route: %q", got)
	}
}

// The fold keeps the first entry, so the order decides which claim survives.
// Ordering by the origin's spelling puts "ai" ahead of "human"; edges rank
// them human, ai, parser, and a route is not a different kind of claim.
func TestTheBestClaimSurvivesAFold(t *testing.T) {
	g := pathGraph()
	g.Paths = []Path{
		{Nodes: []string{"gateway", "checkout"}, Kind: EdgeObserved, Claim: &Claim{Origin: OriginAI, Author: "model"}},
		{Nodes: []string{"gateway", "checkout"}, Kind: EdgeObserved, Claim: &Claim{Origin: OriginHuman, Author: "operator"}},
	}
	g.Normalize()

	if len(g.Paths) != 1 {
		t.Fatalf("got %d paths, want one", len(g.Paths))
	}
	if g.Paths[0].Claim == nil || g.Paths[0].Claim.Origin != OriginHuman {
		t.Fatalf("the surviving claim is %#v, want the human one", g.Paths[0].Claim)
	}
}
