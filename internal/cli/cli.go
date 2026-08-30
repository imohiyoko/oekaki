// Package cli implements the oekaki command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"time"

	loginventorycollector "github.com/imohiyoko/oekaki/collectors/loginventory"
	reachabilitycollector "github.com/imohiyoko/oekaki/collectors/reachability"
	tracecollector "github.com/imohiyoko/oekaki/collectors/traces"
	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers/ai"
	"github.com/imohiyoko/oekaki/enrichers/exposure"
	loginventoryenricher "github.com/imohiyoko/oekaki/enrichers/loginventory"
	"github.com/imohiyoko/oekaki/enrichers/observations"
	"github.com/imohiyoko/oekaki/enrichers/overlay"
	"github.com/imohiyoko/oekaki/enrichers/reachable"
	traceenricher "github.com/imohiyoko/oekaki/enrichers/traces"
	layoutdoc "github.com/imohiyoko/oekaki/layout"
	sourceparser "github.com/imohiyoko/oekaki/parsers/source"
	"github.com/imohiyoko/oekaki/parsers/terraform"
	dotrender "github.com/imohiyoko/oekaki/renderers/dot"
	gvrender "github.com/imohiyoko/oekaki/renderers/graphviz"
	htmlrender "github.com/imohiyoko/oekaki/renderers/html"
	"github.com/imohiyoko/oekaki/renderers/mermaid"
	"github.com/imohiyoko/oekaki/schema"
	"github.com/imohiyoko/oekaki/views"
)

// Version is stamped at build time with -ldflags. Release builds set it; a
// plain `go build` or `go install` does not, which is what resolveVersion is
// for.
var Version = "dev"

// version reports the version to show the user.
//
// Release archives are built by GoReleaser, which stamps Version. But the
// install route the README actually recommends is `go install ...@v0.1.0`,
// and that applies no ldflags at all — so the binary most people end up with
// was reporting "dev". The module version is recorded in the build info, so
// fall back to that.
func version() string {
	var fromBuild string
	if info, ok := debug.ReadBuildInfo(); ok {
		fromBuild = info.Main.Version
	}
	return resolveVersion(Version, fromBuild)
}

// resolveVersion is split out from version so it can be tested without
// depending on how the test binary itself happened to be built.
func resolveVersion(stamped, fromBuild string) string {
	if stamped != "" && stamped != "dev" {
		return stamped
	}
	switch fromBuild {
	case "", "(devel)":
		// Built from a working tree rather than a published version.
		return "dev"
	}
	return fromBuild
}

// Env carries the streams a command reads and writes, so tests can drive the
// CLI without touching the real process.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run dispatches a command line and returns a process exit code.
func Run(ctx context.Context, env Env, args []string) int {
	if len(args) == 0 {
		usage(env.Stderr)
		return 2
	}

	var err error
	switch args[0] {
	case "probe":
		err = runProbe(ctx, env, args[1:])
	case "render":
		err = runRender(ctx, env, args[1:])
	case "graph":
		err = runGraph(ctx, env, args[1:])
	case "scan":
		err = runScan(env, args[1:])
	case "serve":
		err = runServe(ctx, env, args[1:])
	case "focus":
		err = runFocus(env, args[1:])
	case "collapse":
		err = runCollapse(env, args[1:])
	case "export":
		err = runExport(env, args[1:])
	case "validate":
		err = runValidate(env, args[1:])
	case "schema":
		err = runSchema(env, args[1:])
	case "version", "--version", "-version":
		fmt.Fprintf(env.Stdout, "oekaki %s\n", version())
	case "help", "--help", "-h":
		usage(env.Stdout)
	default:
		fmt.Fprintf(env.Stderr, "oekaki: unknown command %q\n\n", args[0])
		usage(env.Stderr)
		return 2
	}

	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 2
		}
		fmt.Fprintf(env.Stderr, "oekaki: %v\n", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `oekaki turns Terraform output into a diagram, and into the graph behind it.

Usage:
  oekaki render <input> [flags]     draw a diagram
  oekaki graph  <input> [flags]     emit the intermediate representation
  oekaki scan   <dir>   [flags]     read committed Terraform source, no init or credentials
  oekaki probe  <graph> [flags]     probe explicitly named network targets
  oekaki focus  <graph> [flags]     keep one group whole, fold the rest to a box each
  oekaki collapse <graph> [flags]   fold every group to one box, lines carry their weight
  oekaki export <graph> [flags]     write the graph out as a table
  oekaki serve  [dir]               hand out rendered pages, their layouts and what was decided
  oekaki validate <graph.json>      check a graph against the IR schema
  oekaki schema                     print the IR JSON Schema
  oekaki version                    print the version

<input> is `+"`terraform show -json`"+` output — a plan or a state — or a graph
this tool produced earlier. Use - to read standard input. No AWS credentials
are needed and nothing is sent anywhere.

Examples:
  terraform show -json tfplan > plan.json
  oekaki render plan.json -o architecture.svg
  oekaki render plan.json -f mermaid --fenced >> README.md
  oekaki graph plan.json | oekaki render - -o architecture.svg

Run a command with -h for its flags.
`)
}

type probeTargets []string

func (p *probeTargets) String() string { return strings.Join(*p, ",") }
func (p *probeTargets) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("probe target is empty")
	}
	*p = append(*p, value)
	return nil
}

func runProbe(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	output := fs.String("o", "", "write normalized reachability JSON to this file instead of standard output")
	from := fs.String("from", "", "source node or group id where this probe runs")
	protocol := fs.String("protocol", "tcp", "probe protocol: tcp, http or https")
	port := fs.Int("port", 0, "optional port recorded on every target")
	timeout := fs.Duration("timeout", 5*time.Second, "timeout for each target")
	var specs probeTargets
	fs.Var(&specs, "target", "target in the form id=address; http(s) URLs infer their protocol; repeatable")
	fs.Usage = func() {
		fmt.Fprintf(env.Stderr, "Usage: oekaki probe <graph.json> --from NODE --target ID=ADDRESS [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("probe needs exactly one graph or Terraform input")
	}
	if *from == "" {
		return errors.New("probe requires --from")
	}
	if *timeout <= 0 {
		return errors.New("probe timeout must be positive")
	}
	g, err := loadGraph(env, fs.Arg(0), terraform.Options{}, sourceparser.Options{})
	if err != nil {
		return err
	}
	if _, ok := g.Node(*from); !ok {
		if _, ok := g.Group(*from); !ok {
			return fmt.Errorf("probe source %q was not found", *from)
		}
	}
	targets := make([]reachabilitycollector.Target, 0, len(specs))
	for i, spec := range specs {
		id, address, ok := strings.Cut(spec, "=")
		if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(address) == "" {
			return fmt.Errorf("target %d must be ID=ADDRESS", i+1)
		}
		address = strings.TrimSpace(address)
		targetProtocol := *protocol
		// URL targets carry their own application protocol, while plain
		// host:port targets use the command default (normally TCP). This
		// permits one probe command to cover both an HTTP health endpoint and
		// a database port without silently probing the wrong transport.
		if strings.HasPrefix(strings.ToLower(address), "http://") {
			targetProtocol = "http"
		} else if strings.HasPrefix(strings.ToLower(address), "https://") {
			targetProtocol = "https"
		}
		targets = append(targets, reachabilitycollector.Target{ID: strings.TrimSpace(id), Address: address, Protocol: targetProtocol, Port: *port, Timeout: *timeout})
	}
	doc, err := reachabilitycollector.Probe(ctx, *from, targets)
	if err != nil {
		return err
	}
	out, err := doc.MarshalIndent()
	if err != nil {
		return err
	}
	return write(env, *output, out)
}

type renderFlags struct {
	output          string
	format          string
	rankdir         string
	lines           string
	axis            string
	scope           string
	kinds           string
	sourceDir       string
	title           string
	legend          bool
	fenced          bool
	data            bool
	unknownSource   bool
	iconDir         string
	externalAssets  bool
	assetBase       string
	view            string
	root            string
	file            string
	depth           int
	observations    stringList
	exposure        stringList
	aiCandidates    stringList
	aiCommand       string
	aiArgs          stringList
	reachable       bool
	reachability    stringList
	logInventories  stringList
	traceFiles      stringList
	repositories    stringList
	overlay         overlayFlags
	layout          string
	layoutUnmatched string
}

func runRender(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)

	var f renderFlags
	fs.StringVar(&f.output, "o", "", "write to this file instead of standard output; the extension picks the format")
	fs.StringVar(&f.format, "f", "", "output format: svg, html, dot, mermaid or json")
	fs.StringVar(&f.rankdir, "rankdir", "LR", "layout direction: LR or TB")
	fs.StringVar(&f.lines, "lines", "curved", "in HTML output, the shape every line is drawn with: curved or orthogonal")
	fs.StringVar(&f.kinds, "kind", "", "comma-separated edge kinds to draw (iac_ref, reachable, observed); default all")
	fs.StringVar(&f.sourceDir, "source-dir", "", "directory of .tf files, to record where each resource was declared")
	fs.StringVar(&f.axis, "axis", "", "which grouping to nest by: network (default), provider or module")
	fs.StringVar(&f.scope, "scope", "", "name this estate and qualify every id with it, so documents from several state files can be combined")
	fs.StringVar(&f.title, "title", "", "title drawn above the diagram")
	fs.BoolVar(&f.legend, "legend", false, "include a key explaining the edge kinds")
	fs.BoolVar(&f.fenced, "fenced", false, "wrap mermaid output in a Markdown code fence")
	fs.BoolVar(&f.data, "include-data-sources", false, "draw data.* lookups as nodes too")
	fs.BoolVar(&f.unknownSource, "include-unknown-source", false, "include text files with unrecognized source extensions")
	fs.StringVar(&f.iconDir, "icon-dir", "", "directory of .svg icons to use in HTML output instead of the built-in glyphs")
	fs.StringVar(&f.layout, "layout", "", "apply a human-authored HTML layout document")
	fs.StringVar(&f.layoutUnmatched, "layout-unmatched", "report",
		"what to do about positions naming nothing in this graph: report or error")
	fs.BoolVar(&f.externalAssets, "external-assets", false, "in HTML output, write the graph and the shared runtime as separate files the page loads, instead of one self-contained file; needs -o and a server, because a fetch from file:// is blocked")
	fs.StringVar(&f.assetBase, "asset-base", "", "url prefix the shared HTML runtime is served from, such as /shell/v1; a path segment `auto` becomes the runtime's fingerprint; empty writes it beside the page")
	fs.StringVar(&f.view, "view", "architecture", "diagram view: architecture, network, er, workflow, request-path, security-exposure, code-dependency, service-dependency or reachability")
	fs.StringVar(&f.root, "root", "", "root node id for request-path or reachability view")
	fs.StringVar(&f.file, "file", "", "source file to focus and expand related entities")
	fs.IntVar(&f.depth, "depth", 4, "maximum traversal depth for request-path")
	fs.Var(&f.observations, "observations", "apply observation JSON; repeatable")
	fs.Var(&f.exposure, "exposure", "apply external exposure report JSON; repeatable")
	fs.Var(&f.aiCandidates, "ai-candidates", "apply validated AI candidate JSON; repeatable")
	fs.StringVar(&f.aiCommand, "ai-command", "", "run this explicit local AI executable with the graph on stdin")
	fs.Var(&f.aiArgs, "ai-arg", "argument for --ai-command; repeatable and never shell-expanded")
	fs.BoolVar(&f.reachable, "reachable", false, "derive reachable edges from supported network rules")
	fs.Var(&f.reachability, "reachability", "apply normalized reachability JSON; repeatable")
	fs.Var(&f.logInventories, "log-inventory", "apply classified log inventory JSON; repeatable")
	fs.Var(&f.traceFiles, "traces", "apply request trace JSON; repeatable")
	fs.Var(&f.repositories, "repo", "add a repository, source directory, Terraform output, or graph; repeatable")
	fs.Var(&f.repositories, "input", "alias for --repo; repeatable")
	f.overlay.register(fs, true)
	fs.Usage = func() {
		fmt.Fprintf(env.Stderr, "Usage: oekaki render <input> [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return errors.New("render accepts one positional input plus repeatable --repo inputs")
	}
	inputs := append([]string{}, f.repositories...)
	if fs.NArg() == 1 {
		inputs = append([]string{fs.Arg(0)}, inputs...)
	}
	if len(inputs) == 0 {
		fs.Usage()
		return errors.New("render needs exactly one input file or at least one --repo (use - for standard input)")
	}

	format, err := resolveFormat(f.format, f.output)
	if err != nil {
		return err
	}
	var layoutRaw []byte
	var layoutDoc *layoutdoc.Document
	if f.layout != "" {
		if format != "html" {
			return errors.New("--layout is only supported with HTML output")
		}
		layoutRaw, err = os.ReadFile(f.layout)
		if err != nil {
			return fmt.Errorf("reading layout: %w", err)
		}
		if layoutDoc, err = layoutdoc.Parse(layoutRaw, f.layout); err != nil {
			return err
		}
	}

	g, err := loadGraphs(env, inputs, terraform.Options{
		SourceDir:          f.sourceDir,
		Scope:              f.scope,
		IncludeDataSources: f.data,
	}, sourceparser.Options{IncludeUnknown: f.unknownSource})
	if err != nil {
		return err
	}

	if err := applyOverlays(env, g, f.overlay); err != nil {
		return err
	}
	if err := applyEvidenceInputs(env, g, f.observations, f.exposure, f.aiCandidates); err != nil {
		return err
	}
	if err := applyAIGenerator(ctx, env, g, f.aiCommand, f.aiArgs); err != nil {
		return err
	}
	if err := applyLogInventories(env, g, f.logInventories); err != nil {
		return err
	}
	if err := applyTraces(env, g, f.traceFiles); err != nil {
		return err
	}
	if err := applyReachability(env, g, f.reachability); err != nil {
		return err
	}
	if f.reachable {
		if report, err := (reachable.Enricher{}).Enrich(g); err != nil {
			return err
		} else if report != nil {
			report.WriteText(env.Stderr)
		}
	}
	g, err = views.Apply(g, views.Options{Name: f.view, Root: f.root, File: f.file, Depth: f.depth})
	if err != nil {
		return err
	}
	if f.overlay.hide {
		g = hideSuppressed(g)
	}
	// The graph is settled here: views and suppression have run, so this is
	// what the page will carry and what the layout will be applied to.
	if err := reportLayout(env, g, layoutDoc, f.layout, f.layoutUnmatched); err != nil {
		return err
	}

	kinds, err := parseKinds(f.kinds)
	if err != nil {
		return err
	}

	var out []byte
	switch format {
	case "svg":
		out, err = gvrender.Render(ctx, g, gvrender.Options{
			Title:   f.title,
			RankDir: f.rankdir,
			Axis:    f.axis,
			Kinds:   kinds,
			Legend:  f.legend,
		})
	case "dot":
		var s string
		s, err = dotrender.Render(g, dotrender.Options{
			Title:   f.title,
			RankDir: f.rankdir,
			Axis:    f.axis,
			Kinds:   kinds,
			Legend:  f.legend,
		})
		out = []byte(s)
	case "mermaid":
		var s string
		s, err = mermaid.Render(g, mermaid.Options{
			Direction: f.rankdir,
			Axis:      f.axis,
			Kinds:     kinds,
			Fenced:    f.fenced,
		})
		out = []byte(s)
	case "html":
		hopts := htmlrender.Options{
			Title: f.title, Axis: f.axis, RankDir: f.rankdir, Lines: f.lines, Kinds: kinds,
			IconDir: f.iconDir, Layout: layoutRaw,
		}
		if f.externalAssets {
			if f.output == "" {
				return errors.New("--external-assets writes files beside the page, so it needs -o")
			}
			hopts.ExternalAssets = true
			hopts.AssetBase = resolveAssetBase(f.assetBase)
			hopts.GraphSrc = filepath.Base(graphDocument(f.output))
		}
		out, err = htmlrender.Render(g, hopts)
	case "json":
		out, err = g.MarshalIndent()
	default:
		err = fmt.Errorf("unknown format %q: want svg, html, dot, mermaid or json", format)
	}
	if err != nil {
		return err
	}

	if err := write(env, f.output, out); err != nil {
		return err
	}
	if format == "html" && f.externalAssets {
		return writeHTMLAssets(env, f.output, resolveAssetBase(f.assetBase), g)
	}
	return nil
}

// graphDocument is the file an external page fetches its graph from: the
// page's own name with .graph.json in place of its extension, so that a
// directory holding several diagrams stays readable.
func graphDocument(page string) string {
	return strings.TrimSuffix(page, filepath.Ext(page)) + ".graph.json"
}

// resolveAssetBase turns a path segment spelled "auto" into the runtime's
// fingerprint.
//
// A directory named after the bytes in it can be shared by every page of every
// generation and never has to be invalidated: a build that changed the runtime
// writes somewhere else, and pages rendered against the old one go on reading
// the old one. Overwriting a fixed directory instead hands a fresh runtime to
// pages that were drawn against an older one, and the query fingerprint cannot
// help with that — it changes what the browser caches, not what the server
// has on disk.
func resolveAssetBase(base string) string {
	if base == "" {
		return ""
	}
	parts := strings.Split(base, "/")
	for i, p := range parts {
		if p == "auto" {
			parts[i] = htmlrender.RuntimeStamp()
		}
	}
	return strings.Join(parts, "/")
}

// writeHTMLAssets writes what an external page loads: its graph document
// always, and the shared runtime only when --asset-base names somewhere this
// command can reach.
//
// A base with a scheme, or an absolute path, describes what a server exposes
// rather than where files live. Writing there would be a guess about somebody
// else's layout, and a wrong guess would put a stale runtime next to a fresh
// graph.
func writeHTMLAssets(env Env, page, base string, g *core.Graph) error {
	graphJSON, err := g.MarshalIndent()
	if err != nil {
		return err
	}
	if err := write(env, graphDocument(page), graphJSON); err != nil {
		return err
	}
	if strings.Contains(base, "://") || strings.HasPrefix(base, "/") {
		return nil
	}
	dir := filepath.Join(filepath.Dir(page), filepath.FromSlash(base))
	for name, data := range htmlrender.Assets() {
		if err := write(env, filepath.Join(dir, name), data); err != nil {
			return err
		}
	}
	return nil
}

func runGraph(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("graph", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)

	output := fs.String("o", "", "write to this file instead of standard output")
	sourceDir := fs.String("source-dir", "", "directory of .tf files, to record where each resource was declared")
	scope := fs.String("scope", "", "name this estate and qualify every id with it")
	data := fs.Bool("include-data-sources", false, "include data.* lookups as nodes")
	unknownSource := fs.Bool("include-unknown-source", false, "include text files with unrecognized source extensions")
	var repositories stringList
	var ov overlayFlags
	ov.register(fs, false)
	var observationsFiles, exposureFiles, aiCandidateFiles stringList
	aiCommand := ""
	var aiArgs stringList
	var logInventoryFiles stringList
	var traceFiles stringList
	var reachabilityFiles stringList
	reachableFlag := false
	fs.Var(&observationsFiles, "observations", "apply observation JSON; repeatable")
	fs.Var(&exposureFiles, "exposure", "apply external exposure report JSON; repeatable")
	fs.Var(&aiCandidateFiles, "ai-candidates", "apply validated AI candidate JSON; repeatable")
	fs.StringVar(&aiCommand, "ai-command", "", "run this explicit local AI executable with the graph on stdin")
	fs.Var(&aiArgs, "ai-arg", "argument for --ai-command; repeatable and never shell-expanded")
	fs.BoolVar(&reachableFlag, "reachable", false, "derive reachable edges from supported network rules")
	fs.Var(&logInventoryFiles, "log-inventory", "apply classified log inventory JSON; repeatable")
	fs.Var(&traceFiles, "traces", "apply request trace JSON; repeatable")
	fs.Var(&reachabilityFiles, "reachability", "apply normalized reachability JSON; repeatable")
	fs.Var(&repositories, "repo", "add a repository, source directory, Terraform output, or graph; repeatable")
	fs.Var(&repositories, "input", "alias for --repo; repeatable")
	fs.Usage = func() {
		fmt.Fprintf(env.Stderr, "Usage: oekaki graph <input> [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return errors.New("graph accepts one positional input plus repeatable --repo inputs")
	}
	inputs := append([]string{}, repositories...)
	if fs.NArg() == 1 {
		inputs = append([]string{fs.Arg(0)}, inputs...)
	}
	if len(inputs) == 0 {
		fs.Usage()
		return errors.New("graph needs an input or at least one --repo (use - for standard input)")
	}

	g, err := loadGraphs(env, inputs, terraform.Options{
		SourceDir:          *sourceDir,
		Scope:              *scope,
		IncludeDataSources: *data,
	}, sourceparser.Options{IncludeUnknown: *unknownSource})
	if err != nil {
		return err
	}

	if err := applyOverlays(env, g, ov); err != nil {
		return err
	}
	if err := applyEvidenceInputs(env, g, observationsFiles, exposureFiles, aiCandidateFiles); err != nil {
		return err
	}
	if err := applyAIGenerator(ctx, env, g, aiCommand, aiArgs); err != nil {
		return err
	}
	if err := applyLogInventories(env, g, logInventoryFiles); err != nil {
		return err
	}
	if err := applyTraces(env, g, traceFiles); err != nil {
		return err
	}
	if err := applyReachability(env, g, reachabilityFiles); err != nil {
		return err
	}
	if reachableFlag {
		if report, err := (reachable.Enricher{}).Enrich(g); err != nil {
			return err
		} else if report != nil {
			report.WriteText(env.Stderr)
		}
	}

	out, err := g.MarshalIndent()
	if err != nil {
		return err
	}
	return write(env, *output, out)
}

func applyAIGenerator(ctx context.Context, env Env, g *core.Graph, executable string, args stringList) error {
	if executable == "" {
		return nil
	}
	doc, err := ai.Generate(ctx, executable, []string(args), g)
	if err != nil {
		return err
	}
	report, err := (ai.Enricher{Docs: []*ai.Document{doc}}).Enrich(g)
	for _, need := range doc.Needs {
		hint := ""
		if need.RepositoryHint != "" {
			hint = fmt.Sprintf(" (repository hint: %s)", need.RepositoryHint)
		}
		fmt.Fprintf(env.Stderr, "ai: needs %s %q%s: %s\n", need.Kind, need.Identifier, hint, need.Reason)
		for _, ref := range need.References {
			fmt.Fprintf(env.Stderr, "  reference: %s\n", ref)
		}
	}
	if report != nil {
		report.WriteText(env.Stderr)
	}
	if err != nil {
		return err
	}
	return g.Validate()
}

func applyLogInventories(env Env, g *core.Graph, files stringList) error {
	for _, path := range files {
		raw, err := readInput(env, path)
		if err != nil {
			return err
		}
		var inv loginventorycollector.Inventory
		if err := json.Unmarshal(raw, &inv); err != nil {
			return fmt.Errorf("%s: parsing log inventory: %w", displayName(path), err)
		}
		if err := loginventoryenricher.ValidateInventory(inv); err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		report, err := (loginventoryenricher.Enricher{Inventory: inv}).Enrich(g)
		if report != nil {
			report.WriteText(env.Stderr)
		}
		if err != nil {
			return err
		}
	}
	return g.Validate()
}

func applyTraces(env Env, g *core.Graph, files stringList) error {
	if len(files) == 0 {
		return nil
	}
	docs := make([]*tracecollector.Document, 0, len(files))
	for _, path := range files {
		raw, err := readInput(env, path)
		if err != nil {
			return err
		}
		d, err := tracecollector.Parse(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		docs = append(docs, d)
	}
	report, err := (traceenricher.Enricher{Documents: docs}).Enrich(g)
	if report != nil {
		report.WriteText(env.Stderr)
	}
	if err != nil {
		return err
	}
	return g.Validate()
}

func applyReachability(env Env, g *core.Graph, files stringList) error {
	if len(files) == 0 {
		return nil
	}
	docs := make([]*reachable.Document, 0, len(files))
	for _, path := range files {
		raw, err := readInput(env, path)
		if err != nil {
			return err
		}
		d, err := reachable.Parse(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		if err := schema.ValidateReachability(raw); err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		docs = append(docs, d)
	}
	report, err := (reachable.Enricher{Documents: docs}).Enrich(g)
	if report != nil {
		report.WriteText(env.Stderr)
	}
	if err != nil {
		return err
	}
	return g.Validate()
}

func applyEvidenceInputs(env Env, g *core.Graph, observationFiles, exposureFiles, aiCandidateFiles stringList) error {
	var observationDocs []*observations.Document
	for _, path := range observationFiles {
		raw, err := readInput(env, path)
		if err != nil {
			return err
		}
		d, err := observations.Parse(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		observationDocs = append(observationDocs, d)
	}
	if len(observationDocs) > 0 {
		report, err := (observations.Enricher{Docs: observationDocs}).Enrich(g)
		if report != nil {
			report.WriteText(env.Stderr)
		}
		if err != nil {
			return err
		}
	}
	var exposureDocs []*exposure.Document
	for _, path := range exposureFiles {
		raw, err := readInput(env, path)
		if err != nil {
			return err
		}
		d, err := exposure.Parse(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		exposureDocs = append(exposureDocs, d)
	}
	if len(exposureDocs) > 0 {
		report, err := (exposure.Enricher{Docs: exposureDocs}).Enrich(g)
		if report != nil {
			report.WriteText(env.Stderr)
		}
		if err != nil {
			return err
		}
	}
	var candidateDocs []*ai.Document
	for _, path := range aiCandidateFiles {
		raw, err := readInput(env, path)
		if err != nil {
			return err
		}
		if err := schema.ValidateAICandidates(raw); err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		d, err := ai.Parse(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		candidateDocs = append(candidateDocs, d)
	}
	if len(candidateDocs) > 0 {
		report, err := (ai.Enricher{Docs: candidateDocs}).Enrich(g)
		if report != nil {
			report.WriteText(env.Stderr)
		}
		if err != nil {
			return err
		}
	}
	return g.Validate()
}

func runValidate(env Env, args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(env.Stderr, "Usage: oekaki validate <graph.json | overlay.json | layout.json | ai-candidates.json | reachability.json | observations.json>\n")
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("validate needs exactly one document (use - for standard input)")
	}

	raw, err := read(env, fs.Arg(0))
	if err != nil {
		return err
	}

	// An overlay is the other document this project publishes a contract for,
	// and the schema is the product, so both deserve the same front door.
	if schema.IsOverlay(raw) {
		doc, err := overlay.Parse(raw, displayName(fs.Arg(0)))
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "ok: %d assertions, %d sinks\n", len(doc.Assertions), len(doc.Sinks))
		return nil
	}
	if schema.IsLayout(raw) {
		doc, err := layoutdoc.Parse(raw, displayName(fs.Arg(0)))
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "ok: %d layout nodes, %d lines\n", len(doc.Nodes), len(doc.Edges))
		return nil
	}
	if schema.IsAICandidates(raw) {
		if err := schema.ValidateAICandidates(raw); err != nil {
			return err
		}
		if _, err := ai.Parse(raw); err != nil {
			return err
		}
		fmt.Fprintln(env.Stdout, "ok: AI candidate document")
		return nil
	}
	if schema.IsReachability(raw) {
		if err := schema.ValidateReachability(raw); err != nil {
			return err
		}
		if _, err := reachable.Parse(raw); err != nil {
			return err
		}
		fmt.Fprintln(env.Stdout, "ok: reachability document")
		return nil
	}
	if schema.IsObservations(raw) {
		if err := schema.ValidateObservations(raw); err != nil {
			return err
		}
		if _, err := observations.Parse(raw); err != nil {
			return err
		}
		fmt.Fprintln(env.Stdout, "ok: observations document")
		return nil
	}

	// Both checks run because they catch different things: the schema covers
	// shape, and Decode covers the referential integrity JSON Schema cannot
	// express, such as an edge pointing at a node that is not there.
	if err := schema.Validate(raw); err != nil {
		return err
	}
	g, err := core.Decode(strings.NewReader(string(raw)))
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Stdout, "ok: %d nodes, %d edges, %d groups\n", len(g.Nodes), len(g.Edges), len(g.Groups))
	return nil
}

func runSchema(env Env, args []string) error {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	output := fs.String("o", "", "write to this file instead of standard output")
	wantOverlay := fs.Bool("overlay", false, "print the overlay schema instead of the IR schema")
	wantAICandidates := fs.Bool("ai-candidates", false, "print the AI candidate schema instead of the IR schema")
	wantReachability := fs.Bool("reachability", false, "print the reachability schema instead of the IR schema")
	wantObservations := fs.Bool("observations", false, "print the observations schema instead of the IR schema")
	wantLayout := fs.Bool("layout", false, "print the layout schema instead of the IR schema")
	wantConventions := fs.Bool("conventions", false, "print the conventions schema instead of the IR schema")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *wantOverlay {
		return write(env, *output, schema.OverlaySchema)
	}
	if *wantAICandidates {
		return write(env, *output, schema.AICandidatesSchema)
	}
	if *wantReachability {
		return write(env, *output, schema.ReachabilitySchema)
	}
	if *wantObservations {
		return write(env, *output, schema.ObservationsSchema)
	}
	if *wantConventions {
		return write(env, *output, schema.ConventionsSchema)
	}
	if *wantLayout {
		return write(env, *output, schema.LayoutSchema)
	}
	return write(env, *output, schema.GraphSchema)
}

// loadGraph accepts either Terraform output or an IR document, so that
// `oekaki graph ... | oekaki render -` works and so that a graph
// committed to a repository can be re-rendered without the original plan.
func loadGraph(env Env, path string, opts terraform.Options, sourceOpts sourceparser.Options) (*core.Graph, error) {
	if path != "-" {
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			g, parseErr := sourceparser.ParseDirWithOptions(path, sourceOpts)
			if parseErr != nil {
				return nil, fmt.Errorf("reading %s as source: %w", describe(path), parseErr)
			}
			return g, nil
		}
	}
	raw, err := read(env, path)
	if err != nil {
		return nil, err
	}

	var probe struct {
		Version       string          `json:"version"`
		Nodes         json.RawMessage `json:"nodes"`
		FormatVersion string          `json:"format_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", describe(path), err)
	}

	if probe.Version != "" && probe.Nodes != nil {
		g, err := core.Decode(strings.NewReader(string(raw)))
		if err != nil {
			return nil, fmt.Errorf("reading %s as a graph: %w", describe(path), err)
		}
		return g, nil
	}

	if probe.FormatVersion == "" {
		return nil, fmt.Errorf("%s is neither `terraform show -json` output nor an oekaki graph", describe(path))
	}

	g, err := terraform.Parse(raw, opts)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", describe(path), err)
	}
	if g.Metadata != nil {
		g.Metadata.Generator = "oekaki/" + version()
	}
	return g, nil
}

// loadGraphs builds one graph from the positional input and every --repo (or
// --input) flag. A single input keeps its historical identifiers; multiple
// inputs are placed in deterministic repository namespaces so two repositories
// may both contain a `main` package or an `aws_vpc.main` state address without
// silently merging unrelated entities.
func loadGraphs(env Env, paths []string, opts terraform.Options, sourceOpts sourceparser.Options) (*core.Graph, error) {
	if len(paths) == 0 {
		return nil, errors.New("no graph inputs")
	}
	if len(paths) == 1 {
		return loadGraph(env, paths[0], opts, sourceOpts)
	}
	combined := core.New()
	combined.Metadata = &core.Metadata{Generator: "oekaki/" + version(), Source: "combined"}
	combined.Metadata.Scope = opts.Scope
	nodeIndex := map[string]int{}
	for i, path := range paths {
		inputOpts := opts
		// The repository namespace below is authoritative for combined input.
		// Passing a caller scope through here would qualify Terraform once and
		// then qualify it again, so it is applied in one place only.
		inputOpts.Scope = ""
		g, err := loadGraph(env, path, inputOpts, sourceOpts)
		if err != nil {
			return nil, err
		}
		scope := repositoryScope(path, i)
		qualifyGraph(g, scope)
		sourceVersion := ""
		if g.Metadata != nil {
			sourceVersion = g.Metadata.SourceVersion
		}
		combined.Metadata.Inputs = append(combined.Metadata.Inputs, core.InputRef{
			ID:            scope,
			Path:          path,
			Kind:          inputKind(g),
			SourceVersion: sourceVersion,
		})
		if g.Metadata != nil {
			combined.Metadata.Inputs = append(combined.Metadata.Inputs, g.Metadata.Inputs...)
			combined.Metadata.Overlays = append(combined.Metadata.Overlays, g.Metadata.Overlays...)
		}
		combined.Axes = appendUniqueAxes(combined.Axes, g.Axes)
		for _, node := range g.Nodes {
			if existing, ok := nodeIndex[node.ID]; ok {
				if !strings.HasPrefix(node.ID, "external:") || !reflect.DeepEqual(combined.Nodes[existing], node) {
					return nil, fmt.Errorf("combining repositories: shared node %q has conflicting definitions", node.ID)
				}
				continue
			}
			nodeIndex[node.ID] = len(combined.Nodes)
			combined.Nodes = append(combined.Nodes, node)
		}
		combined.Groups = append(combined.Groups, g.Groups...)
		combined.Edges = append(combined.Edges, g.Edges...)
		combined.Observations = append(combined.Observations, g.Observations...)
		combined.LogRecords = append(combined.LogRecords, g.LogRecords...)
		combined.Conflicts = append(combined.Conflicts, g.Conflicts...)
		combined.LogStatus = mergeLogStatus(combined.LogStatus, g.LogStatus, scope)
	}
	combined.Normalize()
	if err := combined.Validate(); err != nil {
		return nil, fmt.Errorf("validating combined repositories: %w", err)
	}
	return combined, nil
}

func mergeLogStatus(dst, src *core.LogCollectionStatus, scope string) *core.LogCollectionStatus {
	if src == nil {
		return dst
	}
	if dst == nil {
		dst = &core.LogCollectionStatus{}
	}
	dst.Fetched += src.Fetched
	dst.Classified += src.Classified
	dst.StartedAt = selectTimestamp(dst.StartedAt, src.StartedAt, true)
	dst.CompletedAt = selectTimestamp(dst.CompletedAt, src.CompletedAt, false)
	if src.LastError != "" {
		entry := scope + ": " + src.LastError
		if dst.LastError == "" {
			dst.LastError = entry
		} else {
			dst.LastError += "; " + entry
		}
	}
	return dst
}

func selectTimestamp(current, candidate string, earlier bool) string {
	if current == "" {
		return candidate
	}
	if candidate == "" {
		return current
	}
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
	if currentErr == nil && candidateErr == nil {
		if candidateTime.Equal(currentTime) {
			if candidate < current {
				return candidate
			}
			return current
		}
		if candidateTime.Before(currentTime) == earlier {
			return candidate
		}
		return current
	}
	// Prefer a valid timestamp to malformed legacy data. If neither parses,
	// lexical order is at least deterministic and preserves the old behavior.
	if candidateErr == nil {
		return candidate
	}
	if currentErr == nil {
		return current
	}
	if (candidate < current) == earlier {
		return candidate
	}
	return current
}

func inputKind(g *core.Graph) string {
	if g.Metadata == nil {
		return "graph"
	}
	switch g.Metadata.Source {
	case "source":
		return "repository"
	case "terraform":
		return "terraform"
	default:
		return "graph"
	}
}

func appendUniqueAxes(dst []core.Axis, src []core.Axis) []core.Axis {
	seen := make(map[string]bool, len(dst))
	for _, axis := range dst {
		seen[axis.ID] = true
	}
	for _, axis := range src {
		if !seen[axis.ID] {
			dst = append(dst, axis)
			seen[axis.ID] = true
		}
	}
	return dst
}

func repositoryScope(path string, index int) string {
	base := filepath.Base(filepath.Clean(path))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "repository"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "repository"
	}
	return fmt.Sprintf("repo-%d-%s", index+1, name)
}

func qualifyGraph(g *core.Graph, scope string) {
	qualify := func(id string) string {
		if id == "" || strings.HasPrefix(id, "external:") {
			return id
		}
		return scope + ":" + id
	}
	for i := range g.Nodes {
		old := g.Nodes[i].ID
		g.Nodes[i].ID = qualify(old)
		if g.Nodes[i].ID != old {
			if g.Nodes[i].Attrs == nil {
				g.Nodes[i].Attrs = map[string]any{}
			}
			g.Nodes[i].Attrs["repository"] = scope
		}
		for axis, path := range g.Nodes[i].Groups {
			parts := strings.Split(path, core.GroupSeparator)
			for j := range parts {
				parts[j] = qualify(parts[j])
			}
			g.Nodes[i].Groups[axis] = strings.Join(parts, core.GroupSeparator)
		}
		if g.Nodes[i].Coverage != nil {
			for j := range g.Nodes[i].Coverage.Evidence {
				g.Nodes[i].Coverage.Evidence[j].Sink = qualify(g.Nodes[i].Coverage.Evidence[j].Sink)
			}
		}
		qualifySecurityGroupRefs(g.Nodes[i].Attrs, scope)
	}
	for i := range g.Groups {
		g.Groups[i].ID = qualify(g.Groups[i].ID)
		if g.Groups[i].Parent != nil {
			parent := qualify(*g.Groups[i].Parent)
			g.Groups[i].Parent = &parent
		}
		if g.Groups[i].Attrs == nil {
			g.Groups[i].Attrs = map[string]any{}
		}
		g.Groups[i].Attrs["repository"] = scope
	}
	for i := range g.Edges {
		g.Edges[i].From = qualify(g.Edges[i].From)
		g.Edges[i].To = qualify(g.Edges[i].To)
	}
	for i := range g.Observations {
		g.Observations[i].Subject = qualify(g.Observations[i].Subject)
	}
	for i := range g.LogRecords {
		g.LogRecords[i].ID = qualify(g.LogRecords[i].ID)
		g.LogRecords[i].Source = qualify(g.LogRecords[i].Source)
	}
	for i := range g.Conflicts {
		switch g.Conflicts[i].TargetKind {
		case core.ConflictTargetEntity:
			g.Conflicts[i].Target = qualify(g.Conflicts[i].Target)
		case core.ConflictTargetEdge:
			from, to, kind, relation, ok := core.ParseEdgeKey(g.Conflicts[i].Target)
			if ok {
				g.Conflicts[i].Target = core.EdgeKey(qualify(from), qualify(to), kind, relation)
			}
		}
	}
	if g.Metadata == nil {
		g.Metadata = &core.Metadata{}
	}
	for i := range g.Metadata.Inputs {
		g.Metadata.Inputs[i].ID = qualify(g.Metadata.Inputs[i].ID)
	}
	g.Metadata.Scope = scope
}

// qualifySecurityGroupRefs updates references embedded in Terraform-style
// inline rule attributes. They are not edges, so qualifying only edge
// endpoints would leave reachability unable to resolve them in a combined
// graph.
func qualifySecurityGroupRefs(attrs map[string]any, scope string) {
	if attrs == nil {
		return
	}
	for key, value := range attrs {
		switch key {
		case "security_groups", "security_group_id", "source_security_group_id":
			attrs[key] = qualifySecurityGroupValue(value, scope)
		default:
			if nested, ok := value.(map[string]any); ok {
				qualifySecurityGroupRefs(nested, scope)
			}
			if values, ok := value.([]any); ok {
				for _, item := range values {
					if nested, ok := item.(map[string]any); ok {
						qualifySecurityGroupRefs(nested, scope)
					}
				}
			}
		}
	}
}

func qualifySecurityGroupValue(value any, scope string) any {
	qualify := func(s string) string {
		if s == "" || strings.HasPrefix(s, "external:") || strings.HasPrefix(s, scope+":") {
			return s
		}
		return scope + ":" + s
	}
	switch values := value.(type) {
	case string:
		return qualify(values)
	case []string:
		out := append([]string(nil), values...)
		for i := range out {
			out[i] = qualify(out[i])
		}
		return out
	case []any:
		out := append([]any(nil), values...)
		for i, item := range out {
			if s, ok := item.(string); ok {
				out[i] = qualify(s)
			}
		}
		return out
	default:
		return value
	}
}

// parse permutes the arguments before handing them to the flag package, which
// otherwise stops at the first positional. Without this, `oekaki render
// plan.json -o out.svg` would quietly ignore -o and print SVG to the terminal,
// which reads as the tool being broken.
func parse(fs *flag.FlagSet, args []string) error {
	var flags, positional []string

	for i := 0; i < len(args); i++ {
		a := args[i]

		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}

		flags = append(flags, a)

		name, _, hasInlineValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if hasInlineValue {
			// -flag=value carries its own value.
			continue
		}

		def := fs.Lookup(name)
		if def == nil {
			// Unknown flag: let the flag package produce the error message.
			continue
		}
		if bf, ok := def.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	return fs.Parse(append(flags, positional...))
}

func parseKinds(s string) ([]core.EdgeKind, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []core.EdgeKind
	for part := range strings.SplitSeq(s, ",") {
		k := core.EdgeKind(strings.TrimSpace(part))
		if !k.Valid() {
			return nil, fmt.Errorf("unknown edge kind %q: want iac_ref, reachable or observed", part)
		}
		out = append(out, k)
	}
	return out, nil
}

// resolveFormat prefers an explicit -f, falls back to the output file's
// extension, and defaults to SVG.
func resolveFormat(explicit, output string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	switch strings.ToLower(filepath.Ext(output)) {
	case ".svg":
		return "svg", nil
	case ".dot", ".gv":
		return "dot", nil
	case ".mmd", ".mermaid", ".md":
		return "mermaid", nil
	case ".html", ".htm":
		return "html", nil
	case ".json":
		return "json", nil
	case "":
		return "svg", nil
	default:
		return "", fmt.Errorf("cannot tell the format from %q: pass -f svg, html, dot, mermaid or json", output)
	}
}

func read(env Env, path string) ([]byte, error) {
	if path == "-" {
		raw, err := io.ReadAll(env.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading standard input: %w", err)
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func write(env Env, path string, data []byte) error {
	if path == "" {
		_, err := env.Stdout.Write(data)
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func describe(path string) string {
	if path == "-" {
		return "standard input"
	}
	return path
}
