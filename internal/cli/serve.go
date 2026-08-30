package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imohiyoko/oekaki/internal/serve"
)

// runServe hands out a directory of rendered pages and the layouts saved for
// them.
//
// It binds the loopback address only. The pages are yours and the server
// writes files; neither belongs on a network interface by accident. Callers
// who want otherwise can put a reverse proxy in front and decide for
// themselves what that means.
func runServe(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	addr := fs.String("addr", "127.0.0.1:8080", "address to listen on; loopback only")
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
	if err := loopbackOnly(*addr); err != nil {
		return err
	}

	srv := &http.Server{Addr: *addr, Handler: &site{root: root}, ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stderr, "serving %s\n  http://%s/\n  http://%s/layouts   saved layouts\n",
		root, ln.Addr(), ln.Addr())

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

type site struct{ root string }

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/layouts":
		s.index(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/layouts/"),
		strings.HasPrefix(r.URL.Path, "/api/overlays/"):
		s.api(w, r)
	default:
		s.page(w, r)
	}
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

func (s *site) api(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "not from this page", http.StatusForbidden)
		return
	}
	kind := "layouts"
	rest := strings.TrimPrefix(r.URL.Path, "/api/layouts/")
	if strings.HasPrefix(r.URL.Path, "/api/overlays/") {
		kind = "overlays"
		rest = strings.TrimPrefix(r.URL.Path, "/api/overlays/")
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
		if err := save(s.root, page, name, body); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		fmt.Fprintf(w, "saved %s/%s", page, name)
	case http.MethodDelete:
		remove := serve.Remove
		if kind == "overlays" {
			remove = serve.RemoveOverlay
		}
		if err := remove(s.root, page, name); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		fmt.Fprintf(w, "removed %s/%s", page, name)
	default:
		http.Error(w, "POST to save, DELETE to remove", http.StatusMethodNotAllowed)
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
	full := filepath.Join(s.root, clean)
	if info, err := os.Stat(full); err == nil && info.IsDir() {
		full = filepath.Join(full, "index.html")
	}
	name := strings.TrimSuffix(filepath.Base(full), filepath.Ext(full))
	wantOverlay := r.URL.Query().Get("overlay")

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

	d := serve.Dressing{
		LayoutPost:  "/api/layouts/" + url.PathEscape(name) + "/" + url.PathEscape(r.URL.Query().Get("layout")),
		OverlayPost: "/api/overlays/" + url.PathEscape(name) + "/" + url.PathEscape(wantOverlay),
	}
	if want := r.URL.Query().Get("layout"); want != "" {
		if d.Layout, err = serve.Read(s.root, name, want); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
	}
	if wantOverlay != "" {
		if d.Overlay, err = serve.ReadOverlay(s.root, name, wantOverlay); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		d.GraphQuery = "overlay=" + url.QueryEscape(wantOverlay)
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
	claims, err := serve.ReadOverlay(s.root, page, name)
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

func (s *site) index(w http.ResponseWriter, r *http.Request) {
	pages, err := serve.Pages(s.root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><title>layouts</title>` + indexCSS + `<h1>layouts</h1>`)
	if len(pages) == 0 {
		b.WriteString(`<p class=m>No rendered pages under this directory.</p>`)
	}
	for _, p := range pages {
		b.WriteString(`<h2><a href="/` + html.EscapeString(p.Rel) + `">` + html.EscapeString(p.Rel) + `</a></h2>`)
		saved, err := serve.Layouts(s.root, p)
		if err != nil {
			b.WriteString(`<p class=m>` + html.EscapeString(err.Error()) + `</p>`)
			continue
		}
		if len(saved) == 0 {
			b.WriteString(`<p class=m>None saved yet. Open the page, switch to Edit, move things, then Export layout.</p>`)
			continue
		}
		b.WriteString(`<table><tr><th>saved<th>positions<th>placed<th>not in this graph`)
		for _, l := range saved {
			href := "/" + p.Rel + "?layout=" + url.QueryEscape(l.Name)
			missing := "—"
			if n := len(l.Missing); n > 0 {
				missing = fmt.Sprintf("%d (%s)", n, strings.Join(l.Missing[:min(3, n)], ", "))
			}
			paired := ""
			if l.Paired {
				// The box somebody drew lives in the overlay; without it the
				// layout has a position for something that does not exist.
				href += "&overlay=" + url.QueryEscape(l.Name)
				paired = ` <small>+ what it asserts</small>`
			}
			b.WriteString(`<tr><td><a href="` + html.EscapeString(href) + `">` +
				html.EscapeString(l.Name) + `</a>` + paired + `<td>` + fmt.Sprint(l.Nodes) +
				`<td>` + fmt.Sprint(l.Placed) + `<td>` + html.EscapeString(missing))
		}
		b.WriteString(`</table>`)
	}
	b.WriteString(`<p class=m>A box drawn in the browser has two facts about it: that it ` +
		`exists, which an overlay carries, and where it sits, which a layout carries. ` +
		`Saving one without the other loses the box, so both are saved together and ` +
		`opened together.</p>`)
	b.WriteString(`<p class=m>A layout applies to whatever the page carries now. ` +
		`Positions that match nothing are kept in the file and listed here rather ` +
		`than dropped, so a layout shared with a narrower view does not lose them.</p>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, b.String())
}

const indexCSS = `<style>
body{margin:0;padding:32px 24px;font:14px/1.7 system-ui,sans-serif;color:#323232;background:#f7f5f5;max-width:900px}
h1{font-size:20px;margin:0 0 4px}h2{font-size:15px;margin:32px 0 8px}
a{color:#285ac8}.m{color:#6e6b6b}
table{width:100%;border-collapse:collapse;background:#fff;border-radius:8px;overflow:hidden}
th,td{text-align:left;padding:7px 12px;border-bottom:1px solid #e9e7e7}
th{color:#6e6b6b;font-weight:400}tr:last-child td{border-bottom:0}
</style>`
