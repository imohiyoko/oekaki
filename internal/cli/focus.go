package cli

import (
	"flag"
	"fmt"
	"strings"

	sourceparser "github.com/imohiyoko/oekaki/parsers/source"
	"github.com/imohiyoko/oekaki/parsers/terraform"
	"github.com/imohiyoko/oekaki/views"
)

// runFocus draws one group with everything else folded away.
func runFocus(env Env, args []string) error {
	fs := flag.NewFlagSet("focus", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	output := fs.String("o", "", "write to this file instead of standard output")
	axis := fs.String("axis", "", "which axis the group is on; the graph's first if not said")
	group := fs.String("group", "", "the group to keep whole")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("focus takes one graph")
	}
	if *group == "" {
		return fmt.Errorf("focus needs --group; there is nothing to focus on otherwise")
	}

	g, err := loadGraph(env, fs.Arg(0), terraform.Options{}, sourceparser.Options{})
	if err != nil {
		return err
	}
	folded, err := views.Focus(g, *axis, *group)
	if err != nil {
		return err
	}

	inside, collapsed := 0, 0
	for _, n := range folded.Nodes {
		if n.Attrs["collapsed"] != nil {
			collapsed++
			continue
		}
		inside++
	}
	fmt.Fprintf(env.Stderr, "%d inside %s, %d group%s folded to one box each, %d lines\n",
		inside, *group, collapsed, plural(collapsed), len(folded.Edges))

	out, err := folded.MarshalIndent()
	if err != nil {
		return err
	}
	return write(env, *output, out)
}

// runExport writes a graph out as a table.
func runExport(env Env, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	output := fs.String("o", "", "write to this file instead of standard output")
	table := fs.String("table", views.TableNodes, "which table: "+strings.Join(views.Tables(), " or "))
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("export takes one graph")
	}

	g, err := loadGraph(env, fs.Arg(0), terraform.Options{}, sourceparser.Options{})
	if err != nil {
		return err
	}
	var b strings.Builder
	if err := views.WriteCSV(&b, g, *table); err != nil {
		return err
	}
	rows := len(g.Nodes)
	if *table == views.TableEdges {
		rows = len(g.Edges)
	}
	fmt.Fprintf(env.Stderr, "%d %s\n", rows, *table)
	return write(env, *output, []byte(b.String()))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
