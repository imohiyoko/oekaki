package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A projection rebuilds every list in the document. One it forgot used to
// leave routes naming boxes the drawing no longer has, and the validator at
// the end of Apply refuses a document like that — so a graph carrying traces
// could not be projected at all.
func TestAViewProjectsTheRoutesToo(t *testing.T) {
	g := routes()
	g.Nodes = append(g.Nodes, core.Node{ID: "db:orders", Type: "aws_db_instance", Name: "orders"})
	g.Edges = append(g.Edges, core.Edge{From: "ledger", To: "db:orders", Kind: core.EdgeIACRef, Relation: "writes"})
	walked(g, "2026-09-01T00:00:00Z", 4, "gateway", "checkout", "ledger")
	g.Normalize()

	out, err := Apply(g, Options{Name: ER})
	if err != nil {
		t.Fatalf("a graph carrying routes could not be projected: %v", err)
	}
	// The route walks boxes this view drops, so it goes with them — and the
	// reading about it goes too, or the document names a route it does not
	// carry.
	if len(out.Paths) != 0 {
		t.Fatalf("a route survived a view that dropped its participants: %#v", out.Paths)
	}
	for _, o := range out.Observations {
		if _, isPath := core.ParsePathKey(o.Subject); isPath {
			t.Fatalf("a reading about a dropped route survived: %#v", o)
		}
	}
}

// A route every participant survives is kept, along with what was measured
// about it.
func TestAViewKeepsARouteItStillDraws(t *testing.T) {
	g := routes()
	for i := range g.Edges {
		g.Edges[i].Kind = core.EdgeObserved
	}
	walked(g, "2026-09-01T00:00:00Z", 4, "gateway", "checkout", "ledger")
	g.Normalize()

	out, err := Apply(g, Options{Name: ServiceDependency})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Paths) != 1 {
		t.Fatalf("got %d routes, want the one whose participants all survived", len(out.Paths))
	}
	found := false
	for _, o := range out.Observations {
		if o.Subject == core.PathKey([]string{"gateway", "checkout", "ledger"}) {
			found = true
		}
	}
	if !found {
		t.Fatal("the route survived and its reading did not")
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("the projection is not a valid document: %v", err)
	}
}

// How far a different walk got along a route says nothing about when this one
// last ran. Letting a prefix overwrite the full walk's reading reported a
// route walked yesterday as having stopped in January.
func TestAPartialWalkDoesNotAgeAFullOne(t *testing.T) {
	g := routes()
	g.Nodes = append(g.Nodes, core.Node{ID: "zzz", Type: "service", Name: "zzz"})
	g.Paths = DeclarePaths(g, DeclareOptions{})
	walked(g, "2026-09-01T00:00:00Z", 100, "gateway", "checkout", "ledger")
	walked(g, "2026-01-01T00:00:00Z", 1, "gateway", "checkout", "zzz")
	g.Normalize()

	got := found(t, g, PathOptions{Since: "2026-08-01T00:00:00Z"})
	if f, reported := got["gateway → checkout → ledger"]; reported {
		t.Fatalf("a route walked in September was reported as %s: %#v", f.Kind, f)
	}
}

// A moment is not a string. The collector writes nanoseconds and a relative
// cutoff is written to the second, and "." sorts before "Z" — so comparing the
// text puts a walk half a second past the cutoff before it.
func TestMomentsAreComparedAsMoments(t *testing.T) {
	g := routes()
	g.Paths = DeclarePaths(g, DeclareOptions{})
	walked(g, "2026-09-06T10:00:00.5Z", 3, "gateway", "checkout", "ledger")
	g.Normalize()

	if f, reported := found(t, g, PathOptions{Since: "2026-09-06T10:00:00Z"})["gateway → checkout → ledger"]; reported {
		t.Fatalf("a walk after the cutoff was reported as %s: %#v", f.Kind, f)
	}

	// And an offset is not a suffix either. 09:00+09:00 is midnight UTC, which
	// is before a 05:00 UTC cutoff — while the text of it sorts after.
	g.Observations[0].ObservedAt = "2026-09-06T09:00:00+09:00"
	f, reported := found(t, g, PathOptions{Since: "2026-09-06T05:00:00Z"})["gateway → checkout → ledger"]
	if !reported || f.Kind != Quiet {
		t.Fatalf("midnight UTC written as 09:00+09:00 was read as after a 05:00Z cutoff: %#v", f)
	}
}
