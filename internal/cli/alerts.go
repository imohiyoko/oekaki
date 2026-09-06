package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/imohiyoko/oekaki/core"
	sourceparser "github.com/imohiyoko/oekaki/parsers/source"
	"github.com/imohiyoko/oekaki/parsers/terraform"
	"github.com/imohiyoko/oekaki/views"
)

// runAlerts runs a rules document against a graph.
func runAlerts(env Env, args []string) error {
	fs := flag.NewFlagSet("alerts", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	output := fs.String("o", "", "write to this file instead of standard output")
	format := fs.String("f", "table", "output format: table or json")
	rules := fs.String("rules", "", "the rules document to run")
	since := fs.String("since", "",
		"the moment a rule means by \"since\", when it does not say one: an RFC3339 time, or a span like 30d or 12h back from now")
	failing := fs.Bool("exit-code", false, "exit 1 when anything fired, for a pipeline that should stop")
	declare := fs.Bool("derive-declared", true,
		"when the graph carries no declared routes, derive them by following declared references, as oekaki paths does")
	fs.Usage = func() {
		fmt.Fprintf(env.Stderr, "Usage: oekaki alerts <graph> --rules rules.json [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("alerts takes one graph")
	}
	if *rules == "" {
		return fmt.Errorf("alerts needs --rules; there is nothing to check against otherwise")
	}
	if *format != "table" && *format != "json" {
		return fmt.Errorf("unknown format %q: want table or json", *format)
	}

	cutoff, err := resolveSince(*since, time.Now)
	if err != nil {
		return err
	}

	raw, err := read(env, *rules)
	if err != nil {
		return err
	}
	// The moment goes in with the document. A rule that asks what has gone
	// quiet cannot be judged without one, so filling it in afterwards meant
	// --since could never reach the one condition that needs it: the document
	// was refused before the flag was applied.
	//
	// Rules that name their own keep them: a document that says "since the
	// first of August" means it whoever runs it and whenever.
	doc, err := views.ParseRules(raw, cutoff)
	if err != nil {
		return err
	}

	g, err := loadGraph(env, fs.Arg(0), terraform.Options{}, sourceparser.Options{})
	if err != nil {
		return err
	}
	// The same graph must not answer two questions differently. `oekaki paths`
	// derives the declared routes when nothing wrote them down, and a rule
	// about routes is asking for exactly that comparison — so it is derived
	// here too, and said out loud, rather than every observed route arriving
	// as a surprise because the declared side of the comparison was empty.
	if *declare {
		written := false
		for _, p := range g.Paths {
			if p.Kind != core.EdgeObserved {
				written = true
				break
			}
		}
		if !written {
			routes := views.DeclarePaths(g, views.DeclareOptions{})
			switch {
			case len(routes) > 0:
				g.Paths = append(g.Paths, routes...)
				g.Normalize()
				fmt.Fprintf(env.Stderr,
					"%d declared route%s derived by following references; nothing wrote them down\n",
					len(routes), plural(len(routes)))
			case len(g.Paths) > 0:
				// Silence here reads as "everything observed is a surprise",
				// which is exactly what a rule about unexpected routes then
				// says — one alert per route, none of them about anything
				// that happened.
				fmt.Fprintln(env.Stderr,
					"no declared routes could be derived: nothing here is called only from outside, so there is nowhere a route starts. Until the routes are written down, every observed route will read as unannounced")
			}
		}
	}

	alerts, err := views.Alerts(g, doc)
	if err != nil {
		return err
	}

	var out []byte
	if *format == "json" {
		out, err = json.MarshalIndent(struct {
			Since  string        `json:"since,omitempty"`
			Alerts []views.Alert `json:"alerts"`
		}{cutoff, alerts}, "", "  ")
		if err != nil {
			return err
		}
		out = append(out, '\n')
	} else {
		var b strings.Builder
		for _, a := range alerts {
			head := a.Rule
			if a.Severity != "" {
				head = a.Severity + "  " + head
			}
			line := a.Reason
			// When it was last measured, and what it said. It is what the path
			// listing prints and what somebody deciding whether to act needs;
			// a reason without them is a claim to go and check elsewhere.
			if a.LastSeen != "" || a.Value != nil {
				line += "  ("
				if a.LastSeen != "" {
					line += "last " + a.LastSeen
				}
				if a.Value != nil {
					if a.LastSeen != "" {
						line += ", "
					}
					line += fmt.Sprintf("%g", *a.Value)
				}
				line += ")"
			}
			fmt.Fprintf(&b, "%s\n    %s\n    %s\n", head, a.Label, line)
		}
		out = []byte(b.String())
	}

	if len(alerts) == 0 {
		fmt.Fprintf(env.Stderr, "%d rule%s, nothing fired\n", len(doc.Rules), plural(len(doc.Rules)))
	} else {
		fmt.Fprintf(env.Stderr, "%d rule%s, %d fired\n", len(doc.Rules), plural(len(doc.Rules)), len(alerts))
	}
	if err := write(env, *output, out); err != nil {
		return err
	}
	// A pipeline that should stop asks for it. The default is to report and
	// carry on, because a listing is also something people read while nothing
	// is wrong.
	if *failing && len(alerts) > 0 {
		return errFired
	}
	return nil
}

// errFired is not a mistake anybody made, so it says nothing about how to fix
// itself. It exists to move the exit code.
var errFired = fmt.Errorf("rules fired")
