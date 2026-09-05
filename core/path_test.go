package core

import "testing"

// A key is how an observation names a route, so it has to survive node ids
// containing whatever a resource address contains — including the separator.
func TestPathKeyRoundTripsAwkwardIDs(t *testing.T) {
	nodes := []string{"module.shop.aws_ecs_service.api", "svc:a.b.c", "path:not-a-key"}
	got, ok := ParsePathKey(PathKey(nodes))
	if !ok {
		t.Fatalf("%q did not parse back", PathKey(nodes))
	}
	if len(got) != len(nodes) {
		t.Fatalf("got %v, want %v", got, nodes)
	}
	for i := range nodes {
		if got[i] != nodes[i] {
			t.Fatalf("got %v, want %v", got, nodes)
		}
	}
}

// Anything that is not one of these keys has to be refused rather than
// half-decoded, or an id that happens to start with the prefix would be read
// as a route nobody wrote down.
func TestOnlyAPathKeyParsesAsOne(t *testing.T) {
	for _, key := range []string{
		"", "path:", "path:x", "node:a.b", "path:" + "!!!" + ".b",
		PathKey([]string{"a"}),            // one participant is not a path
		"path:" + "YQ" + "." + "Yg" + ".", // a trailing empty component
	} {
		if nodes, ok := ParsePathKey(key); ok {
			t.Errorf("%q parsed as a path: %v", key, nodes)
		}
	}
}

func pathGraph() *Graph {
	g := New()
	g.Nodes = []Node{
		{ID: "gateway", Type: "service", Name: "gateway"},
		{ID: "checkout", Type: "service", Name: "checkout"},
		{ID: "ledger", Type: "service", Name: "ledger"},
	}
	return g
}

// Traces are full of the same route walked again. A document that kept one
// entry per walk would grow with traffic rather than with the estate.
func TestTheSameWalkTwiceIsOnePath(t *testing.T) {
	g := pathGraph()
	g.Paths = []Path{
		{Nodes: []string{"gateway", "checkout"}, Kind: EdgeObserved, Attrs: map[string]any{"requests": 3.0}},
		{Nodes: []string{"gateway", "checkout"}, Kind: EdgeObserved, Attrs: map[string]any{"traces": 2.0}},
		{Nodes: []string{"gateway", "checkout"}, Kind: EdgeIACRef},
	}
	g.Normalize()

	if len(g.Paths) != 2 {
		t.Fatalf("got %d paths, want the observed one folded and the declared one kept: %#v", len(g.Paths), g.Paths)
	}
	// Folding keeps what each record carried, the way merging edges does.
	observed, ok := g.Path([]string{"gateway", "checkout"}, EdgeObserved)
	if !ok {
		t.Fatal("the observed path is gone")
	}
	if observed.Attrs["requests"] == nil || observed.Attrs["traces"] == nil {
		t.Fatalf("the fold dropped what one of them said: %#v", observed.Attrs)
	}
	if _, ok := g.Path([]string{"gateway", "checkout"}, EdgeIACRef); !ok {
		t.Fatal("a declared route was folded into an observed one; they are different claims")
	}
}

// A route through something this document does not have is a dangling
// reference, and the same walk written twice under one kind is two records of
// one fact.
func TestAPathIsCheckedAgainstTheEstateItWalks(t *testing.T) {
	g := pathGraph()
	g.Paths = []Path{
		{Nodes: []string{"gateway", "nowhere"}, Kind: EdgeObserved},
		{Nodes: []string{"gateway"}, Kind: EdgeObserved},
		{Nodes: []string{"gateway", "checkout"}, Kind: "guessed"},
	}
	err := g.Validate()
	if err == nil {
		t.Fatal("a path naming an unknown participant was accepted")
	}
	for _, want := range []string{"unknown participant", "at least two participants", "unknown kind"} {
		if !contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// A container does not call anything, and a path through one would make the
// key ambiguous about what it names.
func TestAContainerCannotBeAParticipant(t *testing.T) {
	g := pathGraph()
	g.Axes = []Axis{{ID: AxisNetwork}}
	g.Groups = []Group{{ID: "vpc", Axis: AxisNetwork, Type: "vpc", Label: "main"}}
	g.Paths = []Path{{Nodes: []string{"gateway", "vpc"}, Kind: EdgeObserved}}
	if err := g.Validate(); err == nil {
		t.Fatal("a path walked through a container")
	}
}

// A measurement about a route is an ordinary observation with an ordinary
// subject, which is the point of the key — every threshold, window and claim
// that already applies to observations applies to it without a second
// mechanism. It still has to name a route this document carries.
func TestAnObservationMayBeAboutAPath(t *testing.T) {
	g := pathGraph()
	g.Paths = []Path{{Nodes: []string{"gateway", "checkout"}, Kind: EdgeObserved}}
	value := 1200.0
	g.Observations = []Observation{
		{Subject: PathKey([]string{"gateway", "checkout"}), Metric: "requests", Value: &value, Window: "last-7d"},
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatalf("a reading about a route this document carries was refused: %v", err)
	}

	g.Observations[0].Subject = PathKey([]string{"gateway", "ledger"})
	if err := g.Validate(); err == nil {
		t.Fatal("a reading about a route nobody wrote down was accepted")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
