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

// Folding the kind away would report a reachability finding or an observation
// as a declared reference, which is the one distinction this program exists to
// keep.
func TestFoldingKeepsWhatKindOfLineItWas(t *testing.T) {
	g := estate()
	g.Edges = append(g.Edges, core.Edge{From: "a1", To: "b1", Kind: core.EdgeReachable})
	g.Normalize()

	got, err := Collapse(g, "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[core.EdgeKind]bool{}
	for _, e := range got.Edges {
		if e.From == "one" && e.To == "two" {
			kinds[e.Kind] = true
		}
	}
	if !kinds[core.EdgeIACRef] || !kinds[core.EdgeReachable] {
		t.Fatalf("the two kinds were folded into one: %#v", got.Edges)
	}
}

// least is a threshold on how many references something stands for, and it has
// to mean the same for a box as for a line. Keeping every group that has any
// reference of its own, however high the threshold, fills a drawing asked to
// show only the busy part with boxes nothing reaches — the picture the
// threshold was raised to escape.
func TestAGroupBusyOnlyWithItselfIsHeldToTheSameThreshold(t *testing.T) {
	g := estate()
	// "three" ends up with one internal reference and one line out.
	g.Edges = append(g.Edges, core.Edge{From: "c1", To: "c1", Kind: core.EdgeIACRef, Relation: "self"})
	g.Normalize()

	loose, err := Collapse(g, "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !has(loose.Nodes, "three") {
		t.Fatalf("nothing was filtered and a group still went missing: %#v", loose.Nodes)
	}

	tight, err := Collapse(g, "account", 3)
	if err != nil {
		t.Fatal(err)
	}
	if has(tight.Nodes, "three") {
		t.Fatalf("a group with one reference survived a threshold of three: %#v", tight.Nodes)
	}
}

// At zero nothing is being filtered, so a group nothing touches is still part
// of what exists and is drawn.
func TestAskingForNoFilteringDrawsEveryGroupThereIs(t *testing.T) {
	g := estate()
	g.Groups = append(g.Groups, core.Group{ID: "lonely", Axis: "account", Type: "account", Label: "lonely"})
	g.Nodes = append(g.Nodes, core.Node{ID: "d1", Type: "thing", Name: "d1", Provider: "aws",
		Groups: map[string]string{"account": "lonely"}})
	g.Normalize()

	got, err := Collapse(g, "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !has(got.Nodes, "lonely") {
		t.Fatalf("a group with members and no references was left out: %#v", got.Nodes)
	}
}

func has(nodes []core.Node, id string) bool {
	for _, n := range nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

// A line's count is per kind, so a group's own references have to be counted
// the same way. Adding them together lets a group joined to itself by one
// declared and one observed reference outrank a pair of groups joined by
// exactly the same two — and survive as a box with no lines while the pair it
// should have tied with vanishes.
func TestABoxAndALineAreHeldToTheThresholdTheSameWay(t *testing.T) {
	g := core.New()
	g.Axes = []core.Axis{{ID: "account", Label: "account"}}
	for _, id := range []string{"solo", "x", "y"} {
		g.Groups = append(g.Groups, core.Group{ID: id, Axis: "account", Type: "account", Label: id})
	}
	add := func(id, group string) {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "thing", Name: id, Provider: "aws",
			Groups: map[string]string{"account": group}})
	}
	add("s1", "solo")
	add("s2", "solo")
	add("x1", "x")
	add("y1", "y")
	// The same two references, once inside a group and once across a boundary.
	for _, k := range []core.EdgeKind{core.EdgeIACRef, core.EdgeReachable} {
		g.Edges = append(g.Edges, core.Edge{From: "s1", To: "s2", Kind: k})
		g.Edges = append(g.Edges, core.Edge{From: "x1", To: "y1", Kind: k})
	}
	g.Normalize()

	got, err := Collapse(g, "account", 2)
	if err != nil {
		t.Fatal(err)
	}
	if has(got.Nodes, "solo") {
		t.Fatalf("one reference of each kind outranked the same two across a boundary: %#v", got.Nodes)
	}
	if len(got.Edges) != 0 {
		t.Fatalf("a line under the threshold survived: %#v", got.Edges)
	}
}

// A container somebody declared and put nothing in is still one they declared,
// and an empty subnet is a normal thing for a parser to find.
func TestAGroupWithNothingInItIsStillAGroupThatExists(t *testing.T) {
	g := estate()
	g.Groups = append(g.Groups, core.Group{ID: "empty", Axis: "account", Type: "account", Label: "empty"})
	g.Normalize()

	loose, err := Collapse(g, "account", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !has(loose.Nodes, "empty") {
		t.Fatalf("nothing was filtered and a declared group went missing: %#v", loose.Nodes)
	}
	tight, err := Collapse(g, "account", 1)
	if err != nil {
		t.Fatal(err)
	}
	if has(tight.Nodes, "empty") {
		t.Fatalf("an empty group survived a threshold: %#v", tight.Nodes)
	}
}
