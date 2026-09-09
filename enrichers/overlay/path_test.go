package overlay

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A route somebody wrote down. Until there was one, the declared side of the
// comparison could only be derived from references — and where there are no
// references to follow it was empty, which made every observed route read as
// unannounced.
func TestARouteCanBeWrittenDown(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"path",
	   "through":[{"name":"api","type":"aws_lb"},
	              {"name":"api","type":"aws_ecs_service"},
	              {"node":"aws_db_instance.orders"}],
	   "label":"checkout"}`), Options{})

	if len(g.Paths) != 1 {
		t.Fatalf("got %d routes: %#v", len(g.Paths), g.Paths)
	}
	p := g.Paths[0]
	want := []string{"aws_lb.api", "aws_ecs_service.api", "aws_db_instance.orders"}
	if len(p.Nodes) != len(want) {
		t.Fatalf("the walk is %v", p.Nodes)
	}
	for i := range want {
		if p.Nodes[i] != want[i] {
			t.Errorf("participant %d is %q, want %q", i, p.Nodes[i], want[i])
		}
	}
	if p.Label != "checkout" {
		t.Errorf("the route is called %q", p.Label)
	}
	if p.Claim == nil || p.Claim.Origin != core.OriginHuman || p.Claim.Author != "operator" {
		t.Errorf("the route is not signed by whoever wrote it: %#v", p.Claim)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("the enriched document does not hold together: %v", err)
	}
}

// What a person writes down is a claim about what may happen, which is the
// family the configuration's own references belong to.
func TestAWrittenRouteIsADeclaredOne(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"path",
	   "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]}`), Options{})

	if len(g.Paths) != 1 {
		t.Fatalf("got %d routes", len(g.Paths))
	}
	if g.Paths[0].Kind == core.EdgeObserved {
		t.Error("a route somebody wrote down landed on the observed side of the comparison")
	}
	if g.Paths[0].Kind != core.EdgeIACRef {
		t.Errorf("the route's kind is %q", g.Paths[0].Kind)
	}
}

// What did happen comes from something that watched it. Letting an overlay say
// otherwise would put a hand-written route on the observed side of the very
// comparison the entity exists for.
func TestAnOverlayCannotSayARouteWasWalked(t *testing.T) {
	body := doc(`
	  {"assert":"path","kind":"observed",
	   "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]}`)

	_, err := Parse([]byte(body), "test.json")
	if err == nil {
		t.Fatal("an overlay claimed something was observed")
	}
	if !strings.Contains(err.Error(), "collector") {
		t.Errorf("the error does not say where an observation comes from: %v", err)
	}
}

// One box is not a walk.
func TestARouteNeedsTwoParticipants(t *testing.T) {
	body := doc(`{"assert":"path","through":[{"node":"aws_lb.api"}]}`)
	if _, err := Parse([]byte(body), "test.json"); err == nil {
		t.Fatal("a route with one participant was accepted")
	}
}

// The walk is applied whole or not at all: a participant the policy drops ends
// the assertion, because a walk with a hop missing is a different walk and one
// that arrived that way would be compared as though somebody had declared it.
func TestAWalkWithAHopMissingIsNotApplied(t *testing.T) {
	g, r := apply(t, doc(`
	  {"assert":"path",
	   "through":[{"node":"aws_lb.api"},
	              {"name":"nothing-like-this"},
	              {"node":"aws_db_instance.orders"}]}`),
		Options{Unmatched: PolicyReport})

	if len(g.Paths) != 0 {
		t.Fatalf("a shortened walk was applied: %#v", g.Paths)
	}
	if r.r.Clean() {
		t.Error("the report says nothing went wrong")
	}
}

// A route is not a box, so the fields that rename one are refused here.
func TestARouteDoesNotCarryAResourceField(t *testing.T) {
	body := doc(`{"assert":"path","name":"renamed",
	   "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]}`)
	_, err := Parse([]byte(body), "test.json")
	if err == nil {
		t.Fatal("a route carrying a rename was accepted")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("the error does not say which field: %v", err)
	}
}
