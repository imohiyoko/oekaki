// Package html renders the IR as a single self-contained page.
//
// Self-contained is not a preference. A page loaded from file:// cannot fetch
// a sibling document — Chrome blocks it as a cross-origin request — so a page
// that kept its graph in a separate .json would need a web server, and the
// audience for this renderer is one person looking at their own estate on
// their own machine. Everything therefore goes in one file: the graph, the
// layout engine, the script and the styles.
//
// What that buys is bigger than avoiding a server. The output stays a file:
// movable, attachable, committable, and openable a year later by somebody who
// has never installed this tool.
//
// # Determinism
//
// The artifact is deterministic and is covered by `make verify-example`: the
// embedded IR is normalized, the assets are fixed, and nothing here reads a
// clock or a random source.
//
// The positions in the page are not, in the sense that this project can check.
// ELK computes them in the browser after the file is written, and CI never
// runs a browser. ELK's layered algorithm is itself deterministic for
// identical input, so reopening the same file gives the same picture — but
// that is a property of ELK rather than a promise tested here. SVG remains the
// default output and the one to commit into a pull request.
package html

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"sync"

	"github.com/imohiyoko/oekaki/core"
)

//go:embed vendor/elk.bundled.js
var elkJS string

//go:embed vendor/maxgraph.bundled.js
var maxJS string

//go:embed app.js
var appJS string

//go:embed boot.js
var bootJS string

//go:embed app.css
var appCSS string

//go:embed page.html.tmpl
var pageTemplate string

//go:embed icons.svg
var builtinIcons string

// AssetELK, AssetApp and AssetCSS are the names an external page references
// its runtime under. They do not vary with the graph, which is the whole
// point: a server sends them once and every diagram after that costs only its
// own document.
const (
	AssetELK  = "oekaki.elk.js"
	AssetMax  = "oekaki.maxgraph.js"
	AssetApp  = "oekaki.app.js"
	AssetCSS  = "oekaki.app.css"
	AssetBoot = "oekaki.boot.js"
)

// Assets returns the shared runtime an external page references, keyed by the
// name it references it under.
//
// All but one are the same bytes a self-contained page inlines, so the two
// kinds of page run identical code. The exception, the bootstrap, exists only
// here: a self-contained page already has its graph and has nothing to fetch.
func Assets(extraCSS []byte) map[string][]byte {
	return map[string][]byte{
		AssetELK:  []byte(elkJS),
		AssetMax:  []byte(maxJS),
		AssetApp:  []byte(appJS),
		AssetCSS:  append([]byte(appCSS), extraCSS...),
		AssetBoot: []byte(bootJS),
	}
}

// assetURL joins the prefix a server serves the shared files from with one
// file name, and stamps the runtime's own fingerprint on it. An empty base
// means the files sit beside the page.
//
// The stamp is what keeps a page and its runtime the same age. The shared
// files are served from one path for every diagram and every generation of
// them — that is the point of sharing — so a browser caches them, and it is
// right to. But when the runtime changes underneath that path, everyone who
// has opened a page before goes on running the old one against the new
// markup, and the two disagree in whatever way they disagree: seen here as a
// DOM error thrown out of a script that has no way to report it, leaving a
// blank canvas that reads as an estate with nothing in it.
//
// A reload does not fix it either. The bootstrap fetches the viewer by
// creating a script element, so it is not a subresource of the document and a
// hard reload leaves it alone.
//
// With the fingerprint in the URL, a runtime that changed is a different URL
// and cannot be served from the old entry; a runtime that did not change is
// the same URL and is still shared. Nobody has to remember to rename
// anything.
func assetURL(base, name, stamp string) string {
	stamped := name + "?v=" + stamp
	switch {
	case base == "":
		return stamped
	case strings.HasSuffix(base, "/"):
		return base + stamped
	default:
		return base + "/" + stamped
	}
}

// RuntimeStamp is the fingerprint of the shared runtime files.
//
// It is exported so that a caller can put it in the path the runtime is
// served from, not just in the query string. The query string stops a browser
// serving a stale runtime to a new page; a fingerprinted directory stops the
// opposite, which is a fresh runtime being served to a page rendered against
// an older one. Pages outlive builds when they are kept per generation, so
// both directions happen.
func RuntimeStamp(extraCSS []byte) string {
	if len(extraCSS) == 0 {
		return builtinStamp()
	}
	// Hashed on top of the built-in stamp rather than beside it, so a build
	// that changed the runtime and a caller that changed the theme both move
	// the answer, and neither can land on the other's value.
	sum := sha256.New()
	sum.Write([]byte(builtinStamp()))
	sum.Write(extraCSS)
	return hex.EncodeToString(sum.Sum(nil))[:12]
}

// builtinStamp fingerprints the shared files as this build ships them. It is
// the same for every page a given build renders, so a server still holds one
// copy of each file.
var builtinStamp = sync.OnceValue(func() string {
	assets := Assets(nil)
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)

	sum := sha256.New()
	for _, name := range names {
		sum.Write([]byte(name))
		sum.Write(assets[name])
	}
	return hex.EncodeToString(sum.Sum(nil))[:12]
})

// Options tunes the page.
type Options struct {
	// Title is shown in the header and used as the document title.
	Title string

	// Axis selects which grouping to nest by. Empty means the network axis,
	// or whichever axis the document has.
	Axis string

	// RankDir selects the ELK layout direction using the same LR/TB values as
	// the other renderers. Empty means LR.
	RankDir string

	// Lines is the shape every line is drawn with unless a layout document
	// says otherwise: "curved" (the default) or "orthogonal". The viewer has a
	// switch for it, but that is a thing to do to a page, and a collection of
	// pages wants to be generated the same way rather than switched one by one.
	Lines string

	// Kinds restricts which edge kinds are drawn. The full graph remains
	// embedded so details and exported evidence retain their provenance.
	Kinds []core.EdgeKind

	// Layout is an optional human-authored layout document embedded in the page.
	Layout []byte

	// Atlas is an optional bound set of diagrams, as produced by
	// views.BuildAtlas and marshalled. When present the page opens on the
	// atlas's root diagram and a box that has an inside opens it, instead of
	// drawing the whole estate nested on one canvas.
	//
	// It arrives marshalled rather than typed for the same reason Layout
	// does: a renderer takes documents. The graph passed to Render remains
	// the page's fallback and the source of the page's own attributes, so an
	// atlas that fails to parse degrades to the drawing this renderer has
	// always produced rather than to a blank canvas.
	Atlas []byte

	// IconDir is a directory of SVG files to use instead of the built-in
	// glyphs: <resource_type>.svg where a type has its own, otherwise
	// <category>.svg.
	//
	// It exists because the icon sets people actually want — AWS, Google
	// Cloud, Azure — ship under terms that a public Apache-2.0 repository
	// cannot vendor. The same principle as credentials applies: the asset you
	// already have is the input, and this project ships nothing it cannot
	// licence.
	IconDir string

	// CSS is an extra stylesheet appended to the built-in one, for a caller
	// who wants the diagrams to look like the rest of what they publish.
	//
	// Appended in both page kinds, so a self-contained page and an external
	// one go on running identical styles — the same reason the other assets
	// are the same bytes either way.
	//
	// It is part of what the fingerprint covers. The shared stylesheet is
	// served from one URL for every diagram and every generation of them, so
	// a theme that changed and did not move the fingerprint would go on being
	// served from cache, and the pages that asked for it would be the ones
	// that never saw it.
	CSS []byte

	// ExternalAssets writes a page that fetches its graph and loads the
	// runtime from separate files instead of carrying both itself.
	//
	// Self-contained is the right default: the file opens from file:// with
	// nothing to serve it. But it also means every diagram carries its own
	// copy of ELK, and that copy is 1.5 MB against a graph document of a few
	// tens of kilobytes. Serving a collection inverts the trade — the runtime
	// is sent once and cached, and each further diagram costs only its data.
	//
	// The page then needs a server. A fetch from file:// is blocked as a
	// cross-origin request, so this mode is for something that serves HTTP,
	// not for a directory somebody opens.
	ExternalAssets bool

	// AssetBase is the prefix the shared files are served from, such as
	// "/shell/v1". Empty means they sit beside the page.
	AssetBase string

	// GraphSrc is where the page fetches its graph document from. It is
	// required when ExternalAssets is set: without it the page has no data
	// and no way to say so.
	GraphSrc string
}

var page = template.Must(template.New("page").Parse(pageTemplate))

// Render returns a complete HTML document.
func Render(g *core.Graph, opts Options) ([]byte, error) {
	if opts.RankDir == "" {
		opts.RankDir = "LR"
	}
	if opts.RankDir != "LR" && opts.RankDir != "TB" {
		return nil, fmt.Errorf("unknown HTML layout direction %q: want LR or TB", opts.RankDir)
	}
	if opts.Lines == "" {
		opts.Lines = "curved"
	}
	if opts.Lines != "curved" && opts.Lines != "orthogonal" {
		return nil, fmt.Errorf("unknown HTML line shape %q: want curved or orthogonal", opts.Lines)
	}
	// A self-contained page carries the stylesheet inside a <style> element,
	// where that element's closing tag ends the block and drops the rest of
	// the sheet into the document as markup. An external page would take the
	// same bytes without complaint, so the two kinds would disagree about a
	// file that is wrong in both.
	if at := bytes.Index(bytes.ToLower(opts.CSS), []byte("</style")); at >= 0 {
		return nil, fmt.Errorf("this stylesheet writes \"</style\" at byte %d, which would end the page's style element early", at)
	}
	axis := g.AxisOrDefault(opts.Axis)
	if opts.Axis != "" && axis == "" {
		return nil, fmt.Errorf("this graph has no axis %q", opts.Axis)
	}
	if opts.ExternalAssets && opts.GraphSrc == "" {
		return nil, fmt.Errorf("an external page needs a graph url to fetch")
	}

	// core.Encode rather than an ad-hoc marshal, so the embedded document is
	// byte-identical to what `oekaki graph` would have written. A viewer
	// that shows something subtly different from the file it came from is
	// worse than no viewer.
	//
	// An external page fetches that same document instead of carrying it, so
	// there is nothing to embed and nothing to escape.
	var graph template.JS
	var layout template.JS
	if !opts.ExternalAssets {
		graphJSON, err := g.MarshalIndent()
		if err != nil {
			return nil, err
		}
		// The graph goes into a script element of a non-JavaScript type, so
		// it is data the page parses rather than code the browser runs. A
		// resource named "</script>" would otherwise close the block early
		// and the rest of the graph would land in the document as markup, so
		// every "</" is written as the JSON escape "<\/", which parsers
		// accept and HTML tokenizers do not recognise as a closing tag.
		graph = template.JS(bytes.ReplaceAll(graphJSON, []byte("</"), []byte(`<\/`)))
	}
	if len(opts.Layout) > 0 {
		layout = template.JS(bytes.ReplaceAll(opts.Layout, []byte("</"), []byte(`<\/`)))
	}
	var atlas template.JS
	if len(opts.Atlas) > 0 {
		atlas = template.JS(bytes.ReplaceAll(opts.Atlas, []byte("</"), []byte(`<\/`)))
	}

	title := opts.Title
	if title == "" {
		title = "oekaki"
	}
	kinds := make([]string, 0, len(opts.Kinds))
	for _, kind := range opts.Kinds {
		kinds = append(kinds, string(kind))
	}
	sort.Strings(kinds)

	inputs := inputNames(g)

	icons := builtinIcons
	if opts.IconDir != "" {
		var err error
		icons, err = loadIcons(opts.IconDir, g)
		if err != nil {
			return nil, err
		}
	}

	stamp := RuntimeStamp(opts.CSS)

	var out bytes.Buffer
	err := page.Execute(&out, struct {
		Title    string
		Axis     string
		RankDir  string
		Lines    string
		Kinds    string
		Inputs   string
		Graph    template.JS
		Layout   template.JS
		Atlas    template.JS
		ELK      template.JS
		Max      template.JS
		App      template.JS
		CSS      template.CSS
		Icons    template.HTML
		External bool
		CSSSrc   string
		ELKSrc   string
		MaxSrc   string
		AppSrc   string
		BootSrc  string
		GraphSrc string
	}{
		Title:    title,
		Axis:     axis,
		RankDir:  opts.RankDir,
		Lines:    opts.Lines,
		Kinds:    strings.Join(kinds, ","),
		Inputs:   strings.Join(inputs, ","),
		Graph:    graph,
		Layout:   layout,
		Atlas:    atlas,
		ELK:      template.JS(elkJS),
		Max:      template.JS(maxJS),
		App:      template.JS(appJS),
		CSS:      template.CSS(appCSS + string(opts.CSS)),
		Icons:    template.HTML(icons),
		External: opts.ExternalAssets,
		CSSSrc:   assetURL(opts.AssetBase, AssetCSS, stamp),
		ELKSrc:   assetURL(opts.AssetBase, AssetELK, stamp),
		MaxSrc:   assetURL(opts.AssetBase, AssetMax, stamp),
		AppSrc:   assetURL(opts.AssetBase, AssetApp, stamp),
		BootSrc:  assetURL(opts.AssetBase, AssetBoot, stamp),
		GraphSrc: opts.GraphSrc,
	})
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// inputNames is what the graph says it was built from.
//
// The names are already in the document, and a page that carries them says so
// about itself. That matters for anything holding a directory of pages: it can
// ask which of them came from a particular repository without opening the
// graph beside each one, and a graph beside each one is exactly what a
// self-contained page does not have.
//
// A name with a comma in it is left out rather than written down. The
// attribute separates on commas, so keeping it would put two names on the page
// that were never in the graph — and the reader has no way to tell that pair
// from two real ones. One name missing is visible in the graph it came from;
// two invented ones are not visible anywhere.
func inputNames(g *core.Graph) []string {
	if g == nil || g.Metadata == nil {
		return nil
	}
	out := make([]string, 0, len(g.Metadata.Inputs))
	seen := map[string]bool{}
	for _, in := range g.Metadata.Inputs {
		if in.ID == "" || strings.Contains(in.ID, ",") || seen[in.ID] {
			continue
		}
		seen[in.ID] = true
		out = append(out, in.ID)
	}
	sort.Strings(out)
	return out
}
