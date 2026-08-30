package catalog

import "testing"

func sample() *Catalog {
	return &Catalog{
		Kinds: []Kind{{ID: "drawing", Label: "Drawings"}, {ID: "table", Label: "Tables"}},
		Items: []Rule{
			{Match: "core.html", Kind: "drawing", Title: "the whole thing", About: "everything at once"},
			{Match: "*.html", Kind: "drawing"},
			{Match: "*.csv", Kind: "table"},
			{Match: "*.tmp", Hidden: true},
		},
	}
}

// The rules are read the way they are written, top to bottom, so a specific
// one placed above a catch-all is the one that applies. Anybody arriving from
// a CODEOWNERS file will expect the opposite, so this has to be pinned.
func TestTheFirstRuleThatMatchesIsTheOneThatApplies(t *testing.T) {
	c := sample()
	if got := c.Describe("core.html"); got.Title != "the whole thing" {
		t.Fatalf("the specific rule did not win: %#v", got)
	}
	if got := c.Describe("other.html"); got.Kind != "drawing" || got.Title != "other.html" {
		t.Fatalf("the catch-all did not apply: %#v", got)
	}
}

// Adding a file to the pipeline must not make it invisible until somebody
// remembers to describe it, because that failure is silence.
func TestAFileNobodyDescribedStillAppears(t *testing.T) {
	got := sample().Describe("surprise.json")
	if got.Name != "surprise.json" || got.Title != "surprise.json" {
		t.Fatalf("%#v", got)
	}
}

// A kind is shown by its label, and a kind nobody labelled falls back to its
// own name rather than to nothing.
func TestAKindIsShownByItsLabel(t *testing.T) {
	c := sample()
	if got := c.Describe("core.html"); got.Label != "Drawings" {
		t.Fatalf("%#v", got)
	}
	c.Items = append([]Rule{{Match: "odd.html", Kind: "unlabelled"}}, c.Items...)
	if got := c.Describe("odd.html"); got.Label != "unlabelled" {
		t.Fatalf("%#v", got)
	}
}

func TestSomethingMarkedHiddenIsLeftOut(t *testing.T) {
	c := sample()
	if !c.Hidden("scratch.tmp") {
		t.Fatal("the rule did not hide it")
	}
	got := c.List([]string{"core.html", "scratch.tmp", "a.csv"})
	for _, e := range got {
		if e.Name == "scratch.tmp" {
			t.Fatalf("a hidden file was listed: %#v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("%#v", got)
	}
}

// The order rules are written in is the order things are shown in, so that
// putting the overview first is done by writing it first.
func TestTheOrderOfTheRulesIsTheOrderOnScreen(t *testing.T) {
	got := sample().List([]string{"z.csv", "other.html", "core.html"})
	want := []string{"core.html", "other.html", "z.csv"}
	if len(got) != len(want) {
		t.Fatalf("%#v", got)
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Fatalf("position %d is %q, expected %q: %#v", i, got[i].Name, want[i], got)
		}
	}
}

// A listing that changes order between runs for reasons nobody chose is a
// listing people stop trusting.
func TestFilesNobodyDescribedGoLastInAStableOrder(t *testing.T) {
	c := sample()
	first := names(c.List([]string{"b.json", "core.html", "a.json"}))
	if first[0] != "core.html" || first[1] != "a.json" || first[2] != "b.json" {
		t.Fatalf("%v", first)
	}
	for range 10 {
		if got := names(c.List([]string{"b.json", "core.html", "a.json"})); !same(got, first) {
			t.Fatalf("the order changed between runs: %v vs %v", got, first)
		}
	}
}

// A personal file sitting after the shared one has to be able to change the
// look without restating everything in it.
func TestALaterFileAddsRulesAndReplacesSingleValues(t *testing.T) {
	c := &Catalog{Title: "shared", Theme: map[string]string{"ink": "black", "page": "white"}}
	c.Merge(&Catalog{
		Title: "mine",
		Items: []Rule{{Match: "*.svg", Kind: "drawing"}},
		Theme: map[string]string{"ink": "navy"},
	})
	if c.Title != "mine" {
		t.Fatalf("the title was not replaced: %q", c.Title)
	}
	if c.Theme["ink"] != "navy" {
		t.Fatalf("the overridden value did not take: %#v", c.Theme)
	}
	if c.Theme["page"] != "white" {
		t.Fatalf("an untouched value was lost: %#v", c.Theme)
	}
	if len(c.Items) != 1 {
		t.Fatalf("%#v", c.Items)
	}
}

// Merging into a catalog with no theme at all must not panic on a nil map.
func TestAThemeCanArriveWhereThereWasNone(t *testing.T) {
	c := &Catalog{}
	c.Merge(&Catalog{Theme: map[string]string{"ink": "navy"}})
	if c.Theme["ink"] != "navy" {
		t.Fatalf("%#v", c.Theme)
	}
}

// Nothing configured is the state every deployment starts in, and it has to
// behave rather than crash.
func TestNoCatalogAtAllStillDescribesThings(t *testing.T) {
	var c *Catalog
	got := c.Describe("core.html")
	if got.Name != "core.html" || got.Title != "core.html" {
		t.Fatalf("%#v", got)
	}
	if c.Hidden("anything") {
		t.Fatal("a nil catalog hid something")
	}
}

// A pattern that will not compile must not take the whole listing with it.
func TestAPatternThatMakesNoSenseIsJustNoMatch(t *testing.T) {
	c := &Catalog{Items: []Rule{{Match: "[", Kind: "broken"}, {Match: "*", Kind: "fine"}}}
	if got := c.Describe("core.html"); got.Kind != "fine" {
		t.Fatalf("%#v", got)
	}
}

func names(in []Entry) []string {
	out := make([]string, 0, len(in))
	for _, e := range in {
		out = append(out, e.Name)
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Output is usually filed under a directory per generation, so the name
// reaching Describe is runs/abc123/core.html. path.Match does not cross a
// slash, so a rule written as "core.html" would silently describe nothing, and
// making everybody write the directory in asks them to know something they
// should not have to.
func TestARuleForAFileNameFindsItWhereverItIsFiled(t *testing.T) {
	c := sample()
	got := c.Describe("runs/abc123/core.html")
	if got.Title != "the whole thing" {
		t.Fatalf("a rule for the file name did not reach it: %#v", got)
	}
	if got.Kind != "drawing" {
		t.Fatalf("%#v", got)
	}
}

// A pattern that names a directory means that directory, and must not be
// matched against a bare file name.
func TestAPatternWithAPathStillMeansThePath(t *testing.T) {
	c := &Catalog{Items: []Rule{{Match: "shared/*.html", Kind: "shared"}}}
	if got := c.Describe("runs/a/core.html"); got.Kind == "shared" {
		t.Fatalf("a path pattern matched somewhere else: %#v", got)
	}
	if got := c.Describe("shared/core.html"); got.Kind != "shared" {
		t.Fatalf("%#v", got)
	}
}
