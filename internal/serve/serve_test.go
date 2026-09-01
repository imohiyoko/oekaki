package serve

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A page as this tool renders one: the graph is what a layout lands on and
// what an overlay changes, so the fixture has to be a document core can read
// rather than something merely shaped like one.
const graph = `{"version":"0.5","axes":[{"id":"network"}],` +
	`"nodes":[{"id":"a","type":"aws_vpc","name":"a"},{"id":"b","type":"aws_vpc","name":"b"}],` +
	`"edges":[],"groups":[{"id":"g","axis":"network","type":"aws_region","label":"g"}]}`

const page = `<!doctype html><html><head></head><body data-mode="read">
<script type="application/json" id="oekaki-graph">` + graph + `</script>
</body></html>`

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func layoutFor(ids ...string) string {
	var b strings.Builder
	b.WriteString(`{"kind":"oekaki.layout","version":"0.2","nodes":[`)
	for i, id := range ids {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"` + id + `","x":1,"y":2}`)
	}
	b.WriteString(`],"claim":{"origin":"human"}}`)
	return b.String()
}

// What a page says it was built from is read from the page, because a listing
// already has its bytes and a self-contained page has no graph beside it.
func TestAPageCarriesWhatItWasBuiltFrom(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "core.html"),
		strings.Replace(page, `data-mode="read"`,
			`data-mode="read" data-inputs="checkout, payments"`, 1))
	write(t, filepath.Join(root, "plain.html"), page)
	write(t, filepath.Join(root, "empty.html"),
		strings.Replace(page, `data-mode="read"`, `data-mode="read" data-inputs=""`, 1))

	pages, err := Pages(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, p := range pages {
		got[p.Rel] = p.Inputs
	}
	if want := []string{"checkout", "payments"}; !slices.Equal(got["core.html"], want) {
		t.Errorf("core.html was built from %v, want %v", got["core.html"], want)
	}
	// Both of these mean "did not say", and neither may become a name: an
	// empty attribute splits into one empty string, and narrowing by "" is
	// not a question anybody asked.
	if len(got["plain.html"]) != 0 {
		t.Errorf("a page with no attribute named %v", got["plain.html"])
	}
	if len(got["empty.html"]) != 0 {
		t.Errorf("an empty attribute named %v", got["empty.html"])
	}
}

// A stylesheet somebody supplied is written into the page above the body tag,
// and selecting on a data attribute of the body is this tool's own idiom. What
// the page was built from is what the graph said, not what a rule above it
// happens to spell.
func TestAStylesheetCannotSayWhereAPageCameFrom(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "core.html"),
		`<!doctype html><html><head><style>`+
			`body[data-inputs="ghost"] { color: red; }`+
			`</style></head><body data-mode="read" data-inputs="checkout">`+
			`<script type="application/json" id="oekaki-graph">`+graph+`</script></body></html>`)

	pages, err := Pages(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"checkout"}; !slices.Equal(pages[0].Inputs, want) {
		t.Errorf("read %v, want %v — a stylesheet was believed over the graph", pages[0].Inputs, want)
	}
}

// Only the body tag is read, so nothing further down the document can answer
// for it either — and a page that has no attribute is not a reason to read the
// rest of a self-contained one.
func TestNothingBelowTheBodyTagAnswersForIt(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "core.html"),
		`<!doctype html><html><head></head><body data-mode="read">`+
			`<script type="application/json" id="oekaki-graph">`+graph+`</script>`+
			`<!-- data-inputs="ghost" --></body></html>`)

	pages, err := Pages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages[0].Inputs) != 0 {
		t.Errorf("something below the body tag answered: %v", pages[0].Inputs)
	}
}

// A name that arrived escaped has to come back as it was written, or it will
// not match the graph it came from.
func TestANameIsUnescapedOnTheWayBack(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "core.html"),
		strings.Replace(page, `data-mode="read"`,
			`data-mode="read" data-inputs="orders &amp; refunds"`, 1))

	pages, err := Pages(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"orders & refunds"}; !slices.Equal(pages[0].Inputs, want) {
		t.Errorf("read %v, want %v", pages[0].Inputs, want)
	}
}

// A directory can hold anything. Only the pages this tool rendered can carry a
// layout, so only those are offered one.
func TestOnlyPagesThisToolMadeAreListed(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "core.html"), page)
	write(t, filepath.Join(root, "notes.html"), "<html><body>hand written</body></html>")
	write(t, filepath.Join(root, "data.csv"), "a,b\n")

	pages, err := Pages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].Rel != "core.html" {
		t.Fatalf("expected only core.html, got %#v", pages)
	}
}

// The count is the point: a layout written for another graph still applies,
// and without the number nothing says how little of it landed.
func TestALayoutIsMeasuredAgainstThePageItWouldBeAppliedTo(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "core.html"), page)
	if err := Save(root, "core", "mine", []byte(layoutFor("a", "g", "gone"))); err != nil {
		t.Fatal(err)
	}

	got, err := Layouts(root, root, Page{Rel: "core.html", Name: "core"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one layout, got %#v", got)
	}
	l := got[0]
	// "g" is a group; a layout may place containers too.
	if l.Nodes != 3 || l.Placed != 2 || len(l.Missing) != 1 || l.Missing[0] != "gone" {
		t.Fatalf("measured wrong: %#v", l)
	}
}

// getElementById answers with the first match, so a second layout element
// would leave the old one winning — silently, and only for whoever opened the
// page twice.
func TestApplyingALayoutReplacesTheOneAlreadyThere(t *testing.T) {
	first, _ := Apply([]byte(page), Dressing{Layout: []byte(layoutFor("a"))})
	second, _ := Apply(first, Dressing{Layout: []byte(layoutFor("b"))})

	if n := strings.Count(string(second), `id="oekaki-layout"`); n != 1 {
		t.Fatalf("expected one layout element, found %d:\n%s", n, second)
	}
	// Look inside the layout element only. The graph beside it names the same
	// nodes, so searching the whole document would pass either way.
	inside := regexp.MustCompile(`(?s)id="oekaki-layout">(.*?)</script>`).FindStringSubmatch(string(second))
	if inside == nil {
		t.Fatalf("no layout element:\n%s", second)
	}
	if !strings.Contains(inside[1], `"id":"b"`) || strings.Contains(inside[1], `"id":"a"`) {
		t.Fatalf("the second layout did not replace the first: %s", inside[1])
	}
}

// A layout can contain the string that ends a script element. Left alone it
// would end the element early and put the rest of the document in the page.
func TestALayoutCannotCloseTheElementItSitsIn(t *testing.T) {
	doc := `{"kind":"oekaki.layout","version":"0.2","nodes":[{"id":"</script><b>","x":1,"y":2}],"claim":{"origin":"human"}}`
	applied, _ := Apply([]byte(page), Dressing{Layout: []byte(doc)})
	out := string(applied)

	if strings.Contains(out, "</script><b>") {
		t.Fatalf("the closing tag survived into the page:\n%s", out)
	}
}

// Saving is not only done by the browser, so the check cannot live there. A
// document that will not parse must not become the one a page is served with.
func TestOnlyALayoutCanBeSavedAsOne(t *testing.T) {
	root := t.TempDir()
	overlay := `{"kind":"oekaki.overlay","version":"0.1","assertions":[]}`
	if err := Save(root, "core", "mine", []byte(overlay)); err == nil {
		t.Fatal("an overlay was accepted as a layout")
	}
	if _, err := os.Stat(filepath.Join(root, Dir, "core", "mine.layout.json")); !os.IsNotExist(err) {
		t.Fatal("a refused document was written anyway")
	}
}

func TestALayoutNameCannotLeaveItsFolder(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"../escape", "a/b", "", "."} {
		if _, err := Path(root, "core", name); err == nil {
			t.Fatalf("%q was accepted as a layout name", name)
		}
	}
}

// A box drawn in the browser has two facts about it: that it exists, which
// only an overlay carries, and where it sits, which only a layout carries.
// Saving one without the other loses the box.
func TestABoxDrawnInThePageComesBackWithItsPlace(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "core.html"), page)
	claims := `{"kind":"oekaki.overlay","version":"0.1","metadata":{"origin":"human"},` +
		`"assertions":[{"assert":"node","subject":{"name":"drawn"},"name":"drawn"}]}`
	if err := SaveOverlay(root, "core", "mine", []byte(claims)); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, "core", "mine", []byte(layoutFor("asserted:name=drawn"))); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(root, "core.html"))
	if err != nil {
		t.Fatal(err)
	}
	layout, _ := Read(root, "core", "mine")
	overlay, _ := ReadOverlay(root, "core", "mine")
	out, err := Apply(body, Dressing{Layout: layout, Overlay: overlay})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), `"asserted:name=drawn"`) {
		t.Fatalf("the box is not in the graph the page carries:\n%s", out)
	}
	// And the count must agree: without the overlay the position lands
	// nowhere, and saying so would be wrong for a pair saved together.
	got, err := Layouts(root, root, Page{Rel: "core.html", Name: "core"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Placed != 1 || len(got[0].Missing) != 0 || !got[0].Paired {
		t.Fatalf("the pair was counted apart: %#v", got)
	}
}

// An overlay is a different claim from a layout, so it is checked as one.
func TestOnlyAnOverlayCanBeSavedAsOne(t *testing.T) {
	root := t.TempDir()
	if err := SaveOverlay(root, "core", "mine", []byte(layoutFor("a"))); err == nil {
		t.Fatal("a layout was accepted as an overlay")
	}
}

// A file where a folder of saved documents has to go is something wrong, not
// an empty folder — and it has to read that way on every platform.
//
// os.ReadDir reports ENOTDIR for this on Unix and a not-found error on
// Windows, so checking os.IsNotExist alone made this "nothing saved here" on
// Windows only. A listing then reports the page as having nothing saved
// instead of as unreadable, which is the fail-open direction: the condition
// asking for pages with nothing saved collects the ones it cannot read.
func TestAFileWhereSavedDocumentsGoIsNotAnEmptyFolder(t *testing.T) {
	pages, state := t.TempDir(), t.TempDir()
	at := Page{Rel: "core.html", Name: "core"}
	write(t, filepath.Join(pages, "core.html"), page)
	for _, dir := range []string{Dir, OverlayDir} {
		write(t, filepath.Join(state, dir, "core"), "in the way")
	}
	if _, err := Layouts(pages, state, at); err == nil {
		t.Error("Layouts called a file an empty folder")
	}
	if _, err := Overlays(state, at); err == nil {
		t.Error("Overlays called a file an empty folder")
	}

	// A folder nobody has written to yet is still the ordinary empty case.
	clean := t.TempDir()
	if got, err := Layouts(pages, clean, at); err != nil || len(got) != 0 {
		t.Errorf("an untouched store: %#v %v", got, err)
	}
}
