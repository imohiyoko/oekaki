// Package serve hands out rendered pages together with the layouts a person
// chose for them.
//
// A layout is a document this tool already defines: where the boxes go, applied
// with --layout. What was missing is the loop. You export one from the browser,
// it lands in a downloads folder, and putting it back means remembering a path
// and re-running the CLI. Most people do that once.
//
// Layouts live beside the pages, under layouts/<page>/<name>.layout.json. A
// page is served with one applied by injecting the document the page already
// reads by id — no re-render, and the original input does not need to be
// nearby.
//
// Every layout is reported against the graph the page actually carries. A
// layout written for last month's graph still applies, just less of it; the
// count is the only thing that says so.
package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers/overlay"
	layoutdoc "github.com/imohiyoko/oekaki/layout"
)

// Dir is where layouts live, relative to the served root. OverlayDir is the
// same for overlays.
//
// They are kept apart because they are different claims. A layout says where a
// box goes; an overlay says a box exists. A box somebody drew in the browser
// has both facts, and saving only one of them loses half of it — which is the
// whole reason this package handles both.
const (
	Dir        = "layouts"
	OverlayDir = "overlays"
)

const (
	suffix        = ".layout.json"
	overlaySuffix = ".overlay.json"
)

// marker identifies a page this tool rendered. Both the embedded and the
// external-asset forms carry it; the external one leaves the element empty and
// fetches the graph named by data-graph.
const marker = `id="oekaki-graph"`

var (
	layoutTag = regexp.MustCompile(`(?s)<script type="application/json" id="oekaki-layout">.*?</script>\s*`)
	graphAttr = regexp.MustCompile(`data-graph="([^"]*)"`)
	graphTag  = regexp.MustCompile(`(?s)<script type="application/json" id="oekaki-graph">(.*?)</script>`)
	safeName  = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._-]{0,63}\z`)
)

// ErrDocument marks a document the caller sent that this package will not
// store, as opposed to everything else that can go wrong in here.
//
// A server has to answer the person who sent a broken file differently from
// the person who has to go and fix a disk, and without a way to tell them
// apart it answers both the same — which tells the first one to change
// something that was fine and tells whoever reads the log that a person made a
// mistake when a machine did.
var ErrDocument = errors.New("not a document this can store")

// Page is a rendered page found under the served root.
//
// Name is the stem, and it is what everything saved for the page is filed
// under — not Rel. Two generations of the same drawing, runs/a/core.html and
// runs/b/core.html, therefore share one set of layouts and one default, which
// is the point: a position somebody chose outlives the run it was chosen on,
// and re-running the pipeline is not supposed to lose it.
//
// The cost is that two unrelated diagrams that happen to share a stem share
// each other's layouts. A directory holding both wants them named apart.
type Page struct {
	Rel  string // path relative to the root, e.g. "runs/abc/core.html"
	Name string // the stem, which names the folder its layouts live in

	// Inputs is what the graph said it was built from, as the page carries it.
	//
	// It is read from the page rather than from the graph because a
	// self-contained page has no graph beside it, and because the listing has
	// the page's bytes in hand already. A page rendered before this was
	// written carries nothing, which reads as "did not say" — not as "came
	// from nowhere". Nothing is narrowed away by a page that did not say.
	Inputs []string
}

// Layout is one saved layout and how much of it this page can use.
type Layout struct {
	Name    string
	Nodes   int
	Placed  int
	Missing []string
	Paired  bool // an overlay of the same name sits beside it
}

func hasOverlay(root, page, name string) bool {
	path, err := OverlayPath(root, page, name)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Pages lists the rendered pages under root, nearest the top first.
func Pages(root string) ([]Page, error) {
	var out []Page
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.EqualFold(filepath.Ext(path), ".html") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(body, []byte(marker)) {
			// Not one of ours, or unreadable. An index page someone wrote by
			// hand is not an error; it is just not a diagram.
			return nil //nolint:nilerr // unreadable files are simply not pages
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, Page{Rel: filepath.ToSlash(rel),
			Name:   strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
			Inputs: inputsIn(body)})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, err
}

// inputsAttr finds what a page says it was built from.
//
// The renderer writes the names into an attribute of the body element, which
// the template escapes, so what comes back out has to be unescaped before it
// is compared with anything.
var inputsAttr = regexp.MustCompile(`data-inputs="([^"]*)"`)

// bodyTag is the page's body element, opening angle bracket to closing one.
//
// Everything here is read out of that tag rather than out of the file, for two
// reasons that happen to have the same answer.
//
// A page can carry a stylesheet somebody supplied, and selecting on a data
// attribute of the body is this tool's own idiom — app.css ships
// body[data-mode="edit"] — so `data-inputs="..."` is a string that can honestly
// appear in the style element above. Searching the file would find that one
// first and report a name no graph ever named. The style element is the only
// place a stylesheet reaches, and the renderer refuses one that writes
// "</style", so the first "</style>" is the end of everything it could have
// written. A page whose assets are external has no style element at all, and a
// stylesheet it merely links to is not in the document to be read.
//
// The other reason is what a page rendered before any of this costs. It has no
// attribute, and looking for one in the whole file means reading every byte of
// a self-contained page — the graph, the layout engine, the canvas library —
// on every listing, to conclude what the first tag already said.
func bodyTag(body []byte) []byte {
	from := 0
	if end := bytes.Index(body, []byte("</style>")); end >= 0 {
		from = end
	}
	start := bytes.Index(body[from:], []byte("<body"))
	if start < 0 {
		return nil
	}
	start += from
	end := bytes.IndexByte(body[start:], '>')
	if end < 0 {
		return nil
	}
	return body[start : start+end]
}

// inputsIn reads those names out of a page.
//
// Whitespace around a name is dropped and an empty one is skipped, because the
// separator is written unconditionally: a page with nothing to say carries an
// empty attribute rather than none, and splitting that yields one empty
// string, which is not a name anybody can narrow by.
func inputsIn(body []byte) []string {
	m := inputsAttr.FindSubmatch(bodyTag(body))
	if m == nil {
		return nil
	}
	var out []string
	for _, name := range strings.Split(html.UnescapeString(string(m[1])), ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// CheckName rejects anything that would escape the layout folder.
func CheckName(name string) error {
	if !safeName.MatchString(name) {
		return fmt.Errorf("%w: %q cannot be a layout name: letters, digits, dot, underscore and dash, up to 64", ErrDocument, name)
	}
	return nil
}

func folder(root, page string) string { return filepath.Join(root, Dir, page) }

func overlayFolder(root, page string) string { return filepath.Join(root, OverlayDir, page) }

// OverlayPath is where an overlay for a page is kept.
func OverlayPath(root, page, name string) (string, error) {
	if err := CheckName(page); err != nil {
		return "", err
	}
	if err := CheckName(name); err != nil {
		return "", err
	}
	return filepath.Join(overlayFolder(root, page), name+overlaySuffix), nil
}

// savedIn lists a folder of saved documents, telling "nothing here yet" apart
// from "something is wrong here" the same way on every platform.
//
// os.ReadDir on a path that is a file reports ENOTDIR on Unix and a not-found
// error on Windows, so os.IsNotExist alone answers "there is nothing saved" on
// one of them and nothing at all on the other. A caller that reads no saved
// versions as none existing then fails open on exactly one platform: a file
// sitting where the folder goes is reported as a page with nothing saved,
// rather than as a page that could not be read. manage.AllMeta carries the
// same guard for the same reason.
func savedIn(dir string) ([]os.DirEntry, error) {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is where these are saved and it is not a directory", dir)
	}
	return os.ReadDir(dir)
}

// Overlays lists the overlays saved for a page.
func Overlays(root string, page Page) ([]string, error) {
	entries, err := savedIn(overlayFolder(root, page.Name))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), overlaySuffix) {
			out = append(out, strings.TrimSuffix(e.Name(), overlaySuffix))
		}
	}
	sort.Strings(out)
	return out, nil
}

// SaveOverlay writes an overlay, refusing anything that is not one.
func SaveOverlay(root, page, name string, body []byte) error {
	path, err := OverlayPath(root, page, name)
	if err != nil {
		return err
	}
	if _, err := overlay.Parse(body, name); err != nil {
		return fmt.Errorf("%w: %w", ErrDocument, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

// ReadOverlay returns a saved overlay.
func ReadOverlay(root, page, name string) ([]byte, error) {
	path, err := OverlayPath(root, page, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// RemoveOverlay deletes a saved overlay.
func RemoveOverlay(root, page, name string) error {
	path, err := OverlayPath(root, page, name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// Enrich applies an overlay to a graph document.
//
// The graph a page carries is what the drawing is of, so a box asserted in the
// browser only comes back if it is put there before the page reads it. Doing
// it here rather than asking people to re-render means the assertion survives
// a reload, which is the least a person expects of something they saved.
func Enrich(graph, claims []byte) ([]byte, error) {
	doc, err := overlay.Parse(claims, "overlay")
	if err != nil {
		return nil, err
	}
	g, err := core.Decode(bytes.NewReader(graph))
	if err != nil {
		return nil, err
	}
	if _, err := overlay.New([]*overlay.Document{doc}, overlay.Options{}).Enrich(g); err != nil {
		return nil, err
	}
	return g.MarshalIndent()
}

// Path is where a layout for a page is kept.
func Path(root, page, name string) (string, error) {
	if err := CheckName(page); err != nil {
		return "", err
	}
	if err := CheckName(name); err != nil {
		return "", err
	}
	return filepath.Join(folder(root, page), name+suffix), nil
}

// Layouts lists what has been saved for a page, each measured against the
// graph that page carries.
//
// It is the one place that needs both directories at once. What was saved is
// state and outlives any generation; the graph to measure it against is in the
// page, which is regenerated. Handing the same path to both is fine and is
// what a caller keeping them together does.
func Layouts(pages, state string, page Page) ([]Layout, error) {
	entries, err := savedIn(folder(state, page.Name))
	if err != nil {
		return nil, err
	}
	base, err := GraphIDs(pages, page, nil)
	if err != nil {
		return nil, err
	}

	var out []Layout
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), suffix)
		path, err := Path(state, page.Name, name)
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		doc, err := layoutdoc.Parse(raw, path)
		if err != nil {
			// A file that will not parse is still worth listing: it is there,
			// somebody put it there, and hiding it makes it unfindable.
			out = append(out, Layout{Name: name})
			continue
		}
		// A layout saved alongside an overlay is counted against the graph
		// that overlay makes. Counting against the bare graph would report a
		// box the page will draw as landing nowhere.
		known := base
		if claims, err := ReadOverlay(state, page.Name, name); err == nil {
			if with, err := GraphIDs(pages, page, claims); err == nil {
				known = with
			}
		}
		at := doc.Against(known)
		out = append(out, Layout{Name: name, Nodes: at.Total(),
			Placed: at.Placed, Missing: at.Missing, Paired: hasOverlay(state, page.Name, name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GraphIDs is every node and group id the page carries, which is what a layout
// can land on.
func GraphIDs(root string, page Page, claims []byte) (map[string]struct{}, error) {
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page.Rel)))
	if err != nil {
		return nil, err
	}

	raw := []byte(nil)
	if m := graphTag.FindSubmatch(body); m != nil && len(bytes.TrimSpace(m[1])) > 0 {
		raw = m[1]
	} else if m := graphAttr.FindSubmatch(body); m != nil {
		// External assets: the graph sits beside the page.
		next := filepath.Join(filepath.Dir(filepath.Join(root, filepath.FromSlash(page.Rel))),
			filepath.FromSlash(string(m[1])))
		if raw, err = os.ReadFile(next); err != nil {
			return nil, fmt.Errorf("reading the graph %s names: %w", page.Rel, err)
		}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s carries no graph", page.Rel)
	}

	if len(claims) > 0 {
		enriched, err := Enrich(raw, claims)
		if err != nil {
			return nil, err
		}
		raw = enriched
	}

	var g struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Groups []struct {
			ID string `json:"id"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("reading the graph %s carries: %w", page.Rel, err)
	}
	ids := make(map[string]struct{}, len(g.Nodes)+len(g.Groups))
	for _, n := range g.Nodes {
		ids[n.ID] = struct{}{}
	}
	for _, n := range g.Groups {
		ids[n.ID] = struct{}{}
	}
	return ids, nil
}

// Dressing is what to put into a page before handing it over.
type Dressing struct {
	Layout      []byte // the layout to apply, if any
	Overlay     []byte // the overlay to apply, if any
	GraphQuery  string // appended to the graph url when the graph is a separate file
	LayoutPost  string // where the page saves its layout
	OverlayPost string // where the page saves what it asserted

	// DefaultPost is where the page asks for a version it saved to become the
	// one everybody gets. It is separate from LayoutPost because the two mean
	// different things to everyone who is not the person clicking: saving is
	// private and changes nobody's picture, promoting changes what the page
	// draws for the next person who opens it. Handing the page one url for
	// both would make that distinction impossible to offer.
	DefaultPost string
}

// Apply puts a layout and an overlay into a rendered page and tells it where
// to save.
//
// The page reads a layout by id, so injecting the element is enough. Any
// layout already there is removed first: getElementById answers with the first
// match, so adding a second would leave the old one winning.
//
// An overlay changes the graph rather than the page, so it is applied to the
// graph the page carries. When the graph is a separate file the page is only
// pointed at a url that will have it applied — rewriting the document here
// would leave the page fetching one graph and drawing another.
func Apply(page []byte, d Dressing) ([]byte, error) {
	out := page
	if len(d.Layout) > 0 {
		out = layoutTag.ReplaceAll(out, nil)
		tag := []byte(`<script type="application/json" id="oekaki-layout">` +
			strings.ReplaceAll(string(d.Layout), "</", `<\/`) + "</script>\n")
		out = bytes.Replace(out, []byte(`<script type="application/json" id="oekaki-graph">`),
			append(tag, []byte(`<script type="application/json" id="oekaki-graph">`)...), 1)
	}

	if len(d.Overlay) > 0 {
		if m := graphTag.FindSubmatch(out); m != nil && len(bytes.TrimSpace(m[1])) > 0 {
			enriched, err := Enrich(m[1], d.Overlay)
			if err != nil {
				return nil, err
			}
			out = graphTag.ReplaceAll(out, []byte(`<script type="application/json" id="oekaki-graph">`+
				strings.ReplaceAll(string(enriched), "</", `<\/`)+`</script>`))
		}
	}
	if d.GraphQuery != "" {
		out = graphAttr.ReplaceAllFunc(out, func(m []byte) []byte {
			src := string(graphAttr.FindSubmatch(m)[1])
			sep := "?"
			if strings.Contains(src, "?") {
				sep = "&"
			}
			return []byte(`data-graph="` + src + sep + d.GraphQuery + `"`)
		})
	}

	for attr, url := range map[string]string{"data-layout-post": d.LayoutPost,
		"data-overlay-post": d.OverlayPost, "data-default-post": d.DefaultPost} {
		if url != "" {
			out = bytes.Replace(out, []byte("<body "), []byte(`<body `+attr+`="`+url+`" `), 1)
		}
	}
	return out, nil
}

// Save writes a layout, refusing anything that is not one.
//
// Checking here rather than at load keeps a broken document from becoming the
// one a page is served with. The browser is not the only thing that writes
// here, so the check cannot live there.
func Save(root, page, name string, body []byte) error {
	path, err := Path(root, page, name)
	if err != nil {
		return err
	}
	if _, err := layoutdoc.Parse(body, name); err != nil {
		return fmt.Errorf("%w: %w", ErrDocument, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

// Read returns a saved layout.
func Read(root, page, name string) ([]byte, error) {
	path, err := Path(root, page, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Remove deletes a saved layout.
func Remove(root, page, name string) error {
	path, err := Path(root, page, name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}
