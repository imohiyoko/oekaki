package cli

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/authz"
	"github.com/imohiyoko/oekaki/catalog"
	"github.com/imohiyoko/oekaki/config"
	"github.com/imohiyoko/oekaki/internal/serve"
	"github.com/imohiyoko/oekaki/manage"
)

const servedGraph = `{"version":"0.5","nodes":[{"id":"a","type":"aws_instance","name":"a"}]}`

const servedPage = `<!doctype html><body data-mode="read">` +
	`<script type="application/json" id="oekaki-graph">` + servedGraph + `</script></body>`

const servedLayout = `{"kind":"oekaki.layout","version":"0.2",` +
	`"nodes":[{"id":"a","x":1,"y":2}],"claim":{"origin":"human"}}`

// testSite is a server over two fresh directories, in the one mode that runs.
func testSite(t *testing.T) *site {
	t.Helper()
	pages, state := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(pages, "core.html"), []byte(servedPage), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(t.TempDir(), "none"))
	if err != nil {
		t.Fatal(err)
	}
	return &site{pages: pages, state: state, cfg: cfg,
		store: manage.At(state), mode: authz.ModeOf("local")}
}

// enforcing is a site that actually authorizes, so that a guard can be seen
// working rather than only being present.
func enforcing(t *testing.T) *site {
	t.Helper()
	s := testSite(t)
	s.mode = authz.Mode{Auth: false, Enforce: true}
	s.cfg.Roles = authz.Policy{
		Roles: map[string][]authz.Rule{
			"viewer": {{Permission: authz.Read, Effect: authz.Allow}},
			"editor": {{Permission: authz.Read, Effect: authz.Allow},
				{Permission: authz.Write, Effect: authz.Allow}},
		},
	}
	if err := s.store.Grant("github:reader", []string{"viewer"}, manage.Actor{}, []string{"viewer", "editor"}); err != nil {
		t.Fatal(err)
	}
	return s
}

var (
	asReader   = map[string]string{"X-Actor": "github:reader"}
	asStranger = map[string]string{"X-Actor": "github:stranger"}
)

func ask(t *testing.T, s *site, method, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// Whoever did not say how they were running it must not end up serving without
// authentication. There is no identity provider yet, so every mode that wants
// one refuses to start rather than pretending to have it.
func TestServingRefusesToStartWithoutBeingToldHowItIsRunning(t *testing.T) {
	dir := t.TempDir()
	for _, mode := range []string{"", "saas", "enterprise", "prod"} {
		args := []string{dir}
		if mode != "" {
			args = append([]string{"--mode", mode}, args...)
		}
		got := run(t, "", append([]string{"serve"}, args...)...)
		if got.code == 0 {
			t.Fatalf("mode %q started", mode)
		}
		if !strings.Contains(got.stderr, "--mode local") {
			t.Fatalf("mode %q did not say how to run it anyway: %q", mode, got.stderr)
		}
	}
}

// The one mode that runs is also the one that has to stay off the network.
func TestServingRefusesAnAddressAStrangerCouldReach(t *testing.T) {
	dir := t.TempDir()
	got := run(t, "", "serve", "--mode", "local", "--addr", "0.0.0.0:8080", dir)
	if got.code == 0 {
		t.Fatal("it opened on every interface")
	}
	if !strings.Contains(got.stderr, "loopback") {
		t.Fatalf("%q", got.stderr)
	}
}

// The server is on loopback, which stops the network but not the tab next to
// it.
func TestAnotherPageInTheSameBrowserCannotDriveThisOne(t *testing.T) {
	s := testSite(t)
	for _, path := range []string{"/api/layouts/core/wide", "/api/defaults/core/wide", "/api/grants/github:x"} {
		got := ask(t, s, http.MethodPost, path, servedLayout, map[string]string{"Origin": "http://evil.example"})
		if got.Code != http.StatusForbidden {
			t.Fatalf("%s came back %d, expected 403", path, got.Code)
		}
	}
}

func TestAWriteFromThisPageIsAllowedThrough(t *testing.T) {
	s := testSite(t)
	got := ask(t, s, http.MethodPost, "/api/layouts/core/wide", servedLayout,
		map[string]string{"Origin": "http://example.com"})
	if got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// A caller's mistake and a server's failure are different things. A mistyped
// name coming back as 500 tells the person nothing they can act on.
func TestAskingForSomethingThatIsNotThereIsNotAServerFailure(t *testing.T) {
	s := testSite(t)
	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/api/defaults/core/imaginary", ""},
		{http.MethodPost, "/api/layouts/core/wide", "not json at all"},
		{http.MethodDelete, "/api/layouts/core/imaginary", ""},
		{http.MethodPost, "/api/grants/github:someone", `{"roles":["nosuchrole"]}`},
	}
	for _, c := range cases {
		got := ask(t, s, c.method, c.path, c.body, nil)
		if got.Code != http.StatusConflict {
			t.Fatalf("%s %s came back %d, expected 409: %s", c.method, c.path, got.Code, got.Body.String())
		}
		if strings.Contains(got.Body.String(), "refused:") {
			t.Fatalf("the message still carries its wrapper: %q", got.Body.String())
		}
	}
}

func TestAnEndpointThatDoesNotExistSaysSo(t *testing.T) {
	s := testSite(t)
	if got := ask(t, s, http.MethodPost, "/api/nonsense/x", "", nil); got.Code != http.StatusNotFound {
		t.Fatalf("%d", got.Code)
	}
}

// Promoting means the page comes out that way for everybody who did not ask
// for something else. If it only applied when named in the url, it would be a
// bookmark rather than a decision.
func TestAPromotedLayoutIsWhatAPlainRequestGets(t *testing.T) {
	s := testSite(t)
	if err := serve.Save(s.state, "core", "wide", []byte(servedLayout)); err != nil {
		t.Fatal(err)
	}

	plain := ask(t, s, http.MethodGet, "/core.html", "", nil)
	if strings.Contains(plain.Body.String(), "oekaki-layout") {
		t.Fatal("a layout was applied before anyone promoted one")
	}

	if got := ask(t, s, http.MethodPost, "/api/defaults/core/wide", "", nil); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	after := ask(t, s, http.MethodGet, "/core.html", "", nil)
	if !strings.Contains(after.Body.String(), `id="oekaki-layout"`) {
		t.Fatal("the promoted layout was not applied")
	}
}

// Taking the promotion back has to take the layout off the page.
func TestTakingBackAPromotionTakesTheLayoutOffThePage(t *testing.T) {
	s := testSite(t)
	if err := serve.Save(s.state, "core", "wide", []byte(servedLayout)); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, s, http.MethodPost, "/api/defaults/core/wide", "", nil); got.Code != http.StatusOK {
		t.Fatalf("%d", got.Code)
	}
	if got := ask(t, s, http.MethodDelete, "/api/defaults/core/", "", nil); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	after := ask(t, s, http.MethodGet, "/core.html", "", nil)
	if strings.Contains(after.Body.String(), "oekaki-layout") {
		t.Fatal("the layout survived being taken back")
	}
}

// Deleting the version that was promoted has to take the promotion with it,
// through the API as well as through the store.
func TestDeletingThePromotedVersionThroughTheApiAlsoUnpromotesIt(t *testing.T) {
	s := testSite(t)
	if err := serve.Save(s.state, "core", "wide", []byte(servedLayout)); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, s, http.MethodPost, "/api/defaults/core/wide", "", nil); got.Code != http.StatusOK {
		t.Fatalf("%d", got.Code)
	}
	if got := ask(t, s, http.MethodDelete, "/api/layouts/core/wide", "", nil); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if _, ok, _ := s.store.DefaultFor("core"); ok {
		t.Fatal("the promotion outlived the version it named")
	}
}

// What is saved outlives a generation and the pages do not, so the two
// directories have to be able to be different ones.
func TestWhatIsSavedDoesNotHaveToLiveWithThePages(t *testing.T) {
	s := testSite(t)
	if s.pages == s.state {
		t.Fatal("the fixture is not proving anything")
	}
	if got := ask(t, s, http.MethodPost, "/api/layouts/core/wide", servedLayout, nil); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.state, "layouts", "core", "wide.layout.json")); err != nil {
		t.Fatalf("it was not written to the state directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.pages, "layouts")); !os.IsNotExist(err) {
		t.Fatalf("something was written beside the pages: %v", err)
	}
}

// The management page is built on every request. One written when the things
// on it still existed tells lies for as long as somebody leaves the tab open.
func TestTheManagementPageIsBuiltFromWhatIsThereNow(t *testing.T) {
	s := testSite(t)
	if got := ask(t, s, http.MethodGet, "/manage", "", nil); strings.Contains(got.Body.String(), "wide") {
		t.Fatal("it listed something nobody saved")
	}
	if err := serve.Save(s.state, "core", "wide", []byte(servedLayout)); err != nil {
		t.Fatal(err)
	}
	got := ask(t, s, http.MethodGet, "/manage", "", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), "wide") {
		t.Fatal("it did not list what was just saved")
	}
	if ct := got.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("served as %q, which a browser will not render", ct)
	}
}

// The loud half of the missing-default pair has to reach the page, or somebody
// looking at a plain drawing has no way to find out why it is plain.
func TestThePageSaysWhenAPromotedVersionHasGone(t *testing.T) {
	s := testSite(t)
	if err := serve.Save(s.state, "core", "wide", []byte(servedLayout)); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Promote("core", "wide", manage.Actor{}); err != nil {
		t.Fatal(err)
	}
	path, _ := serve.Path(s.state, "core", "wide")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	got := ask(t, s, http.MethodGet, "/manage", "", nil)
	if !strings.Contains(got.Body.String(), "not saved any more") {
		t.Fatal("the page said nothing about a default that cannot be honoured")
	}
	// And the drawing still comes out, because a worse picture beats none.
	if page := ask(t, s, http.MethodGet, "/core.html", "", nil); page.Code != http.StatusOK {
		t.Fatalf("the page stopped being served: %d", page.Code)
	}
}

func TestTheRolesPageSaysWhenNothingIsBeingEnforced(t *testing.T) {
	s := testSite(t)
	got := ask(t, s, http.MethodGet, "/roles", "", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), "Nothing here is being enforced") {
		t.Fatal("a server that authorizes nobody did not say so")
	}
	// The permission names are fixed in the program, so they are shown rather
	// than left to be guessed at.
	for _, p := range authz.Catalog() {
		if !strings.Contains(got.Body.String(), p.About) {
			t.Fatalf("%q is not explained anywhere on the page", p.Name)
		}
	}
}

// A colour out of a configuration file is a value somebody in the deployment
// wrote, which is not the same as a value this program wrote.
func TestAConfiguredColourCannotCloseTheElementItSitsIn(t *testing.T) {
	s := testSite(t)
	s.cfg.Catalog.Theme = map[string]string{"ink": `red}</style><script>alert(1)</script>`}
	got := ask(t, s, http.MethodGet, "/manage", "", nil)
	if strings.Contains(got.Body.String(), "<script>alert(1)</script>") {
		t.Fatalf("a theme value escaped its stylesheet:\n%s", got.Body.String())
	}
}

// The names in a catalog are somebody else's words and reach the page as text.
func TestATitleOutOfTheCatalogIsShownAsText(t *testing.T) {
	s := testSite(t)
	s.cfg.Catalog.Items = append(s.cfg.Catalog.Items, catalogRule())
	got := ask(t, s, http.MethodGet, "/manage", "", nil)
	if strings.Contains(got.Body.String(), "<b>bold</b>") {
		t.Fatal("a configured title was rendered as markup")
	}
	if !strings.Contains(got.Body.String(), "&lt;b&gt;bold&lt;/b&gt;") {
		t.Fatalf("the title did not reach the page at all:\n%s", got.Body.String())
	}
}

func catalogRule() catalog.Rule {
	return catalog.Rule{Match: "core.html", Kind: "drawing", Title: "<b>bold</b>"}
}

// An item nobody annotated reads back blank, so a read error means the file is
// there and cannot be read — and the limit somebody wrote in it is exactly
// what cannot be seen. Treating that as unlimited is the one reading of a
// damaged restriction that must not be the default.
func TestARestrictionThatCannotBeReadDoesNotBecomeNoRestriction(t *testing.T) {
	s := testSite(t)
	path := filepath.Join(s.state, "meta", "core.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ask(t, s, http.MethodGet, "/core.html", "", nil)
	if got.Code != http.StatusForbidden {
		t.Fatalf("a damaged restriction was read as none: %d", got.Code)
	}
	if !strings.Contains(got.Body.String(), "cannot be read") {
		t.Fatalf("%q", got.Body.String())
	}
}

// A disk that will not take the write is not something the caller can fix.
// Answering 409 tells them to change what they sent, which will not help, and
// tells whoever reads the log that a person made a mistake when a machine did.
func TestAFailureAtThisEndIsNotReportedAsTheCallersMistake(t *testing.T) {
	s := testSite(t)
	// A file where the layouts directory has to go: the write cannot succeed
	// and nothing about the request is wrong.
	if err := os.WriteFile(filepath.Join(s.state, "layouts"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ask(t, s, http.MethodPost, "/api/layouts/core/wide", servedLayout, nil)
	if got.Code != http.StatusInternalServerError {
		t.Fatalf("came back %d, expected 500: %s", got.Code, got.Body.String())
	}
}

// The page exists to show what would be hidden before anybody switches
// enforcement on. Swallowing this shows an empty set of limits, which reads as
// "nobody loses anything" — the exact conclusion the page is there to prevent.
func TestTheRolesPageSaysWhenItCouldNotReadWhatPeopleWroteDown(t *testing.T) {
	s := testSite(t)
	// A file where the meta directory has to go.
	if err := os.WriteFile(filepath.Join(s.state, "meta"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ask(t, s, http.MethodGet, "/roles", "", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("%d", got.Code)
	}
	if !strings.Contains(got.Body.String(), "could not be read") {
		t.Fatalf("it showed an empty set of limits without saying so:\n%s", got.Body.String())
	}
}

// A page's graph is the page's data under another name. Authorizing it as
// "core.graph" consults whatever somebody wrote about an item that does not
// exist, so a limit on "core" does not apply and the whole graph can be
// fetched by exactly the person refused the page it belongs to.
func TestTheGraphOfAPageIsGuardedLikeThePage(t *testing.T) {
	s := enforcing(t)
	if _, err := s.store.Annotate("core", manage.Meta{ReadRoles: []string{"editor"}},
		manage.Actor{}, []string{"viewer", "editor"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.pages, "core.graph.json"), []byte(servedGraph), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := ask(t, s, http.MethodGet, "/core.html", "", asReader); got.Code != http.StatusForbidden {
		t.Fatalf("the page itself was not refused: %d", got.Code)
	}
	got := ask(t, s, http.MethodGet, "/core.graph.json", "", asReader)
	if got.Code != http.StatusForbidden {
		t.Fatalf("the graph came back %d to somebody refused the page: %s", got.Code, got.Body.String())
	}
}

// Reading covers seeing what is saved for a diagram, and this page is a list
// of exactly that.
func TestTheListOfSavedLayoutsAsksTheSameQuestionTheOtherPagesDo(t *testing.T) {
	s := enforcing(t)
	got := ask(t, s, http.MethodGet, "/layouts", "", asStranger)
	if got.Code != http.StatusForbidden {
		t.Fatalf("a caller with no roles enumerated what is saved: %d", got.Code)
	}
	if ok := ask(t, s, http.MethodGet, "/layouts", "", asReader); ok.Code != http.StatusOK {
		t.Fatalf("a reader was refused: %d %s", ok.Code, ok.Body.String())
	}
}

// A version whose file has gone falls back to the plain drawing on purpose,
// and StaleDefault says so. State that cannot be read is not that case:
// swallowing it makes a recorded decision appear to evaporate, on every
// request, with nothing saying why.
func TestStateThatCannotBeReadIsNotTheQuietFallback(t *testing.T) {
	s := testSite(t)
	if err := os.WriteFile(filepath.Join(s.state, "defaults.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ask(t, s, http.MethodGet, "/core.html", "", nil)
	if got.Code != http.StatusInternalServerError {
		t.Fatalf("a page came back %d with unreadable state behind it", got.Code)
	}
	if !strings.Contains(got.Body.String(), "cannot be read") {
		t.Fatalf("%q", got.Body.String())
	}
}

// The preview must not be able to say an item is visible to everyone while an
// actual request for that item is refused, which is what happens when one path
// skips an unreadable file and the other fails closed on it.
func TestThePreviewAndTheRealAnswerDoNotDisagree(t *testing.T) {
	s := enforcing(t)
	path := filepath.Join(s.state, "meta", "core.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The real answer refuses.
	if got := ask(t, s, http.MethodGet, "/core.html", "", asReader); got.Code != http.StatusForbidden {
		t.Fatalf("the page was served despite an unreadable limit: %d", got.Code)
	}
	// So the preview must not claim otherwise.
	page := ask(t, s, http.MethodGet, "/roles", "", asReader)
	if !strings.Contains(page.Body.String(), "could not be read") {
		t.Fatalf("the preview said nothing about the file it could not read:\n%s", page.Body.String())
	}
}

// The state directory sits beside the pages by default, which puts it inside
// the tree being handed out. Who holds which role, what was done, and what
// people wrote down — including who may see what — would otherwise be a plain
// GET away.
func TestWhatIsSavedIsNotHandedOutAsAPage(t *testing.T) {
	s := testSite(t)
	s.state = filepath.Join(s.pages, ".oekaki-state")
	s.store = manage.At(s.state)
	if err := s.store.Grant("github:someone", []string{"viewer"}, manage.Actor{}, []string{"viewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Annotate("core", manage.Meta{Note: "a secret"}, manage.Actor{}, nil); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/.oekaki-state/grants.json",
		"/.oekaki-state/journal.jsonl",
		"/.oekaki-state/meta/core.json",
	} {
		got := ask(t, s, http.MethodGet, path, "", nil)
		if got.Code == http.StatusOK {
			t.Fatalf("%s was served: %s", path, got.Body.String())
		}
	}
}

// A name the store will not take is a name no annotation can ever have been
// filed under, so there is no restriction to have failed to read. Refusing
// there turns a file with a space in its name into a 403 on a machine that
// authorizes nobody.
func TestAFileNameTheStoreWouldNotTakeIsStillServed(t *testing.T) {
	s := testSite(t)
	for _, name := range []string{"my file.html", strings.Repeat("a", 70) + ".html"} {
		if err := os.WriteFile(filepath.Join(s.pages, name), []byte(servedPage), 0o600); err != nil {
			t.Fatal(err)
		}
		got := ask(t, s, http.MethodGet, "/"+url.PathEscape(name), "", nil)
		if got.Code != http.StatusOK {
			t.Fatalf("%q came back %d on a server that authorizes nobody: %s",
				name, got.Code, got.Body.String())
		}
	}
}

// Showing a page's name, its saved versions and how much of each lands tells
// somebody refused that page most of what the limit was written to keep from
// them.
func TestAListingLeavesOutWhatTheReaderMayNotOpen(t *testing.T) {
	s := enforcing(t)
	if _, err := s.store.Annotate("core", manage.Meta{ReadRoles: []string{"editor"}},
		manage.Actor{}, []string{"viewer", "editor"}); err != nil {
		t.Fatal(err)
	}
	if err := serve.Save(s.state, "core", "wide", []byte(servedLayout)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/manage", "/layouts"} {
		got := ask(t, s, http.MethodGet, path, "", asReader)
		if got.Code != http.StatusOK {
			t.Fatalf("%s came back %d", path, got.Code)
		}
		if strings.Contains(got.Body.String(), "wide") {
			t.Fatalf("%s listed a saved version of a page the reader cannot open:\n%s",
				path, got.Body.String())
		}
	}
}
