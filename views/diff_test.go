package views

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func twoTier() *core.Graph {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork}}
	one := "vpc:main"
	g.Groups = []core.Group{
		{ID: "vpc:main", Axis: core.AxisNetwork, Type: "vpc", Label: "main"},
		{ID: "subnet:public", Axis: core.AxisNetwork, Type: "subnet", Label: "public", Parent: &one},
	}
	g.Nodes = []core.Node{
		{ID: "svc:api", Type: "service", Name: "api",
			Attrs:  map[string]any{"instance_type": "t3.micro"},
			Groups: map[string]string{core.AxisNetwork: "subnet:public"}},
		{ID: "db:orders", Type: "database", Name: "orders"},
	}
	g.Edges = []core.Edge{
		{From: "svc:api", To: "db:orders", Kind: core.EdgeIACRef, Relation: "reads"},
	}
	g.Normalize()
	return g
}

// at is the node with this id. Normalize sorts, so an index into the slice is
// not the name of anything.
func at(t *testing.T, g *core.Graph, id string) *core.Node {
	t.Helper()
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	t.Fatalf("no node %q", id)
	return nil
}

func group(t *testing.T, g *core.Graph, id string) *core.Group {
	t.Helper()
	for i := range g.Groups {
		if g.Groups[i].ID == id {
			return &g.Groups[i]
		}
	}
	t.Fatalf("no group %q", id)
	return nil
}

func changed(t *testing.T, before, after *core.Graph) map[string]Change {
	t.Helper()
	out, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Change{}
	for _, c := range out {
		by[c.What+" "+c.Kind+" "+c.Subject] = c
	}
	return by
}

// The ordinary case, and the reason the command exists: a reviewer wants the
// box that moved, not two pictures to play spot-the-difference with.
func TestADiffSaysWhatWasAddedRemovedAndChanged(t *testing.T) {
	before := twoTier()
	after := twoTier()
	at(t, after, "svc:api").Attrs["instance_type"] = "t3.large"
	kept := after.Nodes[:0]
	for _, n := range after.Nodes {
		if n.ID != "db:orders" {
			kept = append(kept, n)
		}
	}
	after.Nodes = append(kept, core.Node{ID: "bucket:assets", Type: "bucket", Name: "assets"})
	after.Edges = nil
	after.Normalize()

	by := changed(t, before, after)
	if _, ok := by["node added bucket:assets"]; !ok {
		t.Error("the new node is not reported added")
	}
	if _, ok := by["node removed db:orders"]; !ok {
		t.Error("the deleted node is not reported removed")
	}
	moved, ok := by["node changed svc:api"]
	if !ok {
		t.Fatalf("the changed node is not reported: %#v", by)
	}
	if len(moved.Fields) != 1 || moved.Fields[0].Field != "attr:instance_type" {
		t.Fatalf("the field that moved is %#v", moved.Fields)
	}
	if moved.Fields[0].From != `"t3.micro"` || moved.Fields[0].To != `"t3.large"` {
		t.Errorf("both sides are not on the line: %#v", moved.Fields[0])
	}
	if _, ok := by["edge removed "+core.EdgeKey("svc:api", "db:orders", core.EdgeIACRef, "reads")]; !ok {
		t.Error("the edge that went with the node is not reported removed")
	}
}

// Nothing changed is an empty list, not an absent one: a caller reading the
// JSON should not have to tell them apart.
func TestTheSameGraphTwiceIsNoChanges(t *testing.T) {
	out, err := Diff(twoTier(), twoTier())
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("an empty result is absent rather than empty")
	}
	if len(out) != 0 {
		t.Fatalf("a graph differs from itself: %#v", out)
	}
}

// An id is identity. A resource whose id changed is a removal and an addition,
// because the ids are what say two things are the same and they disagree —
// pairing them by similar names would hide a deletion and a creation behind a
// field change.
func TestARenamedIDIsARemovalAndAnAddition(t *testing.T) {
	before := twoTier()
	after := twoTier()
	at(t, after, "svc:api").ID = "svc:api-v2"
	after.Edges[0].From = "svc:api-v2"
	after.Normalize()

	by := changed(t, before, after)
	if _, ok := by["node removed svc:api"]; !ok {
		t.Error("the old id is not reported removed")
	}
	if _, ok := by["node added svc:api-v2"]; !ok {
		t.Error("the new id is not reported added")
	}
	for key := range by {
		if key == "node changed svc:api" || key == "node changed svc:api-v2" {
			t.Errorf("a rename was guessed at: %s", key)
		}
	}
}

// Measurements change on every collection by design. A diff that reported them
// would bury the box that moved under numbers that were always going to move.
func TestMeasurementsAreNotChanges(t *testing.T) {
	before := twoTier()
	after := twoTier()
	value := 41.0
	after.Observations = []core.Observation{{
		Subject: "svc:api", Metric: "request_rate", Value: &value,
		ObservedAt: "2026-09-05T00:00:00Z",
	}}
	at(t, after, "svc:api").Metrics = map[string]float64{"cpu_p95": 0.34}
	after.Normalize()

	out, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("a fresh collection reads as a change to the estate: %#v", out)
	}
}

// A conclusion drawn from measurements is not a measurement. A service that
// went blind is a change worth being told about.
func TestGoingBlindIsAChange(t *testing.T) {
	before := twoTier()
	after := twoTier()
	at(t, after, "svc:api").Coverage = &core.Coverage{State: core.CoverageBlind}
	after.Normalize()

	by := changed(t, before, after)
	c, ok := by["node changed svc:api"]
	if !ok {
		t.Fatalf("coverage moved and nothing said so: %#v", by)
	}
	if len(c.Fields) != 1 || c.Fields[0].Field != "coverage" {
		t.Fatalf("the field that moved is %#v", c.Fields)
	}
}

// Comparing documents about different estates reports every element of both as
// added and removed, which says nothing. It is refused instead.
func TestTwoEstatesAreNotADiff(t *testing.T) {
	before := twoTier()
	before.Metadata = &core.Metadata{Scope: "platform-prod"}
	after := twoTier()
	after.Metadata = &core.Metadata{Scope: "data-staging"}

	_, err := Diff(before, after)
	if err == nil {
		t.Fatal("two estates were compared as though they were one")
	}
	for _, want := range []string{"platform-prod", "data-staging"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
}

// Containers, routes and notes are compared too: they are drawn, and somebody
// acts on them.
func TestGroupsRoutesAndNotesAreCompared(t *testing.T) {
	before := twoTier()
	after := twoTier()
	group(t, after, "subnet:public").Label = "primary"
	after.Paths = []core.Path{{Nodes: []string{"svc:api", "db:orders"}, Kind: core.EdgeObserved}}
	after.Notes = []core.Note{{
		Subject: "svc:api", Text: "retries three times",
		Claim: &core.Claim{Origin: core.OriginHuman, Author: "operator"},
	}}
	after.Normalize()

	by := changed(t, before, after)
	if c, ok := by["group changed subnet:public"]; !ok {
		t.Errorf("a container's label moved and nothing said so: %#v", by)
	} else if c.Fields[0].Field != "label" {
		t.Errorf("the field that moved is %#v", c.Fields)
	}
	found := 0
	for key, c := range by {
		if c.What == OfPath && c.Kind == ChangeAdded {
			found++
		}
		if c.What == OfNote && c.Kind == ChangeAdded {
			found++
			if !strings.Contains(c.Label, "retries three times") {
				t.Errorf("a note's line does not say what it says: %q (%s)", c.Label, key)
			}
		}
	}
	if found != 2 {
		t.Errorf("the route and the note were not both reported: %#v", by)
	}
}

// Identical input produces identical output, which is the rule that makes a
// comparison meaningful at all — so the comparison itself has to keep it.
func TestADiffIsTheSameEveryTime(t *testing.T) {
	before := twoTier()
	after := twoTier()
	at(t, after, "svc:api").Attrs["instance_type"] = "t3.large"
	at(t, after, "svc:api").Name = "gateway"
	after.Normalize()

	first, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := Diff(twoTier(), after)
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != len(first) {
			t.Fatalf("two runs disagree on how much changed: %d and %d", len(first), len(again))
		}
		for i := range again {
			if again[i].Subject != first[i].Subject || again[i].Kind != first[i].Kind {
				t.Fatalf("the order is not stable at %d: %#v and %#v", i, first[i], again[i])
			}
			if len(again[i].Fields) != len(first[i].Fields) {
				t.Fatalf("the fields are not stable: %#v and %#v", first[i], again[i])
			}
			for j := range again[i].Fields {
				if again[i].Fields[j] != first[i].Fields[j] {
					t.Fatalf("the fields are not in a stable order: %#v", again[i].Fields)
				}
			}
		}
	}
}
