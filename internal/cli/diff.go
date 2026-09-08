package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	sourceparser "github.com/imohiyoko/oekaki/parsers/source"
	"github.com/imohiyoko/oekaki/parsers/terraform"
	"github.com/imohiyoko/oekaki/views"
)

// runDiff says what is different between two graphs.
func runDiff(env Env, args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	output := fs.String("o", "", "write to this file instead of standard output")
	format := fs.String("f", "table", "output format: table or json")
	only := fs.String("only", "", "compare one kind of thing: "+strings.Join(views.OfKinds(), ", "))
	changed := fs.Bool("exit-code", false, "exit 1 when anything changed, for a pipeline that should stop")
	fs.Usage = func() {
		fmt.Fprintf(env.Stderr, "Usage: oekaki diff <before> <after> [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return fmt.Errorf("diff takes two graphs: the one before and the one after")
	}
	if *format != "table" && *format != "json" {
		return fmt.Errorf("unknown format %q: want table or json", *format)
	}
	if *only != "" && !views.ValidOf(*only) {
		return fmt.Errorf("unknown kind %q: want one of %s", *only, strings.Join(views.OfKinds(), ", "))
	}

	before, err := loadGraph(env, fs.Arg(0), terraform.Options{}, sourceparser.Options{})
	if err != nil {
		return err
	}
	after, err := loadGraph(env, fs.Arg(1), terraform.Options{}, sourceparser.Options{})
	if err != nil {
		return err
	}

	changes, err := views.Diff(before, after)
	if err != nil {
		return err
	}
	if *only != "" {
		kept := changes[:0]
		for _, c := range changes {
			if c.What == *only {
				kept = append(kept, c)
			}
		}
		changes = kept
	}

	var out []byte
	if *format == "json" {
		out, err = json.MarshalIndent(struct {
			Changes []views.Change `json:"changes"`
		}{changes}, "", "  ")
		if err != nil {
			return err
		}
		out = append(out, '\n')
	} else {
		var b strings.Builder
		for _, c := range changes {
			fmt.Fprintf(&b, "%-8s %-6s %s\n", c.Kind, c.What, orID(c.Label, c.Subject))
			for _, f := range c.Fields {
				// Both sides on one line, because the question a reader has is
				// not "what is it now" but "what did it used to be".
				fmt.Fprintf(&b, "         %s: %s → %s\n", f.Field, orNothing(f.From), orNothing(f.To))
			}
		}
		out = []byte(b.String())
	}

	counted := map[string]int{}
	for _, c := range changes {
		counted[c.Kind]++
	}
	if len(changes) == 0 {
		fmt.Fprintln(env.Stderr, "nothing changed")
	} else {
		var parts []string
		for _, kind := range views.ChangeKinds() {
			if counted[kind] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counted[kind], kind))
			}
		}
		fmt.Fprintf(env.Stderr, "%d change%s: %s\n", len(changes), plural(len(changes)), strings.Join(parts, ", "))
	}
	if err := write(env, *output, out); err != nil {
		return err
	}
	// A pipeline that should stop asks for it. The default is to report and
	// carry on, because a diff is also something people read while nothing is
	// wrong.
	if *changed && len(changes) > 0 {
		return errChanged
	}
	return nil
}

// errChanged is not a mistake anybody made, so it says nothing about how to fix
// itself. It exists to move the exit code.
var errChanged = fmt.Errorf("the graph changed")

// orNothing is what to print where a field was absent. An empty column would
// read as a value somebody set to the empty string.
func orNothing(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

// orID prints the subject beside its label, when the subject says something
// the label does not.
//
// A node's id is what somebody greps for, and the label is what they read; both
// belong on the line. An edge's key is the label re-encoded — the same two ends
// and the same relation, in base64 — so printing both says one thing twice, and
// the encoded half is the unreadable one.
func orID(label, id string) string {
	if label == "" || label == id {
		return id
	}
	if strings.HasPrefix(id, "edge:") {
		return label
	}
	return label + "  " + id
}
