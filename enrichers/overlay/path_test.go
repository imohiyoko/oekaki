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
//
// Both layers refuse it, and each says something the other cannot: the schema
// names the JSON path, so an editor can point at the line, and Validate says
// why in a sentence.
func TestAnOverlayCannotSayARouteWasWalked(t *testing.T) {
	body := doc(`
	  {"assert":"path","kind":"observed",
	   "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]}`)

	err := Parse2(t, body)
	if !strings.Contains(err.Error(), "/assertions/0/kind") {
		t.Errorf("the schema error does not point at the line: %v", err)
	}

	err = onlyValidate(t, Assertion{
		Assert: AssertPath, Kind: core.EdgeObserved,
		Through: []Selector{{"node": "a"}, {"node": "b"}},
	})
	if !strings.Contains(err.Error(), "collector") {
		t.Errorf("the reason does not say where an observation comes from: %v", err)
	}
}

// One box is not a walk. Refused by both layers, for the same reason as above.
func TestARouteNeedsTwoParticipants(t *testing.T) {
	err := Parse2(t, doc(`{"assert":"path","through":[{"node":"aws_lb.api"}]}`))
	if !strings.Contains(err.Error(), "/assertions/0/through") {
		t.Errorf("the schema error does not point at the walk: %v", err)
	}

	err = onlyValidate(t, Assertion{Assert: AssertPath, Through: []Selector{{"node": "a"}}})
	if !strings.Contains(err.Error(), "one box is not a walk") {
		t.Errorf("the reason does not say what a route needs: %v", err)
	}
}

// Parse2 is Parse, for the tests that are about being refused.
func Parse2(t *testing.T, body string) error {
	t.Helper()
	_, err := Parse([]byte(body), "test.json")
	if err == nil {
		t.Fatal("accepted")
	}
	return err
}

// onlyValidate reaches the checks the schema gets to first. They are not dead
// code: Document and Validate are exported, and a caller that builds one by
// hand — the viewer's export, a generator — never passes through the schema.
func onlyValidate(t *testing.T, a Assertion) error {
	t.Helper()
	d := &Document{Kind: "oekaki.overlay", Version: "0.1", Assertions: []Assertion{a}}
	err := d.Validate()
	if err == nil {
		t.Fatal("accepted")
	}
	return err
}

// The walk is applied whole or not at all, and a hop is never adopted —
// whatever the unmatched policy says.
//
// A route is about boxes that are already there. Adopting a mistyped hop would
// put a box nobody parsed in the middle of the walk, and the route would then
// be permanently unused while the real one stayed unannounced: the two failures
// this assertion exists to remove, manufactured from a typo, in silence.
func TestAHopIsNeverAdopted(t *testing.T) {
	for name, opts := range map[string]Options{
		"adopt":  {Unmatched: PolicyAdopt},
		"report": {Unmatched: PolicyReport},
	} {
		t.Run(name, func(t *testing.T) {
			g, r := apply(t, doc(`
			  {"assert":"path",
			   "through":[{"node":"aws_lb.api"},
			              {"name":"nothing-like-this"},
			              {"node":"aws_db_instance.orders"}]}`), opts)

			if len(g.Paths) != 0 {
				t.Fatalf("a walk through a box nobody parsed was applied: %#v", g.Paths)
			}
			for _, n := range g.Nodes {
				if strings.HasPrefix(n.ID, "asserted:") {
					t.Errorf("a hop was adopted as %q", n.ID)
				}
			}
			if r.r.Clean() {
				t.Error("the report says nothing went wrong")
			}
		})
	}
}

// A container does not call anything, which is why core refuses a path through
// one. Letting it through failed the whole command on a graph validation error
// that named neither the overlay nor the assertion — the graph was blamed for
// what the overlay said.
func TestAHopThatIsAContainerIsRefused(t *testing.T) {
	// Both ways of naming it: by id, and by the label a person would write.
	for name, hop := range map[string]string{
		"by id":    `{"group":"vpc:main"}`,
		"by label": `{"name":"private-a"}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := doc(`{"assert":"path","through":[{"node":"aws_lb.api"},` +
				hop + `,{"node":"aws_db_instance.orders"}]}`)
			d, err := Parse([]byte(body), "test.json")
			if err != nil {
				t.Fatal(err)
			}
			into := graph()
			into.Groups = append(into.Groups, core.Group{
				ID: "vpc:main", Axis: core.AxisNetwork, Type: "vpc", Label: "private-a",
			})
			into.Normalize()

			r, err := New([]*Document{d}, Options{}).Enrich(into)
			if err != nil {
				t.Fatal(err)
			}
			if len(into.Paths) != 0 {
				t.Fatalf("a walk through a container was applied: %#v", into.Paths)
			}
			// The whole point: the command used to fail here, on a graph
			// validation error that named neither the overlay nor the
			// assertion.
			if err := into.Validate(); err != nil {
				t.Fatalf("the graph was left in a state it cannot be in: %v", err)
			}
			if r.Clean() {
				t.Error("the report says nothing went wrong")
			}
		})
	}
}

// A key misspelled in a hop deserves the same sentence as one misspelled in a
// subject, rather than the schema's "additionalProperties not allowed".
func TestAMisspelledKeyInAHopSaysTheVocabulary(t *testing.T) {
	err := Parse2(t, doc(`
	  {"assert":"path","through":[{"svc":"api"},{"node":"aws_db_instance.orders"}]}`))
	for _, want := range []string{"unknown selector key", `"svc"`, "through[0]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not carry %q: %v", want, err)
		}
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

// Two selectors, one after the other, naming the same box. A request does not
// go from a box to itself, so this is a typo — and `a → b → a` is a real loop,
// which is why core allows repeats and why the check has to be here. Left
// alone it produces a route nothing can ever walk, permanently unused, in
// silence: the shape this whole assertion exists to stop being manufactured.
func TestAHopThatRepeatsTheOneBeforeItIsRefused(t *testing.T) {
	g, r := apply(t, doc(`
	  {"assert":"path",
	   "through":[{"name":"api","type":"aws_lb"},
	              {"node":"aws_lb.api"},
	              {"node":"aws_db_instance.orders"}]}`), Options{})

	if len(g.Paths) != 0 {
		t.Fatalf("a route through a box and then itself was applied: %#v", g.Paths)
	}
	if r.r.Clean() {
		t.Error("the report says nothing went wrong")
	}
}

// And a route that genuinely comes back through something it already went
// through is still a route. The check is about two hops in a row, not about
// repeats.
func TestARouteMayComeBackThroughSomething(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"path",
	   "through":[{"node":"aws_lb.api"},
	              {"node":"aws_db_instance.orders"},
	              {"node":"aws_lb.api"}]}`), Options{})

	if len(g.Paths) != 1 {
		t.Fatalf("a loop was refused: %#v", g.Paths)
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
}

// The same walk declared twice. Normalize folds paths that agree, keeping the
// better-ranked claim, and the second label goes with the one it dropped —
// silently, and differently depending on which origin each assertion carried,
// so `oekaki diff` would show the label flipping for no reason anybody wrote
// down.
func TestTheSameWalkDeclaredTwiceIsToldRatherThanFolded(t *testing.T) {
	g, r := apply(t, doc(`
	  {"assert":"path","label":"first",
	   "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]},
	  {"assert":"path","label":"second",
	   "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]}`), Options{})

	if len(g.Paths) != 1 {
		t.Fatalf("got %d routes: %#v", len(g.Paths), g.Paths)
	}
	// The first one wins, because it is the one that was applied — not the
	// one a claim rank happened to prefer.
	if g.Paths[0].Label != "first" {
		t.Errorf("the surviving route is called %q", g.Paths[0].Label)
	}
	if r.r.Clean() {
		t.Error("the second assertion vanished without a word")
	}
}

// The same walk under a different kind is a different route: the entity exists
// for the gap between what may happen and what did.
func TestTheSameWalkUnderAnotherKindIsAnotherRoute(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"path","kind":"iac_ref",
	   "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]},
	  {"assert":"path","kind":"reachable",
	   "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]}`), Options{})

	if len(g.Paths) != 2 {
		t.Fatalf("got %d routes: %#v", len(g.Paths), g.Paths)
	}
}

// A hop is not optional. checkSelector is silent about an empty selector,
// because an absent subject is an ordinary thing elsewhere — but an empty hop
// resolves to nothing and takes the whole route down with it, at apply time,
// for a reason nobody could see from the document.
//
// The schema says the same with minProperties, and both layers keep saying it
// for the reason the other checks here do: Document and Validate are exported,
// and a caller that builds one by hand never passes through the schema.
func TestAHopNeedsASelector(t *testing.T) {
	err := Parse2(t, doc(`{"assert":"path","through":[{},{"node":"aws_lb.api"}]}`))
	if !strings.Contains(err.Error(), "/assertions/0/through/0") {
		t.Errorf("the schema error does not point at the hop: %v", err)
	}

	err = onlyValidate(t, Assertion{
		Assert: AssertPath, Through: []Selector{{}, {"node": "b"}},
	})
	if !strings.Contains(err.Error(), "through[0]") || !strings.Contains(err.Error(), "names nothing") {
		t.Errorf("the reason does not say which hop is empty, or why it matters: %v", err)
	}
}

// Across runs nothing is dropped and Normalize's ordinary rule applies: the
// routes fold, and the better-ranked claim keeps it. Within one run the applier
// gets there first, which is the difference the docs have to state.
func TestAcrossRunsTheBetterClaimKeepsTheRoute(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"path","label":"written down",
	   "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]}`), Options{})
	if len(g.Paths) != 1 {
		t.Fatalf("got %d routes", len(g.Paths))
	}

	// A second run, over the graph the first one produced.
	guess, err := Parse([]byte(`{"kind":"oekaki.overlay","version":"0.1",
	  "metadata":{"origin":"ai","author":"assistant"},
	  "assertions":[{"assert":"path","label":"guessed","confidence":0.6,
	    "through":[{"node":"aws_lb.api"},{"node":"aws_db_instance.orders"}]}]}`), "guess.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New([]*Document{guess}, Options{}).Enrich(g); err != nil {
		t.Fatal(err)
	}
	if len(g.Paths) != 1 {
		t.Fatalf("the same walk became %d routes: %#v", len(g.Paths), g.Paths)
	}
	if g.Paths[0].Label != "written down" {
		t.Errorf("a guess took the route from the person who wrote it: %q", g.Paths[0].Label)
	}
}
