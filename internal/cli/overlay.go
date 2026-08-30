package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers/overlay"
	layout "github.com/imohiyoko/oekaki/layout"
)

// stringList collects a flag that may be given more than once.
//
// Repeatable rather than comma-separated because the browser export flow
// naturally produces "overlay.json", "overlay (1).json" and so on: filenames
// with spaces in them, which a comma-separated list would handle badly.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

type overlayFlags struct {
	files     stringList
	unmatched string
	report    string
	hide      bool
}

func (o *overlayFlags) register(fs *flag.FlagSet, withHide bool) {
	fs.Var(&o.files, "overlay", "apply an overlay of assertions; repeatable, - reads standard input")
	fs.StringVar(&o.unmatched, "overlay-unmatched", string(overlay.PolicyAdopt),
		"what to do with an assertion that matches nothing: adopt, report or error")
	fs.StringVar(&o.report, "overlay-report", "", "write the resolution report to this file as JSON")
	if withHide {
		fs.BoolVar(&o.hide, "hide-suppressed", false, "omit edges an overlay asserted are not real")
	}
}

// applyOverlays enriches g in place.
//
// The summary goes to stderr on every run, not only when something failed. A
// message that appears only on failure teaches people to read its absence as
// success, and the interesting number here — how much of the estate nobody has
// assessed — is not a failure.
func applyOverlays(env Env, g *core.Graph, o overlayFlags) error {
	if len(o.files) == 0 {
		return nil
	}

	policy := overlay.Policy(o.unmatched)
	if !policy.Valid() {
		return fmt.Errorf("unknown -overlay-unmatched %q: want adopt, report or error", o.unmatched)
	}

	docs := make([]*overlay.Document, 0, len(o.files))
	for _, path := range o.files {
		raw, err := readInput(env, path)
		if err != nil {
			return err
		}
		doc, err := overlay.Parse(raw, displayName(path))
		if err != nil {
			return err
		}
		docs = append(docs, doc)
	}

	report, enrichErr := overlay.New(docs, overlay.Options{Unmatched: policy}).Enrich(g)
	if report != nil {
		report.WriteText(env.Stderr)

		if o.report != "" {
			out, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			if err := write(env, o.report, append(out, '\n')); err != nil {
				return err
			}
		}
	}
	if enrichErr != nil {
		return enrichErr
	}
	return g.Validate()
}

// hideSuppressed drops edges an overlay said are not real.
//
// It runs on a copy of the edge slice rather than on the document, so the
// suppression survives in the IR: the flag is the record that somebody
// disagreed, and only the picture is allowed to forget it.
func hideSuppressed(g *core.Graph) *core.Graph {
	out := *g
	out.Edges = nil
	out.Conflicts = append([]core.Conflict(nil), g.Conflicts...)
	for _, e := range g.Edges {
		if e.Suppressed {
			continue
		}
		out.Edges = append(out.Edges, e)
	}
	filterGraphConflicts(&out)
	return &out
}

func filterGraphConflicts(g *core.Graph) {
	conflicts := g.Conflicts[:0]
	for _, conflict := range g.Conflicts {
		if g.HasConflictTarget(conflict.Target, conflict.TargetKind) {
			conflicts = append(conflicts, conflict)
		}
	}
	g.Conflicts = conflicts
}

func readInput(env Env, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(env.Stdin)
	}
	return os.ReadFile(path)
}

func displayName(path string) string {
	if path == "-" {
		return "standard input"
	}
	return path
}

// reportLayout says how much of a layout document this graph can actually use.
//
// A layout is applied by embedding it: the browser places the nodes it
// recognises and ignores the rest. That is the right behaviour — a graph that
// grew a node should still draw — but it is silent. A layout written against
// last month's estate still applies, just less of it, and nothing says so.
// Overlays have reported this since they existed; layouts did not.
//
// The summary goes to stderr on every run for the reason applyOverlays gives:
// a message that appears only on failure teaches people to read its absence as
// success, and "40 of 60 positions no longer match anything" is not a failure.
// It is the number you want before you keep building on that layout.
func reportLayout(env Env, g *core.Graph, doc *layout.Document, path, unmatched string) error {
	if doc == nil {
		return nil
	}
	switch unmatched {
	case string(overlay.PolicyReport), string(overlay.PolicyError):
	default:
		// Adopt is an overlay's answer: an assertion naming nothing can become
		// a node, because an assertion is a statement that something exists. A
		// position is not, so there is nothing here to adopt it into.
		return fmt.Errorf("unknown -layout-unmatched %q: want report or error", unmatched)
	}
	known := make(map[string]struct{}, len(g.Nodes)+len(g.Groups))
	for _, n := range g.Nodes {
		known[n.ID] = struct{}{}
	}
	for _, group := range g.Groups {
		known[group.ID] = struct{}{}
	}

	at := doc.Against(known)

	fmt.Fprintf(env.Stderr, "layout %s: %d positions, %d placed, %d not in this graph\n",
		filepath.Base(path), at.Total(), at.Placed, len(at.Missing))
	// Name them. A count tells you something drifted; the ids tell you whether
	// it was one renamed account or the whole file pointing at the wrong graph.
	const show = 5
	for i, id := range at.Missing {
		if i == show {
			fmt.Fprintf(env.Stderr, "  ... and %d more\n", len(at.Missing)-show)
			break
		}
		fmt.Fprintf(env.Stderr, "  not placed: %s\n", id)
	}

	// Drift that is only ever printed is drift nobody acts on. An overlay can
	// be made to fail the build over it and a layout could not, which meant
	// the two halves of the same idea had different teeth.
	if unmatched == string(overlay.PolicyError) && len(at.Missing) > 0 {
		return fmt.Errorf("layout %s: %d position%s name nothing in this graph",
			filepath.Base(path), len(at.Missing), plural(len(at.Missing)))
	}
	return nil
}
