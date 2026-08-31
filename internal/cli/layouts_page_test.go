package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/internal/serve"
	"github.com/imohiyoko/oekaki/manage"
)

// A layout whose position lands on nothing this graph carries. The saved
// document is valid; it was written against a graph that has since changed,
// which is the ordinary way a layout goes stale.
const strayLayout = `{"kind":"oekaki.layout","version":"0.2",` +
	`"nodes":[{"id":"ghost","x":1,"y":2}],"claim":{"origin":"human"}}`

var listedPage = regexp.MustCompile(`<h2><a href="/([^"]+)"`)

// listed is the pages a rendering of /layouts actually shows.
func listed(t *testing.T, s *site, query string, headers map[string]string) []string {
	t.Helper()
	got := ask(t, s, http.MethodGet, "/layouts"+query, "", headers)
	if got.Code != http.StatusOK {
		t.Fatalf("/layouts%s came back %d: %s", query, got.Code, got.Body.String())
	}
	var out []string
	for _, m := range listedPage.FindAllStringSubmatch(got.Body.String(), -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// screened is three pages that differ in every way a condition can ask about.
//
//	core     annotated, a version somebody settled on, every position lands
//	billing  annotated differently, a version whose positions land nowhere
//	network  nothing written down, nothing saved
func screened(t *testing.T) *site {
	t.Helper()
	s := testSite(t)
	for _, name := range []string{"billing.html", "network.html"} {
		if err := os.WriteFile(filepath.Join(s.pages, name), []byte(servedPage), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.store.Annotate("core", manage.Meta{Tags: []string{"prod"},
		Maintainers: []string{"github:alice"}, Note: "the estate overview"}, manage.Actor{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Annotate("billing", manage.Meta{Tags: []string{"staging", "prod"},
		CreatedBy: "github:bob"}, manage.Actor{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := serve.Save(s.state, "core", "wide", []byte(servedLayout)); err != nil {
		t.Fatal(err)
	}
	if err := serve.Save(s.state, "billing", "old", []byte(strayLayout)); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Promote("core", "wide", manage.Actor{}); err != nil {
		t.Fatal(err)
	}
	return s
}

func same(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Somebody who asked for nothing gets what they got before any of this
// existed. A listing that quietly narrows itself is worse than one that is
// long, because the long one is at least honest about what is there.
func TestAskingForNothingListsEverything(t *testing.T) {
	s := screened(t)
	got := listed(t, s, "", nil)
	want := []string{"billing.html", "core.html", "network.html"}
	if !same(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Each condition is asked of what is attached to the page, not of the page.
func TestEachConditionNarrowsToWhatIsAttachedToThePage(t *testing.T) {
	s := screened(t)
	for _, c := range []struct {
		query string
		want  []string
		why   string
	}{
		{"?q=billing", []string{"billing.html"}, "text against the path"},
		{"?q=estate+overview", []string{"core.html"}, "text against a note somebody wrote"},
		{"?q=wide", []string{"core.html"}, "text against the name of a saved version"},
		{"?tag=staging", []string{"billing.html"}, "one tag"},
		{"?tag=PROD", []string{"billing.html", "core.html"}, "a tag, whatever case it was typed in"},
		{"?tag=prod&tag=staging", []string{"billing.html"}, "every tag, not any of them"},
		{"?who=alice", []string{"core.html"}, "part of a maintainer's name"},
		{"?who=bob", []string{"billing.html"}, "whoever wrote it down"},
		{"?state=promoted", []string{"core.html"}, "a version somebody settled on"},
		{"?state=plain", []string{"billing.html", "network.html"}, "no version settled on"},
		{"?fit=complete", []string{"core.html"}, "every position lands"},
		{"?fit=partial", []string{"billing.html"}, "positions that land nowhere"},
		{"?fit=none", []string{"network.html"}, "nothing saved at all"},
	} {
		if got := listed(t, s, c.query, nil); !same(got, c.want) {
			t.Errorf("%s (%s): got %v, want %v", c.query, c.why, got, c.want)
		}
	}
}

// A version somebody promoted whose file has gone is its own answer. Folding
// it into "settled" would file a page nobody has looked at since the file
// vanished in with the ones that are working as intended.
func TestAPromotionPointingAtNothingIsNotTheSameAsOneThatWorks(t *testing.T) {
	s := screened(t)
	if err := serve.Remove(s.state, "core", "wide"); err != nil {
		t.Fatal(err)
	}
	if got := listed(t, s, "?state=stale", nil); !same(got, []string{"core.html"}) {
		t.Fatalf("stale: %v", got)
	}
	if got := listed(t, s, "?state=promoted", nil); len(got) != 0 {
		t.Fatalf("a promotion pointing at nothing counted as settled: %v", got)
	}
}

// A misspelt condition shows everything rather than nothing. An empty listing
// reads as "there is nothing here", which would be a lie told by a typo.
func TestAConditionThisPageDoesNotKnowIsNotASilentEmptyListing(t *testing.T) {
	s := screened(t)
	for _, query := range []string{"?state=promotedd", "?fit=whatever", "?unknown=thing"} {
		if got := listed(t, s, query, nil); len(got) != 3 {
			t.Errorf("%s narrowed to %v", query, got)
		}
	}
}

// The rule that a page somebody may not open is not one whose saved versions
// they may read the names of is applied before any screening. A condition that
// happened to exclude it would be doing the right thing by accident.
func TestAScreeningCannotReachAPageTheReaderMayNotOpen(t *testing.T) {
	s := enforcing(t)
	if _, err := s.store.Annotate("core", manage.Meta{ReadRoles: []string{"editor"},
		Tags: []string{"prod"}}, manage.Actor{}, []string{"viewer", "editor"}); err != nil {
		t.Fatal(err)
	}
	if err := serve.Save(s.state, "core", "wide", []byte(servedLayout)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "?tag=prod", "?q=core"} {
		if got := listed(t, s, query, asReader); len(got) != 0 {
			t.Errorf("%s showed a page the reader may not open: %v", query, got)
		}
	}
}

// A screening is kept for one person and is not another's to see.
func TestAKeptScreeningBelongsToWhoeverKeptIt(t *testing.T) {
	s := screened(t)
	alice := map[string]string{"Cookie": ActorCookie + "=alice"}
	bob := map[string]string{"Cookie": ActorCookie + "=bob"}

	if got := ask(t, s, http.MethodPost, "/api/screens", `{"name":"prod","query":"?tag=prod"}`, alice); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}

	hers := ask(t, s, http.MethodGet, "/layouts", "", alice).Body.String()
	if !strings.Contains(hers, `href="/layouts?tag=prod"`) {
		t.Fatalf("she cannot see what she kept:\n%s", hers)
	}
	if his := ask(t, s, http.MethodGet, "/layouts", "", bob).Body.String(); strings.Contains(his, "tag=prod") {
		t.Fatalf("he can see what she kept:\n%s", his)
	}

	// And it is hers to drop.
	if got := ask(t, s, http.MethodDelete, "/api/screens/prod", "", alice); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if back := ask(t, s, http.MethodGet, "/layouts", "", alice).Body.String(); strings.Contains(back, "tag=prod") {
		t.Fatalf("forgetting it left it there:\n%s", back)
	}
}

// What comes out of the store is a string somebody typed. It is parsed back
// into the conditions this build knows and the link is rebuilt from those, so
// what a kept screening can do is bounded by what this page understands rather
// than by what was once written into a file.
func TestAKeptScreeningIsRebuiltFromWhatThisPageUnderstands(t *testing.T) {
	s := screened(t)
	who := manage.Actor{}
	if _, err := s.store.SaveScreen(who, "odd",
		`?q=core&bogus=%22%3E%3Cscript%3E&state=nonsense`); err != nil {
		t.Fatal(err)
	}
	body := ask(t, s, http.MethodGet, "/layouts", "", nil).Body.String()
	if !strings.Contains(body, `href="/layouts?q=core"`) {
		t.Fatalf("the conditions it did understand did not survive:\n%s", body)
	}
	for _, gone := range []string{"bogus", "nonsense", `"><script>`} {
		if strings.Contains(body, gone) {
			t.Fatalf("%q reached the page from a stored screening:\n%s", gone, body)
		}
	}
}

// A header is how a program says who it is and a cookie is how a person does.
// A stale cookie must not be able to rename a caller that named itself.
func TestTheHeaderBeatsTheCookie(t *testing.T) {
	s := screened(t)
	if _, err := s.store.SaveScreen(manage.Actor{Name: "github:reader"}, "byheader", "?tag=prod"); err != nil {
		t.Fatal(err)
	}
	both := map[string]string{"X-Actor": "github:reader", "Cookie": ActorCookie + "=somebodyelse"}
	if body := ask(t, s, http.MethodGet, "/layouts", "", both).Body.String(); !strings.Contains(body, "byheader") {
		t.Fatalf("the cookie renamed a caller that named itself:\n%s", body)
	}
}

// Saying your own name is not a change anybody else can see, and it is the one
// thing somebody refused everything has to be able to do to be granted a role.
func TestNamingYourselfNeedsNothingAndIsRecordedAsSelfAsserted(t *testing.T) {
	s := enforcing(t)
	got := ask(t, s, http.MethodPost, "/api/whoami", `{"name":"github:reader"}`, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("a caller with no roles could not name themselves: %d %s", got.Code, got.Body.String())
	}
	var set *http.Cookie
	for _, c := range got.Result().Cookies() {
		if c.Name == ActorCookie {
			set = c
		}
	}
	if set == nil || set.Value != "github:reader" {
		t.Fatalf("no name came back: %#v", got.Result().Cookies())
	}
	if !set.HttpOnly {
		t.Fatal("the name is readable by any script on the page")
	}

	// And the name it hands out is the one the rest of the server then uses.
	s2 := screened(t)
	if _, err := s2.store.SaveScreen(manage.Actor{Name: "github:reader"}, "mine", "?tag=prod"); err != nil {
		t.Fatal(err)
	}
	body := ask(t, s2, http.MethodGet, "/layouts", "",
		map[string]string{"Cookie": ActorCookie + "=github:reader"}).Body.String()
	if !strings.Contains(body, "mine") {
		t.Fatalf("the cookie did not name the caller:\n%s", body)
	}
}

// A name has to survive being put in a cookie and shown on a page.
func TestANameThatWouldBreakTheCookieIsRefused(t *testing.T) {
	s := screened(t)
	for _, name := range []string{"", "has space", "a;b", `"><script>`, strings.Repeat("a", 70)} {
		body := `{"name":` + quote(name) + `}`
		if got := ask(t, s, http.MethodPost, "/api/whoami", body, nil); got.Code != http.StatusBadRequest {
			t.Errorf("%q came back %d", name, got.Code)
		}
	}
	// A cookie carrying one anyway names nobody rather than being shown.
	body := ask(t, s, http.MethodGet, "/layouts", "",
		map[string]string{"Cookie": ActorCookie + "=" + `a"b`}).Body.String()
	if !strings.Contains(body, manage.Anonymous) {
		t.Fatalf("a name nothing would issue was taken from a cookie:\n%s", body)
	}
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
