package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/imohiyoko/oekaki/core"
	sourceparser "github.com/imohiyoko/oekaki/parsers/source"
	"github.com/imohiyoko/oekaki/parsers/terraform"
	"github.com/imohiyoko/oekaki/views"
)

// runPaths lists what the declared and the observed routes say about each
// other.
func runPaths(env Env, args []string) error {
	fs := flag.NewFlagSet("paths", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	output := fs.String("o", "", "write to this file instead of standard output")
	format := fs.String("f", "table", "output format: table or json")
	since := fs.String("since", "",
		"a route last walked before this is quiet: an RFC3339 time, or a span like 30d or 12h back from now")
	metric := fs.String("metric", views.DefaultPathMetric, "the reading that counts walks")
	only := fs.String("only", "", "list one kind of finding: "+strings.Join(views.PathFindingKinds(), ", "))
	declare := fs.Bool("derive-declared", true,
		"when the graph carries no declared routes, derive them by following declared references; a route written down by somebody is always preferred")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("paths takes one graph")
	}
	if *format != "table" && *format != "json" {
		return fmt.Errorf("unknown format %q: want table or json", *format)
	}
	if *only != "" && !views.ValidPathFinding(*only) {
		return fmt.Errorf("unknown finding %q: want %s", *only, strings.Join(views.PathFindingKinds(), ", "))
	}

	cutoff, err := resolveSince(*since, time.Now)
	if err != nil {
		return err
	}

	g, err := loadGraph(env, fs.Arg(0), terraform.Options{}, sourceparser.Options{})
	if err != nil {
		return err
	}
	// A document that carries declared routes has better ones than anything
	// derived here, so the derivation only fills a gap and never argues with
	// what somebody wrote down.
	derived, attempted := 0, false
	if *declare {
		written := false
		for _, p := range g.Paths {
			if p.Kind != core.EdgeObserved {
				written = true
				break
			}
		}
		if !written {
			attempted = true
			routes := views.DeclarePaths(g, views.DeclareOptions{})
			derived = len(routes)
			g.Paths = append(g.Paths, routes...)
			g.Normalize()
		}
	}

	findings, err := views.Paths(g, views.PathOptions{Since: cutoff, Metric: *metric})
	if err != nil {
		return err
	}
	if *only != "" {
		kept := findings[:0]
		for _, f := range findings {
			if f.Kind == *only {
				kept = append(kept, f)
			}
		}
		findings = kept
	}

	switch {
	case derived > 0:
		fmt.Fprintf(env.Stderr,
			"%d declared route%s derived by following references; nothing wrote them down\n", derived, plural(derived))
	case attempted:
		// Silence here reads as "everything observed is a surprise", which is
		// exactly what the listing then says. The usual reason is that every
		// way in is also called by something else: an estate whose entry
		// point sits in a cycle has nowhere for a route to start.
		fmt.Fprintln(env.Stderr,
			"no declared routes could be derived: nothing here is called only from outside, so there is nowhere a route starts. Write the routes down in an overlay, or every observed route will read as unannounced")
	}

	// A graph with no routes in it is the ordinary case until somebody runs a
	// collector, and an empty list looks exactly like "everything is used".
	// Say which it is.
	if len(g.Paths) == 0 {
		fmt.Fprintln(env.Stderr,
			"this graph records no routes; add traces with --traces when generating it, or the listing has nothing to compare")
	}

	var out []byte
	if *format == "json" {
		out, err = json.MarshalIndent(struct {
			Since    string          `json:"since,omitempty"`
			Findings []views.Finding `json:"findings"`
		}{cutoff, findings}, "", "  ")
		if err != nil {
			return err
		}
		out = append(out, '\n')
	} else {
		var b strings.Builder
		for _, f := range findings {
			fmt.Fprintf(&b, "%-10s  %s\n", f.Kind, views.PathLabel(g, f.Path))
			line := f.Reason
			// When it was last walked, and how many walks that reading
			// counted. A finding without it is a claim the reader has to go
			// and check somewhere else.
			if f.LastSeen != "" {
				line += "  (last " + f.LastSeen
				if f.Requests != nil {
					line += fmt.Sprintf(", %g requests", *f.Requests)
				}
				line += ")"
			}
			fmt.Fprintf(&b, "%-10s  %s\n", "", line)
		}
		out = []byte(b.String())
	}

	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Kind]++
	}
	var parts []string
	for _, kind := range views.PathFindingKinds() {
		if counts[kind] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[kind], kind))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing to report")
	}
	fmt.Fprintf(env.Stderr, "%d routes: %s\n", len(g.Paths), strings.Join(parts, ", "))

	return write(env, *output, out)
}

// resolveSince turns what somebody typed into the timestamp the view takes.
//
// A span is resolved here rather than in views, because "thirty days" is a
// question about today and today is not something a projection may read. The
// answer is written into the output, so a listing says which moment it was
// asking about even when the question was relative.
func resolveSince(since string, now func() time.Time) (string, error) {
	if since == "" {
		return "", nil
	}
	if at, err := time.Parse(time.RFC3339, since); err == nil {
		return at.UTC().Format(time.RFC3339), nil
	}

	unit := since[len(since)-1:]
	scale, ok := map[string]time.Duration{
		"d": 24 * time.Hour,
		"h": time.Hour,
		"m": time.Minute,
	}[unit]
	if !ok {
		return "", fmt.Errorf("--since %q: want an RFC3339 time, or a span like 30d, 12h or 90m", since)
	}
	// The whole prefix has to be the number. Scanning it leaves whatever
	// followed unread, so "3xd" would quietly mean three days.
	n, err := strconv.Atoi(since[:len(since)-1])
	if err != nil || n <= 0 {
		return "", fmt.Errorf("--since %q: want an RFC3339 time, or a span like 30d, 12h or 90m", since)
	}
	return now().UTC().Add(-time.Duration(n) * scale).Format(time.RFC3339), nil
}
