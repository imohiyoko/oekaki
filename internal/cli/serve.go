package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/imohiyoko/oekaki/authz"
	"github.com/imohiyoko/oekaki/config"
	"github.com/imohiyoko/oekaki/internal/serve"
	"github.com/imohiyoko/oekaki/manage"
)

// runServe hands out a directory of rendered pages, the layouts saved for
// them, and the decisions people made about both.
//
// How it runs is one choice, not several. In local mode it binds loopback,
// asks nobody who they are, and refuses nothing — the pages are yours and so
// is the machine. Any other mode means somebody else can reach it, which needs
// an identity provider, and there is not one yet, so those modes refuse to
// start rather than pretending.
//
// The mode has to be said out loud. Defaulting to local would put whoever did
// not think about it on the side where there is no authentication, and not
// thinking about it is the common case.
func runServe(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	addr := fs.String("addr", "127.0.0.1:8080", "address to listen on; loopback only")
	mode := fs.String("mode", "", "how this is running: "+strings.Join(authz.ModeNames(), ", "))
	configDir := fs.String("config", "", "directory of roles, catalog and conventions (also "+config.EnvConfig+")")
	stateDir := fs.String("state", "", "directory for what outlives a generation (also "+config.EnvState+")")
	if err := parse(fs, args); err != nil {
		return err
	}
	root := "."
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}
	if fs.NArg() > 1 {
		return errors.New("serve takes at most one directory")
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}

	how := authz.ModeOf(*mode)
	if how.Auth {
		named := *mode
		if named == "" {
			named = "unspecified"
		}
		return fmt.Errorf("--mode %s wants to know who is asking and nothing here can tell it yet.\n"+
			"  to run without that, say so: --mode local\n"+
			"  local binds loopback only and authorizes nobody", named)
	}
	if err := loopbackOnly(*addr); err != nil {
		return err
	}

	cfg, err := config.Load(config.Dir(*configDir))
	if err != nil {
		return err
	}
	state := config.StateDir(*stateDir, root)

	s := &site{
		pages: root,
		state: state,
		cfg:   cfg,
		store: manage.At(state),
		mode:  how,
	}

	srv := &http.Server{Addr: *addr, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stderr, "serving %s\n  state  %s\n  config %s\n"+
		"  http://%s/\n  http://%s/layouts   saved layouts\n  http://%s/manage    what is saved and what is current\n"+
		"  http://%s/roles     who may see what\n",
		root, state, cfg.Dir, ln.Addr(), ln.Addr(), ln.Addr(), ln.Addr())

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// loopbackOnly refuses to open the server anywhere a stranger could reach.
//
// This server writes files and shows whatever is in the directory. Both are
// fine on your own machine and neither is fine on a shared network, and the
// difference between the two is one flag nobody reads twice.
func loopbackOnly(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("--addr %q is not loopback: serve writes files and does not ask who is asking", addr)
	}
	return nil
}

// NameHeader carries the name a save was filed under back to whoever sent it.
//
// The body of that response is a sentence for a person. A name has to survive
// being read by a program — the page turns it into the url it reloads under,
// and a sentence that later gains a word would take the drawing with it.
//
// renderers/html/app.js reads this string. The two are far enough apart that
// nothing but a test holds them together; see the one that names them both.
const NameHeader = "Oekaki-Name"

type site struct {
	pages string
	state string
	cfg   *config.Config
	store *manage.Store
	mode  authz.Mode
}

// ActorCookie is where a browser keeps the name somebody typed for themselves.
//
// A header is how a program says who it is, and it is the only way a page
// fetched by a script can. A plain navigation cannot set one, so without this
// every person clicking around is the same nameless caller — which is fine
// while nothing is filed under a name, and stops being fine the moment
// something is.
const ActorCookie = "oekaki-actor"

// actorName is what somebody may call themselves. Colon is allowed because a
// subject is written provider:name and a person granted a role has to be able
// to type the name they were granted it under. Everything else is kept out so
// that a name cannot break the cookie it travels in.
var actorName = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._:-]{0,63}\z`)

// actor is who is asking.
//
// This is the only place that decides. Today it reads a header the caller
// filled in themselves, or a cookie they set themselves, which is worth
// exactly what it sounds like — hence the origin travelling with it, so that a
// record written now cannot later be mistaken for one an identity provider
// vouched for. When there is a provider, this function changes and nothing
// else does.
//
// The header wins. A program driving this said so explicitly on the request;
// the cookie is whatever the browser happened to be carrying, and a stale one
// must not be able to rename a caller that named itself.
func (s *site) actor(r *http.Request) manage.Actor {
	name := r.Header.Get("X-Actor")
	if name == "" {
		if c, err := r.Cookie(ActorCookie); err == nil && actorName.MatchString(c.Value) {
			name = c.Value
		}
	}
	return manage.Actor{Name: name, Origin: manage.Unverified}
}

// policy is the roles from configuration joined to the grants from state.
//
// It is assembled per request because the grants half changes while the server
// runs — somebody clicking is the whole point of it being state — and a cached
// copy would mean a change appears to have worked and then does nothing.
func (s *site) policy() authz.Policy {
	p := s.cfg.Roles
	p.Enforce = s.mode.Enforce
	if grants, err := s.store.Grants(); err == nil {
		p.Grants = grants
	}
	return p
}

func (s *site) roleNames() []string {
	out := make([]string, 0, len(s.cfg.Roles.Roles))
	for name := range s.cfg.Roles.Roles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// may answers one question about the caller, and hands back the sentence to
// show them if the answer is no.
func (s *site) may(r *http.Request, permission, item string) authz.Decision {
	req := authz.Request{Subject: s.actor(r).Name, Permission: permission}
	if item != "" {
		m, err := s.store.Meta(item)
		switch {
		case errors.Is(err, manage.ErrRefused):
			// A name the store will not take is a name no annotation can ever
			// have been filed under, so there is no restriction to have failed
			// to read. Refusing here would turn a file with a space in its
			// name into a 403 on a machine that authorizes nobody.
		case err != nil:
			// Anything else means the file is there and cannot be read — and
			// the limit somebody wrote in it is exactly what cannot be seen.
			// Carrying on would treat it as unlimited, which is the one
			// reading of a damaged restriction that must not be the default.
			return authz.Decision{Allowed: false,
				Because: "what may be seen of this cannot be read: " + err.Error()}
		}
		if len(m.ReadRoles) > 0 {
			req.Item = &authz.Item{ReadRoles: m.ReadRoles}
		}
	}
	return authz.Can(s.policy(), req)
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/layouts":
		s.index(w, r)
	case r.URL.Path == "/manage":
		s.manage(w, r)
	case r.URL.Path == "/roles":
		s.rolesPage(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/"):
		s.api(w, r)
	default:
		s.page(w, r)
	}
}

// inside reports whether path is at or below dir.
func inside(path, dir string) bool {
	a, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	b, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(b, a)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

// sameOrigin keeps another page open in the same browser from driving this
// one. The server is on loopback, which stops the network but not the tab next
// to it.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// refused maps what went wrong to a status.
//
// A caller's mistake and a server's failure are different things and used to
// be the same one. A mistyped name coming back as 500 tells the person nothing
// they can act on and tells whoever reads the logs something untrue.
func refused(w http.ResponseWriter, err error) {
	if errors.Is(err, manage.ErrRefused) || errors.Is(err, serve.ErrDocument) {
		http.Error(w, strings.TrimPrefix(err.Error(), "refused: "), http.StatusConflict)
		return
	}
	// Everything else is this end's problem — a disk that will not take the
	// write, a directory somebody made unreadable. Answering 409 would tell
	// the caller to change what they sent, which will not help, and would tell
	// whoever reads the log that a person made a mistake when a machine did.
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func (s *site) api(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "not from this page", http.StatusForbidden)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/")
	head, tail, _ := strings.Cut(rest, "/")
	switch head {
	case "layouts", "overlays":
		s.documents(w, r, head, tail)
	case "defaults":
		s.defaults(w, r, tail)
	case "grants":
		s.grants(w, r, tail)
	case "meta":
		s.meta(w, r, tail)
	case "screens":
		s.screens(w, r, tail)
	case "whoami":
		s.whoami(w, r)
	default:
		http.Error(w, "no such endpoint", http.StatusNotFound)
	}
}

// documents saves and removes the layout and overlay files themselves.
func (s *site) documents(w http.ResponseWriter, r *http.Request, kind, rest string) {
	if d := s.may(r, authz.Write, ""); !d.Allowed {
		http.Error(w, d.Because, http.StatusForbidden)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		http.Error(w, "want /api/"+kind+"/<page>/<name>", http.StatusBadRequest)
		return
	}
	page, name := parts[0], parts[1]
	if name == "" {
		// Saving from a page that was not opened as a named layout. Name it
		// after the moment rather than refusing: the alternative is asking a
		// question in the middle of somebody's drawing.
		name = time.Now().Format("2006-01-02-1504")
	}

	switch r.Method {
	case http.MethodPost:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		save := serve.Save
		if kind == "overlays" {
			save = serve.SaveOverlay
		}
		if err := save(s.state, page, name, body); err != nil {
			refused(w, err)
			return
		}
		// The name this end chose, where a machine can read it. A save from a
		// page that was not opened under a name gets one made up from the
		// clock a few lines above, and until now the only place that name
		// appeared was in the sentence below — which the browser shows for a
		// second and then forgets. The version was saved and immediately
		// unfindable: the page could not ask to be drawn with it, because it
		// never learned what it was called.
		w.Header().Set(NameHeader, name)
		fmt.Fprintf(w, "saved %s/%s", page, name)
	case http.MethodDelete:
		// Deleting a layout goes through the store rather than straight to the
		// file, because deleting the one that was promoted has to take the
		// promotion with it.
		if kind == "layouts" {
			if err := s.store.Forget(page, name, s.actor(r)); err != nil {
				refused(w, err)
				return
			}
			fmt.Fprintf(w, "removed %s/%s", page, name)
			return
		}
		if err := serve.RemoveOverlay(s.state, page, name); err != nil {
			refused(w, err)
			return
		}
		fmt.Fprintf(w, "removed %s/%s", page, name)
	default:
		http.Error(w, "POST to save, DELETE to remove", http.StatusMethodNotAllowed)
	}
}

// defaults promotes a version, or takes the promotion back.
func (s *site) defaults(w http.ResponseWriter, r *http.Request, rest string) {
	if d := s.may(r, authz.Write, ""); !d.Allowed {
		http.Error(w, d.Because, http.StatusForbidden)
		return
	}
	page, name, _ := strings.Cut(rest, "/")
	if page == "" {
		http.Error(w, "want /api/defaults/<page>/<name>", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPost:
		if name == "" {
			http.Error(w, "want /api/defaults/<page>/<name>", http.StatusBadRequest)
			return
		}
		if err := s.store.Promote(page, name, s.actor(r)); err != nil {
			refused(w, err)
			return
		}
		fmt.Fprintf(w, "%s is drawn with %s from now on", page, name)
	case http.MethodDelete:
		did, err := s.store.Demote(page, s.actor(r))
		if err != nil {
			refused(w, err)
			return
		}
		if !did {
			fmt.Fprintf(w, "%s had no default", page)
			return
		}
		fmt.Fprintf(w, "%s is drawn as generated from now on", page)
	default:
		http.Error(w, "POST to set, DELETE to take back", http.StatusMethodNotAllowed)
	}
}

// grants changes who holds which roles.
func (s *site) grants(w http.ResponseWriter, r *http.Request, subject string) {
	if d := s.may(r, authz.Admin, ""); !d.Allowed {
		http.Error(w, d.Because, http.StatusForbidden)
		return
	}
	if subject == "" {
		http.Error(w, "want /api/grants/<subject>", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Roles []string `json:"roles"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.store.Grant(subject, body.Roles, s.actor(r), s.roleNames()); err != nil {
			refused(w, err)
			return
		}
		fmt.Fprintf(w, "%s holds %s", subject, strings.Join(body.Roles, ", "))
	case http.MethodDelete:
		if err := s.store.Revoke(subject, s.actor(r)); err != nil {
			refused(w, err)
			return
		}
		fmt.Fprintf(w, "%s holds nothing", subject)
	default:
		http.Error(w, "POST to set, DELETE to take away", http.StatusMethodNotAllowed)
	}
}

// meta records what a person wrote about an item.
func (s *site) meta(w http.ResponseWriter, r *http.Request, item string) {
	if d := s.may(r, authz.Write, ""); !d.Allowed {
		http.Error(w, d.Because, http.StatusForbidden)
		return
	}
	if item == "" {
		http.Error(w, "want /api/meta/<item>", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var in manage.Meta
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := s.store.Annotate(item, in, s.actor(r), s.roleNames()); err != nil {
			refused(w, err)
			return
		}
		fmt.Fprintf(w, "wrote down %s", item)
	case http.MethodDelete:
		if err := s.store.Erase(item, s.actor(r)); err != nil {
			refused(w, err)
			return
		}
		fmt.Fprintf(w, "erased %s", item)
	default:
		http.Error(w, "POST to write, DELETE to erase", http.StatusMethodNotAllowed)
	}
}

// screens keeps and forgets the conditions somebody narrowed a listing with.
//
// Reading is the gate, not writing. A screening changes what one person sees
// of a listing they are already allowed to read and changes nothing for
// anybody else — and the person with the most to gain from narrowing a long
// list is exactly the one who may only look at it.
func (s *site) screens(w http.ResponseWriter, r *http.Request, name string) {
	if d := s.may(r, authz.Read, ""); !d.Allowed {
		http.Error(w, d.Because, http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name  string `json:"name"`
			Query string `json:"query"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		kept, err := s.store.SaveScreen(s.actor(r), strings.TrimSpace(body.Name), body.Query)
		if err != nil {
			refused(w, err)
			return
		}
		fmt.Fprintf(w, "kept as %s", kept.Name)
	case http.MethodDelete:
		if name == "" {
			http.Error(w, "want /api/screens/<name>", http.StatusBadRequest)
			return
		}
		did, err := s.store.ForgetScreen(s.actor(r), name)
		if err != nil {
			refused(w, err)
			return
		}
		if !did {
			fmt.Fprintf(w, "there was no %s to forget", name)
			return
		}
		fmt.Fprintf(w, "forgot %s", name)
	default:
		http.Error(w, "POST to keep, DELETE to forget", http.StatusMethodNotAllowed)
	}
}

// whoami is somebody saying what to call them.
//
// Nothing is asked of the caller first. Saying your own name is not a change
// to anything anybody else can see, and gating it on read would mean a person
// refused everything could not even name themselves to be granted a role —
// the one thing they need to do to stop being refused.
//
// What it hands back is exactly as good as the header it stands in for:
// self-asserted, and recorded as such wherever it lands.
func (s *site) whoami(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(body.Name)
		if !actorName.MatchString(name) {
			http.Error(w, "a name here is letters, digits, dot, underscore, dash and colon, "+
				"up to 64 — a subject is written provider:name", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: ActorCookie, Value: name, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode})
		fmt.Fprintf(w, "you are %s here until you say otherwise, and nothing checked that", name)
	case http.MethodDelete:
		http.SetCookie(w, &http.Cookie{Name: ActorCookie, Value: "", Path: "/",
			MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		fmt.Fprint(w, "you are nobody here now")
	default:
		http.Error(w, "POST to say, DELETE to stop saying", http.StatusMethodNotAllowed)
	}
}

// page serves a file, and when it is one of ours, applies the layout asked for
// and tells it where to save.
//
// The page keeps its own URL, so everything it references relatively — the
// graph beside it, the shared runtime above it — resolves exactly as it does
// without a layout.
func (s *site) page(w http.ResponseWriter, r *http.Request) {
	clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if clean == "." {
		clean = "index.html"
	}
	if strings.HasPrefix(clean, "..") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.pages, clean)

	// A directory of pages with nothing called index.html is the ordinary case
	// for output somebody generated, and answering the root with 404 tells a
	// person who just started the server that it is broken. Send them to the
	// listing this program can always build instead.
	if clean == "index.html" {
		if _, err := os.Stat(full); os.IsNotExist(err) {
			http.Redirect(w, r, "/manage", http.StatusSeeOther)
			return
		}
	}

	// The state directory sits beside the pages by default, which puts it
	// inside the tree being handed out. Everything in it — who holds which
	// role, what was done, what people wrote down including who may see what —
	// would otherwise be a plain GET away, and the guard on the pages would be
	// answering questions about a file the caller never had to ask for.
	if inside(full, s.state) {
		http.NotFound(w, r)
		return
	}

	if info, err := os.Stat(full); err == nil && info.IsDir() {
		full = filepath.Join(full, "index.html")
	}
	name := strings.TrimSuffix(filepath.Base(full), filepath.Ext(full))
	wantOverlay := r.URL.Query().Get("overlay")

	// A page's graph is the page's data under another name. Asking about
	// "core.graph" would consult whatever somebody wrote about an item that
	// does not exist, so a limit on "core" would not apply and the whole graph
	// could be fetched by anyone refused the page it belongs to.
	if d := s.may(r, authz.Read, strings.TrimSuffix(name, ".graph")); !d.Allowed {
		http.Error(w, d.Because, http.StatusForbidden)
		return
	}

	// A page whose graph is a separate file asks for it by url. The overlay
	// has to be applied to what that url answers, or the page fetches one
	// graph and draws another.
	if strings.HasSuffix(clean, ".graph.json") && wantOverlay != "" {
		s.graph(w, r, full, strings.TrimSuffix(name, ".graph"), wantOverlay)
		return
	}
	if !strings.EqualFold(filepath.Ext(full), ".html") {
		http.ServeFile(w, r, full)
		return
	}
	body, err := os.ReadFile(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	want := r.URL.Query().Get("layout")
	// Asking for no particular layout gets the one somebody promoted. That is
	// what promoting means: the page comes out that way for everybody who did
	// not ask for something else.
	//
	// Only a version whose file has gone falls back to the plain drawing —
	// that case is deliberate, and StaleDefault says it out loud. State that
	// cannot be read is not that case: swallowing it would make a decision
	// somebody recorded appear to have evaporated, on every request, silently.
	promoted := false
	if want == "" {
		d, ok, err := s.store.DefaultFor(name)
		if err != nil {
			http.Error(w, "what this is drawn with cannot be read: "+err.Error(),
				http.StatusInternalServerError)
			return
		}
		if ok {
			path, err := s.store.LayoutFor(name)
			if err != nil {
				http.Error(w, "what this is drawn with cannot be read: "+err.Error(),
					http.StatusInternalServerError)
				return
			}
			if path != "" {
				want, promoted = d.Version, true
			}
		}
	}

	// A version promoted with an overlay beside it is drawn with both.
	//
	// The two halves of a box somebody drew live in different documents: that
	// it exists is in the overlay, where it sits is in the layout. Promoting
	// used to bring back only the second, so the page pinned a position for a
	// box its graph had never heard of — the box gone and the space it stood
	// in still reserved. /manage has always opened the pair together; this is
	// the same rule for the plain url.
	//
	// Only what is drawn changes. Where a save goes is left alone below, so
	// pressing Save on a promoted page writes a new version rather than
	// quietly rewriting the one everybody else is looking at.
	drawOverlay := wantOverlay
	if drawOverlay == "" && promoted {
		if _, err := serve.ReadOverlay(s.state, name, want); err == nil {
			drawOverlay = want
		}
	}

	d := serve.Dressing{
		LayoutPost:  "/api/layouts/" + url.PathEscape(name) + "/" + url.PathEscape(r.URL.Query().Get("layout")),
		OverlayPost: "/api/overlays/" + url.PathEscape(name) + "/" + url.PathEscape(wantOverlay),
		DefaultPost: "/api/defaults/" + url.PathEscape(name) + "/",
	}
	if want != "" {
		if d.Layout, err = serve.Read(s.state, name, want); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
	}
	if drawOverlay != "" {
		if d.Overlay, err = serve.ReadOverlay(s.state, name, drawOverlay); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		d.GraphQuery = "overlay=" + url.QueryEscape(drawOverlay)
	}

	dressed, err := serve.Apply(body, d)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page changes with what was asked for, so it is not the file on disk
	// and must not be cached as though it were.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(dressed)
}

// graph answers a page's request for its graph with the overlay applied.
func (s *site) graph(w http.ResponseWriter, r *http.Request, full, page, name string) {
	body, err := os.ReadFile(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	claims, err := serve.ReadOverlay(s.state, page, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	enriched, err := serve.Enrich(body, claims)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(enriched)
}
