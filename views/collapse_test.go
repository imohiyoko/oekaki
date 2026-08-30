package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// Every group becomes one box and the references between two of them become
// one line, or the folded drawing is the same tangle at a different scale.
func TestEveryGroupBecomesOneBoxAndEachPairOneLine(t *testing.T) {
	got, err := Collapse(estate(), "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range got.Nodes {
		ids[n.ID] = true
	}
	for _, want := range []string{"one", "two", "three"} {
		if !ids[want] {
			t.Fatalf("%q is missing: %#v", want, ids)
		}
	}
	if ids["a1"] {
		t.Fatalf("a member survived as its own box: %#v", ids)
	}
	n := 0
	for _, e := range got.Edges {
		if e.From == "one" && e.To == "two" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected one line between the pair, got %d: %#v", n, got.Edges)
	}
}

// Ten references and one look identical once they are folded, and they are not
// the same risk when somebody proposes deleting the thing at the far end.
func TestTheLineCarriesHowManyReferencesItStandsFor(t *testing.T) {
	got, err := Collapse(estate(), "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Edges {
		if e.From == "one" && e.To == "two" {
			if e.Attrs["references"] != 3 {
				t.Fatalf("expected the three references to be counted: %#v", e.Attrs)
			}
			if e.Attrs["examples"] == nil {
				t.Fatalf("nothing says which references these were: %#v", e.Attrs)
			}
			return
		}
	}
	t.Fatalf("the line is not there: %#v", got.Edges)
}

// A line from a box to itself says nothing and draws badly, but "this one is
// mostly self-contained" is worth being able to see.
func TestReferencesInsideAGroupBecomeANumberNotALoop(t *testing.T) {
	got, err := Collapse(estate(), "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Edges {
		if e.From == e.To {
			t.Fatalf("a loop was drawn: %#v", e)
		}
	}
	for _, n := range got.Nodes {
		if n.ID == "one" {
			if n.Attrs["internal_reference"] != 1 {
				t.Fatalf("the reference inside the group was lost: %#v", n.Attrs)
			}
			if n.Attrs["members"] != 2 {
				t.Fatalf("how many are in there was lost: %#v", n.Attrs)
			}
			return
		}
	}
	t.Fatal("the box is not there")
}

// A big estate has a long tail of single references that are real and are not
// what anybody is looking at.
func TestALineBelowTheThresholdIsNotDrawn(t *testing.T) {
	got, err := Collapse(estate(), "account", 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Edges {
		if e.Attrs["references"].(int) < 3 {
			t.Fatalf("a line under the threshold survived: %#v", e)
		}
	}
	var heavy bool
	for _, e := range got.Edges {
		if e.From == "one" && e.To == "two" {
			heavy = true
		}
	}
	if !heavy {
		t.Fatalf("the heaviest line was dropped too: %#v", got.Edges)
	}
}

// A box left with no lines and nothing inside it is not evidence of anything,
// and a page of them hides the ones that are.
func TestAGroupWithNothingLeftToSayIsNotDrawn(t *testing.T) {
	got, err := Collapse(estate(), "account", 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range got.Nodes {
		if n.ID == "three" {
			t.Fatalf("a group with only a dropped line survived: %#v", got.Nodes)
		}
	}
}

// Guessing which group an ungrouped node belongs to would invent a dependency.
func TestAnEdgeWithNoGroupAtOneEndIsNotPlaced(t *testing.T) {
	g := estate()
	g.Nodes = append(g.Nodes, core.Node{ID: "loose", Type: "thing", Name: "loose", Provider: "aws"})
	g.Edges = append(g.Edges, core.Edge{From: "loose", To: "a1", Kind: core.EdgeIACRef, Relation: "remote_state"})
	g.Normalize()

	got, err := Collapse(g, "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Edges {
		if e.From == "loose" || e.To == "loose" {
			t.Fatalf("an ungrouped end was placed anyway: %#v", e)
		}
	}
}

func TestAnAxisTheGraphDoesNotHaveIsSaidOutLoud(t *testing.T) {
	if _, err := Collapse(estate(), "sideways", 0); err == nil {
		t.Fatal("an axis nobody has was accepted")
	}
}

// Two runs over the same graph have to fold to the same bytes.
func TestTheSameGraphCollapsesTheSameWayEveryTime(t *testing.T) {
	first, err := Collapse(estate(), "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	want, err := first.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		again, err := Collapse(estate(), "account", 0)
		if err != nil {
			t.Fatal(err)
		}
		got, err := again.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("run %d differed", i)
		}
	}
}

func TestWhatComesOutOfCollapseIsAValidGraph(t *testing.T) {
	got, err := Collapse(estate(), "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("the folded graph does not validate: %v", err)
	}
}
