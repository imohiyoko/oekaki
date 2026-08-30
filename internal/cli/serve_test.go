package cli

import (
	"net/http"
	"net/http/httptest"
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
