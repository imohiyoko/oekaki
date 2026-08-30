package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// estate is three groups on the account axis: two nodes in "one", one each in
// "two" and "three", with edges crossing in both directions.
func estate() *core.Graph {
	g := core.New()
	g.Axes = []core.Axis{{ID: "account", Label: "account"}}
	for _, id := range []string{"one", "two", "three"} {
		g.Groups = append(g.Groups, core.Group{ID: id, Axis: "account", Type: "account", Label: id})
	}
	add := func(id, group string) {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "thing", Name: id, Provider: "aws",
			Groups: map[string]string{"account": group}})
	}
	add("a1", "one")
	add("a2", "one")
	add("b1", "two")
	add("b2", "two")
	add("c1", "three")
	edge := func(from, to string) {
		g.Edges = append(g.Edges, core.Edge{From: from, To: to,
			Kind: core.EdgeIACRef, Relation: "remote_state"})
	}
	edge("a1", "a2") // inside
	edge("a1", "b1") // out
	edge("a1", "b2") // out, to a different member of the same group
	edge("a2", "b1") // out, from a different member of this one
	edge("c1", "a1") // in
	edge("b1", "c1") // neither end inside
	g.Normalize()
	return g
}

// Everything in the chosen group keeps its own box; everything else becomes
// one box per group. Cutting the outside away entirely would lose the thing
// people usually came to find out.
func TestTheChosenGroupKeepsItsMembersAndTheRestBecomeOneBoxEach(t *testing.T) {
	got, err := Focus(estate(), "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range got.Nodes {
		ids[n.ID] = true
	}
	for _, want := range []string{"a1", "a2", "two", "three"} {
		if !ids[want] {
			t.Fatalf("%q is missing: %#v", want, ids)
		}
	}
	if ids["b1"] || ids["b2"] || ids["c1"] {
		t.Fatalf("a node outside the group survived as itself: %#v", ids)
	}
	if len(got.Groups) != 1 || got.Groups[0].ID != "one" {
		t.Fatalf("%#v", got.Groups)
	}
}

// Once the far end is one box, two references from the same node to two
// things inside it are two lines between the same pair. Drawing both is a
// bundle of parallel arrows saying one thing.
func TestTwoReferencesThatBecomeTheSamePairBecomeOneArrow(t *testing.T) {
	got, err := Focus(estate(), "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range got.Edges {
		if e.From == "a1" && e.To == "two" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected one arrow, got %d: %#v", n, got.Edges)
	}
}

// Two different nodes reaching the same collapsed group are two different
// pairs, and folding those together would say that one thing depends on it
// when two do.
func TestDifferentNodesReachingTheSameGroupStayDifferentArrows(t *testing.T) {
	got, err := Focus(estate(), "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	var fromA1, fromA2 bool
	for _, e := range got.Edges {
		if e.To != "two" {
			continue
		}
		fromA1 = fromA1 || e.From == "a1"
		fromA2 = fromA2 || e.From == "a2"
	}
	if !fromA1 || !fromA2 {
		t.Fatalf("a1=%v a2=%v: %#v", fromA1, fromA2, got.Edges)
	}
}

// Folding the lines away without saying how many there were makes a heavily
// used neighbour look like a passing one.
func TestACollapsedBoxSaysHowMuchWasFoldedIntoIt(t *testing.T) {
	got, err := Focus(estate(), "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range got.Nodes {
		if n.ID != "two" {
			continue
		}
		// Three references were folded into this one box: a1 twice, a2 once.
		if n.Attrs["references"] != 3 {
			t.Fatalf("the folded references were not all counted: %#v", n.Attrs)
		}
		if n.Attrs["collapsed"] == nil {
			t.Fatalf("nothing says this box is not the whole story: %#v", n.Attrs)
		}
		return
	}
	t.Fatal("the collapsed box is not there")
}

// A drawing about one group has no business carrying the traffic between two
// others.
func TestEdgesThatTouchNeitherEndAreNotDrawn(t *testing.T) {
	got, err := Focus(estate(), "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Edges {
		if e.From == "two" && e.To == "three" {
			t.Fatalf("an edge between two outsiders survived: %#v", got.Edges)
		}
	}
}

// An edge pointing in has to survive as well as one pointing out, or the
// drawing answers only half the question.
func TestReferencesInBothDirectionsSurvive(t *testing.T) {
	got, err := Focus(estate(), "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	var out, in bool
	for _, e := range got.Edges {
		if e.From == "a1" && e.To == "two" {
			out = true
		}
		if e.From == "three" && e.To == "a1" {
			in = true
		}
	}
	if !out || !in {
		t.Fatalf("out=%v in=%v: %#v", out, in, got.Edges)
	}
}

func TestEdgesInsideTheGroupAreKeptAsThemselves(t *testing.T) {
	got, err := Focus(estate(), "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Edges {
		if e.From == "a1" && e.To == "a2" {
			return
		}
	}
	t.Fatalf("an edge inside the group was lost: %#v", got.Edges)
}

// A drawing of nothing and a drawing of a group with nothing in it look
// identical, so which one it is has to be said.
func TestFocusingOnAGroupThatIsNotThereSaysSo(t *testing.T) {
	if _, err := Focus(estate(), "account", "four"); err == nil {
		t.Fatal("a group nobody has was accepted")
	}
	if _, err := Focus(estate(), "account", ""); err == nil {
		t.Fatal("focusing on nothing was accepted")
	}
}

// An account is just a group. The same fold has to work on any axis, or this
// is one estate's feature wearing a general name.
func TestTheSameFoldWorksOnAnyAxis(t *testing.T) {
	g := core.New()
	g.Axes = []core.Axis{{ID: "network", Label: "network"}}
	g.Groups = []core.Group{
		{ID: "vpc-a", Axis: "network", Type: "vpc", Label: "a"},
		{ID: "vpc-b", Axis: "network", Type: "vpc", Label: "b"},
	}
	g.Nodes = []core.Node{
		{ID: "x", Type: "thing", Name: "x", Provider: "aws", Groups: map[string]string{"network": "vpc-a"}},
		{ID: "y", Type: "thing", Name: "y", Provider: "aws", Groups: map[string]string{"network": "vpc-b"}},
	}
	g.Edges = []core.Edge{{From: "x", To: "y", Kind: core.EdgeIACRef, Relation: "remote_state"}}
	g.Normalize()

	got, err := Focus(g, "network", "vpc-a")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range got.Nodes {
		ids[n.ID] = true
	}
	if !ids["x"] || !ids["vpc-b"] || ids["y"] {
		t.Fatalf("%#v", ids)
	}
}

// The graph coming out has to be one the renderers will take without any
// further checking, the same as one that came from a parser.
func TestWhatComesOutIsAValidGraph(t *testing.T) {
	got, err := Focus(estate(), "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("the folded graph does not validate: %v", err)
	}
	if _, err := got.MarshalIndent(); err != nil {
		t.Fatalf("the folded graph will not encode: %v", err)
	}
}

// Map iteration order must not reach the output, or two runs over the same
// input produce different files.
func TestTheSameGraphFoldsTheSameWayEveryTime(t *testing.T) {
	first, err := Focus(estate(), "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	want, err := first.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		again, err := Focus(estate(), "account", "one")
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

// Folding on the endpoints alone keeps whichever edge the input listed first
// and throws the rest away, so a pair joined by both a declared reference and
// an observed one comes out as one of the two, chosen by file order.
func TestFoldingAPairKeepsEachKindOfLineBetweenThem(t *testing.T) {
	g := estate()
	g.Edges = append(g.Edges, core.Edge{From: "a1", To: "b1", Kind: core.EdgeReachable})
	g.Normalize()

	got, err := Focus(g, "account", "one")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[core.EdgeKind]bool{}
	for _, e := range got.Edges {
		if e.From == "a1" && e.To == "two" {
			kinds[e.Kind] = true
		}
	}
	if !kinds[core.EdgeIACRef] || !kinds[core.EdgeReachable] {
		t.Fatalf("one of the two kinds was lost: %#v", got.Edges)
	}
}

// nested is a hierarchical axis: a network holding two subnets, each holding a
// machine, plus one machine directly in the network and one somewhere else.
func nested() *core.Graph {
	g := core.New()
	g.Axes = []core.Axis{{ID: "network", Label: "network"}}
	parent := "vpc-a"
	g.Groups = []core.Group{
		{ID: "vpc-a", Axis: "network", Type: "vpc", Label: "a"},
		{ID: "sub-1", Axis: "network", Type: "subnet", Label: "1", Parent: &parent},
		{ID: "sub-2", Axis: "network", Type: "subnet", Label: "2", Parent: &parent},
		{ID: "vpc-b", Axis: "network", Type: "vpc", Label: "b"},
	}
	add := func(id, path string) {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "thing", Name: id, Provider: "aws",
			Groups: map[string]string{"network": path}})
	}
	add("direct", "vpc-a")
	add("deep-1", "vpc-a/sub-1")
	add("deep-2", "vpc-a/sub-2")
	add("elsewhere", "vpc-b")
	g.Edges = []core.Edge{
		{From: "direct", To: "deep-1", Kind: core.EdgeIACRef, Relation: "remote_state"},
		{From: "deep-1", To: "elsewhere", Kind: core.EdgeIACRef, Relation: "remote_state"},
	}
	g.Normalize()
	return g
}

// A node records the whole path down to it, not the id of the group it sits
// in, so a machine inside a subnet inside a network reads "vpc/subnet".
// Comparing that against "vpc" matches nothing, and focusing on any container
// that has containers of its own would fold its own contents away as though
// they belonged to somebody else.
func TestFocusingOnAContainerKeepsWhatIsNestedInsideIt(t *testing.T) {
	got, err := Focus(nested(), "network", "vpc-a")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range got.Nodes {
		ids[n.ID] = true
	}
	for _, want := range []string{"direct", "deep-1", "deep-2"} {
		if !ids[want] {
			t.Fatalf("%q is inside the focused network and was folded away: %#v", want, ids)
		}
	}
	if ids["elsewhere"] {
		t.Fatalf("something outside survived as itself: %#v", ids)
	}
}

// An edge between two things that are both inside must stay an edge between
// them, not become a line to a box standing in for their own container.
func TestAnEdgeBetweenANetworkAndItsOwnSubnetIsNotFolded(t *testing.T) {
	got, err := Focus(nested(), "network", "vpc-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Edges {
		if e.From == "direct" && e.To == "deep-1" {
			return
		}
	}
	t.Fatalf("an edge inside the focus was folded: %#v", got.Edges)
}

// A box per subnet of somebody else's network is not one box; it is the
// tangle this view exists to fold away.
func TestWhatIsOutsideFoldsToItsOutermostContainer(t *testing.T) {
	g := nested()
	g.Nodes = append(g.Nodes, core.Node{ID: "other-deep", Type: "thing", Name: "other-deep",
		Provider: "aws", Groups: map[string]string{"network": "vpc-b/sub-9"}})
	sub9 := "vpc-b"
	g.Groups = append(g.Groups, core.Group{ID: "sub-9", Axis: "network", Type: "subnet",
		Label: "9", Parent: &sub9})
	g.Edges = append(g.Edges, core.Edge{From: "direct", To: "other-deep",
		Kind: core.EdgeIACRef, Relation: "remote_state"})
	g.Normalize()

	got, err := Focus(g, "network", "vpc-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range got.Nodes {
		if n.ID == "vpc-b/sub-9" || n.ID == "sub-9" {
			t.Fatalf("a subnet of another network got its own box: %#v", got.Nodes)
		}
	}
	var found bool
	for _, n := range got.Nodes {
		if n.ID == "vpc-b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the other network is not there at all: %#v", got.Nodes)
	}
}
