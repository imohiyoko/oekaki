package views

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

// The tables a graph can be written out as.
const (
	TableNodes = "nodes"
	TableEdges = "edges"
)

// Tables lists what can be exported, in a stable order.
func Tables() []string { return []string{TableNodes, TableEdges} }

// WriteCSV writes one table of a graph.
//
// A graph is the thing this program is about, and a spreadsheet is the thing
// most questions about one actually get answered in — how many of these are
// there, which of them has the most going into it, what changed since last
// month. Answering those by reading JSON is work nobody should have to do
// twice.
//
// The columns are fixed and the free-form attributes are not spread across
// them. A column per attribute would change shape whenever the input did,
// which is exactly what a spreadsheet cannot cope with; they go into one
// column as key=value instead, sorted so the same graph writes the same bytes.
func WriteCSV(w io.Writer, g *core.Graph, table string) error {
	out := csv.NewWriter(w)
	switch table {
	case TableNodes:
		if err := writeNodes(out, g); err != nil {
			return err
		}
	case TableEdges:
		if err := writeEdges(out, g); err != nil {
			return err
		}
	default:
		return fmt.Errorf("no table called %q; there is %s", table, strings.Join(Tables(), " and "))
	}
	out.Flush()
	return out.Error()
}

func writeNodes(out *csv.Writer, g *core.Graph) error {
	axes := axisIDs(g)
	head := []string{"id", "type", "name", "provider"}
	// Axis columns are prefixed because an axis may be called the same thing
	// as a field — "provider" is both — and two columns with one name is a
	// table that spreadsheets and dataframe libraries quietly mangle.
	for _, a := range axes {
		head = append(head, "in_"+a)
	}
	head = append(head, "edges_out", "edges_in", "claim_origin", "attrs")
	if err := out.Write(head); err != nil {
		return err
	}

	outward, inward := map[string]int{}, map[string]int{}
	for _, e := range g.Edges {
		outward[e.From]++
		inward[e.To]++
	}

	for _, n := range g.Nodes {
		row := []string{n.ID, n.Type, n.Name, n.Provider}
		for _, a := range axes {
			row = append(row, n.Groups[a])
		}
		row = append(row,
			strconv.Itoa(outward[n.ID]),
			strconv.Itoa(inward[n.ID]),
			claimOrigin(n.Claim),
			flatten(n.Attrs))
		if err := out.Write(row); err != nil {
			return err
		}
	}
	return nil
}

func writeEdges(out *csv.Writer, g *core.Graph) error {
	axes := axisIDs(g)
	head := []string{"from", "to", "kind", "relation"}
	for _, a := range axes {
		head = append(head, "from_"+a, "to_"+a, "crosses_"+a)
	}
	head = append(head, "suppressed", "claim_origin", "attrs")
	if err := out.Write(head); err != nil {
		return err
	}

	for _, e := range g.Edges {
		row := []string{e.From, e.To, string(e.Kind), e.Relation}
		for _, a := range axes {
			from, _ := groupOf(g, a, e.From)
			to, _ := groupOf(g, a, e.To)
			// Whether an edge leaves its container is the question these
			// tables get built to answer, so it is a column rather than
			// something the reader works out with a formula.
			crosses := ""
			if from != "" && to != "" {
				crosses = strconv.FormatBool(from != to)
			}
			row = append(row, from, to, crosses)
		}
		row = append(row, strconv.FormatBool(e.Suppressed), claimOrigin(e.Claim), flatten(e.Attrs))
		if err := out.Write(row); err != nil {
			return err
		}
	}
	return nil
}

func axisIDs(g *core.Graph) []string {
	out := make([]string, 0, len(g.Axes))
	for _, a := range g.Axes {
		out = append(out, a.ID)
	}
	return out
}

func claimOrigin(c *core.Claim) string {
	if c == nil {
		return ""
	}
	return string(c.Origin)
}

// flatten puts free-form attributes into one column, sorted, so that the same
// graph writes the same bytes every time.
func flatten(attrs map[string]any) string {
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, attrs[k]))
	}
	return strings.Join(parts, "; ")
}
