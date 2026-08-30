// Package mermaid renders the IR as a Mermaid flowchart.
//
// This is the renderer for diagrams that live in a repository. GitHub, GitLab
// and most static site generators render Mermaid from a fenced code block, so a
// CI job can regenerate the diagram on every merge and the result shows up as a
// reviewable text diff rather than a binary blob nobody can read.
package mermaid

import (
	"fmt"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/renderers/style"
)

// Options tunes the output.
type Options struct {
	// Direction is the flowchart direction: "LR" (default) or "TB".
	Direction string

	// Axis selects which grouping to nest by. Empty means the network axis.
	Axis string

	// Kinds restricts which edge kinds are drawn. Empty means all of them.
	Kinds []core.EdgeKind

	// Fenced wraps the output in a ```mermaid code fence, ready to paste into
	// a Markdown file.
	Fenced bool
}

// Render writes the graph as a Mermaid flowchart.
func Render(g *core.Graph, opts Options) (string, error) {
	if opts.Direction == "" {
		opts.Direction = "LR"
	}

	axis := g.AxisOrDefault(opts.Axis)
	if opts.Axis != "" && axis == "" {
		return "", fmt.Errorf("this graph has no axis %q", opts.Axis)
	}

	// Mermaid identifiers cannot contain the dots and brackets a Terraform
	// address is made of. Nodes and groups therefore get separate short
	// synthetic ids, assigned in sorted order to keep the output stable and to
	// avoid collisions such as "team-a" and "team_a".
	ids := make(map[string]string, len(g.Nodes))
	ordered := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		ordered = append(ordered, n.ID)
	}
	sort.Strings(ordered)
	for i, id := range ordered {
		ids[id] = fmt.Sprintf("n%d", i)
	}
	groupIDs := make(map[string]string, len(g.Groups))
	orderedGroups := make([]string, 0, len(g.Groups))
	for _, group := range g.Groups {
		orderedGroups = append(orderedGroups, group.ID)
	}
	sort.Strings(orderedGroups)
	for i, id := range orderedGroups {
		groupIDs[id] = fmt.Sprintf("g%d", i)
	}

	var b strings.Builder
	if opts.Fenced {
		b.WriteString("```mermaid\n")
	}
	fmt.Fprintf(&b, "flowchart %s\n", opts.Direction)

	drawn := map[string]bool{}
	for _, gid := range g.Children(axis, "") {
		if err := writeSubgraph(&b, g, axis, gid, ids, groupIDs, 1, drawn); err != nil {
			return "", err
		}
	}
	for _, n := range g.NodesIn(axis, "") {
		writeNode(&b, n, ids, 1)
	}

	links := writeEdges(&b, g, ids, groupIDs, drawn, opts)
	writeClasses(&b, g, ids)
	writeLinkStyles(&b, links)

	if opts.Fenced {
		b.WriteString("```\n")
	}
	return b.String(), nil
}

func writeSubgraph(b *strings.Builder, g *core.Graph, axis, groupID string, ids, groupIDs map[string]string, depth int, drawn map[string]bool) error {
	grp, ok := g.Group(groupID)
	if !ok {
		return fmt.Errorf("group %q disappeared while rendering", groupID)
	}
	path, err := g.GroupPath(groupID)
	if err != nil {
		return err
	}
	drawn[groupID] = true

	label := grp.Type
	if grp.Label != "" {
		label = grp.Type + ": " + grp.Label
	}

	ind := indent(depth)
	fmt.Fprintf(b, "%ssubgraph %s[%q]\n", ind, groupIDs[groupID], escape(label))
	fmt.Fprintf(b, "%s  direction LR\n", ind)

	for _, child := range g.Children(axis, groupID) {
		if err := writeSubgraph(b, g, axis, child, ids, groupIDs, depth+1, drawn); err != nil {
			return err
		}
	}
	for _, n := range g.NodesIn(axis, path) {
		writeNode(b, n, ids, depth+1)
	}

	fmt.Fprintf(b, "%send\n", ind)
	return nil
}

func writeNode(b *strings.Builder, n *core.Node, ids map[string]string, depth int) {
	// Only <br/> is used for the second line. Mermaid runs labels through a
	// sanitizer that strips most other tags, so anything fancier silently
	// disappears in some renderers and not others.
	second := style.ShortType(n.Type)
	if n.Coverage != nil {
		if badge := style.ForCoverage(string(n.Coverage.State)).Badge; badge != "" {
			second += " · " + badge
		}
	}
	label := escape(n.Name) + "<br/>" + escape(second)
	fmt.Fprintf(b, "%s%s[%q]\n", indent(depth), ids[n.ID], label)
}

// link records an emitted edge so its colour can be set afterwards. Mermaid
// styles links by their index in emission order, so the order has to be kept.
type link struct {
	index int
	kind  core.EdgeKind
}

// endpoint resolves an edge end to a Mermaid identifier. Mermaid lets an edge
// terminate on a subgraph, so a reference to a container is drawable here.
func endpoint(g *core.Graph, id string, ids, groupIDs map[string]string, drawn map[string]bool) (string, bool) {
	if mid, ok := ids[id]; ok {
		return mid, true
	}
	if drawn[id] {
		return groupIDs[id], true
	}
	return "", false
}

func writeEdges(b *strings.Builder, g *core.Graph, ids, groupIDs map[string]string, drawn map[string]bool, opts Options) []link {
	want := map[core.EdgeKind]bool{}
	for _, k := range opts.Kinds {
		want[k] = true
	}

	var links []link
	for _, e := range g.Edges {
		if len(want) > 0 && !want[e.Kind] {
			continue
		}
		from, ok1 := endpoint(g, e.From, ids, groupIDs, drawn)
		to, ok2 := endpoint(g, e.To, ids, groupIDs, drawn)
		if !ok1 || !ok2 {
			continue
		}

		arrow := "-->"
		switch e.Kind {
		case core.EdgeReachable:
			arrow = "-.->"
		case core.EdgeObserved:
			arrow = "==>"
		}
		fmt.Fprintf(b, "  %s %s %s\n", from, arrow, to)
		links = append(links, link{index: len(links), kind: e.Kind})
	}
	return links
}

// writeClasses colours nodes by category. One classDef per category that is
// actually used keeps the output short.
func writeClasses(b *strings.Builder, g *core.Graph, ids map[string]string) {
	members := map[style.Category][]string{}
	for _, n := range g.Nodes {
		c := style.CategoryOf(n.Type)
		members[c] = append(members[c], ids[n.ID])
	}

	cats := make([]string, 0, len(members))
	for c := range members {
		cats = append(cats, string(c))
	}
	sort.Strings(cats)

	for _, name := range cats {
		c := style.Category(name)
		p := style.ForCategory(c)
		fmt.Fprintf(b, "  classDef %s fill:%s,stroke:%s,color:%s,stroke-width:1px\n", name, p.Fill, p.Stroke, p.Text)

		list := members[c]
		sort.Strings(list)
		fmt.Fprintf(b, "  class %s %s\n", strings.Join(list, ","), name)
	}

	writeCoverageClasses(b, g, ids)
}

// writeCoverageClasses applies the coverage state as a second class.
//
// Mermaid applies classes in order and the two do not overlap: the category
// class sets fill, and this one sets stroke and dash. So a node keeps saying
// what it is while also saying whether it is logging, which is the same split
// the DOT renderer makes with its border.
func writeCoverageClasses(b *strings.Builder, g *core.Graph, ids map[string]string) {
	members := map[string][]string{}
	for _, n := range g.Nodes {
		if n.Coverage == nil {
			continue
		}
		state := string(n.Coverage.State)
		members[state] = append(members[state], ids[n.ID])
	}

	for _, state := range style.CoverageStates() {
		list := members[state]
		if len(list) == 0 {
			continue
		}
		cv := style.ForCoverage(state)
		if cv.Stroke == "" {
			// The healthy state gets no class at all, so a map where
			// everything is fine reads as a map where nothing is flagged.
			continue
		}

		def := fmt.Sprintf("  classDef logs_%s stroke:%s,stroke-width:2px", state, cv.Stroke)
		if cv.Dashes == "dashed" {
			def += ",stroke-dasharray: 4 3"
		}
		fmt.Fprintln(b, def)

		sort.Strings(list)
		fmt.Fprintf(b, "  class %s logs_%s\n", strings.Join(list, ","), state)
	}
}

func writeLinkStyles(b *strings.Builder, links []link) {
	byKind := map[core.EdgeKind][]string{}
	for _, l := range links {
		byKind[l.kind] = append(byKind[l.kind], fmt.Sprintf("%d", l.index))
	}

	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)

	for _, name := range kinds {
		st := style.ForEdge(name)
		fmt.Fprintf(b, "  linkStyle %s stroke:%s,stroke-width:1.5px\n",
			strings.Join(byKind[core.EdgeKind(name)], ","), st.Color)
	}
}

func indent(depth int) string { return strings.Repeat("  ", depth) }

// escape neutralises the characters that would end a Mermaid label early.
func escape(s string) string {
	r := strings.NewReplacer(
		`"`, "#quot;",
		"\n", "<br/>",
	)
	return r.Replace(s)
}
