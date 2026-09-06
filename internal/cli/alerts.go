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
				fmt.Fprintln(env.Stderr, whyNoRoutes(g)+
					" Until the routes are written down, every observed route will read as unannounced")
			}
		}
	}

	alerts, unanswered, err := views.Alerts(g, doc)
	if err != nil {
		return err
	}
	// What the rules could not answer, before what they did. A rule that had
	// nothing to apply itself to reports the same silence as one that applied
	// itself and found nothing, and only one of the two is good news.
	for _, notice := range unanswered {
		fmt.Fprintln(env.Stderr, notice)
	}

	var out []byte
	if *format == "json" {
		out, err = json.MarshalIndent(struct {
			Since string `json:"since,omitempty"`
			// What the rules could not answer, beside what they did. A
			// consumer reading only `alerts` reads an empty list as "all
			// well", and a rule that was never applied produces exactly that
			// empty list — which is the failure this whole change is about,
			// moved from the table to the machine-readable side.
			Unanswered []string      `json:"unanswered,omitempty"`
			Alerts     []views.Alert `json:"alerts"`
		}{cutoff, unanswered, alerts}, "", "  ")
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
			line := a.Reason + suffix(a)
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

// suffix is when it was last measured and what it said — the two things
// somebody deciding whether to act needs, and a reason without them is a claim
// to go and check somewhere else.
//
// Only the half the reason does not already carry. Which half that is follows
// from the condition that wrote the reason: a bound's sentence names its value
// ("request_rate is 4000, above 1000"), a silence's names its moment ("beat
// last measured 2026-01-01T00:00:00Z"). Asking the condition is the only way
// to know. Looking for the digits in the sentence instead is a different
// question with a nearly-right answer: the moment in a quiet reason contains
// 0, 1, 2 and 2026, so a heartbeat of 0 — the reading somebody most wants to
// see under a rule called "stopped" — disappears from the line.
func suffix(a views.Alert) string {
	// What the condition's own sentence already said.
	namesMoment := a.Is == views.Quiet
	namesValue := a.Is == views.Above || a.Is == views.Below

	var also []string
	if a.LastSeen != "" && !namesMoment {
		also = append(also, "last "+a.LastSeen)
	}
	if a.Value != nil && !namesValue {
		v := fmt.Sprintf("%g", *a.Value)
		// The name first, because these are metric names rather than units:
		// "path_requests 1" reads as a measurement where "1 path_requests"
		// reads as a typo. The path listing puts the number first because what
		// follows it there is a unit — "1284 requests".
		if a.Metric != "" && !strings.Contains(a.Reason, a.Metric) {
			v = a.Metric + " " + v
		}
		also = append(also, v)
	}
	if len(also) == 0 {
		return ""
	}
	return "  (" + strings.Join(also, ", ") + ")"
}
