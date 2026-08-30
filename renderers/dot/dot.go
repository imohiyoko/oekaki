// Package dot renders the IR as Graphviz DOT.
//
// DOT is the pivot format: the SVG renderer lays this out, and users who want
// a different engine or their own post-processing can take the DOT directly.
// Layout is left entirely to Graphviz. Writing a layout engine is how projects
// like this die.
package dot

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/internal/textmetrics"
	"github.com/imohiyoko/oekaki/renderers/style"
)

// fontName is a single family, not a CSS font stack, and that is the whole
// point.
//
// Graphviz's WebAssembly build has no font files, so it sizes every box from a
// built-in metric table looked up by this name. A comma-separated list matches
// no entry, so boxes were sized from a fallback table about 8.5% narrower than
// Helvetica — and the list was then copied verbatim into the SVG's
// `font-family`, whose trailing generic `sans-serif` a browser may resolve to
// DejaVu Sans, around a third wider again. Labels spilled out of boxes that
// were never sized for them.
//
// "Helvetica" fixes both halves at once: Graphviz can measure it, and every
// font a browser substitutes for it — Arial, Liberation Sans, Nimbus Sans — is
// a metric-compatible clone by design, so the advances the layout assumed are
// the advances the browser draws with. Do not turn this back into a list.
const fontName = "Helvetica"

// nodeMargin is wider horizontally than Graphviz's default to leave slack for
// what correct metrics still cannot cover: hinting, subpixel rounding, and a
// browser with no Helvetica-compatible face at all. The vertical component is
// Graphviz's own.
const nodeMargin = "0.28,0.10"

// Constants shared by the margin above and the width calculation below, so
// that changing one cannot silently stop matching the other.
const (
	nodeFontSize  = 10.0
	marginInches  = 0.28
	pointsPerInch = 72.0
	marginPoints  = marginInches * pointsPerInch
)

// Options tunes the output.
type Options struct {
	// Title is drawn at the top of the diagram. Empty means no title.
	Title string

	// RankDir is the Graphviz layout direction: "LR" (default) or "TB".
	RankDir string

	// Axis selects which grouping to nest by. Empty means the network axis,
	// or whichever axis the document has if it has no network axis.
	Axis string

	// Kinds restricts which edge kinds are drawn. Empty means all of them.
	Kinds []core.EdgeKind

	// Legend adds a key explaining the edge kinds. Worth it once a graph has
	// more than one kind in it.
	Legend bool
}

// Render writes the graph as DOT.
func Render(g *core.Graph, opts Options) (string, error) {
	if opts.RankDir == "" {
		opts.RankDir = "LR"
	}

	axis := g.AxisOrDefault(opts.Axis)
	if opts.Axis != "" && axis == "" {
		return "", fmt.Errorf("this graph has no axis %q; it has %s", opts.Axis, axisList(g))
	}

	var b strings.Builder
	b.WriteString("digraph oekaki {\n")
	b.WriteString("  graph [\n")
	fmt.Fprintf(&b, "    rankdir=%s,\n", quote(opts.RankDir))
	b.WriteString("    compound=true,\n")
	b.WriteString("    newrank=true,\n")
	// Curved splines, not ortho: Graphviz's orthogonal router mangles edges
	// that cross cluster boundaries, and every interesting edge here does.
	b.WriteString("    splines=spline,\n")
	b.WriteString("    nodesep=0.35,\n")
	b.WriteString("    ranksep=0.80,\n")
	fmt.Fprintf(&b, "    fontname=%q,\n", fontName)
	b.WriteString("    fontsize=11,\n")
	b.WriteString("    bgcolor=\"white\",\n")
	if opts.Title != "" {
		fmt.Fprintf(&b, "    label=%s,\n", quote(opts.Title))
		b.WriteString("    labelloc=\"t\",\n")
		b.WriteString("    fontsize=16,\n")
	}
	b.WriteString("  ];\n")
	fmt.Fprintf(&b, "  node [shape=box, style=\"filled,rounded\", fontname=%q, fontsize=10, penwidth=1.2, margin=%q];\n", fontName, nodeMargin)
	fmt.Fprintf(&b, "  edge [fontname=%q, fontsize=8, arrowsize=0.7, penwidth=1.1];\n\n", fontName)

	// Nodes are emitted inside the cluster they belong to, so the recursion
	// walks the group forest and then mops up whatever sits at top level.
	contested := contestedTargets(g)

	drawn := map[string]bool{}
	for _, id := range g.Children(axis, "") {
		if err := writeCluster(&b, g, axis, id, 1, drawn, contested); err != nil {
			return "", err
		}
	}
	for _, n := range g.NodesIn(axis, "") {
		writeNode(&b, n, 1, contested.entities[n.ID])
	}

	b.WriteString("\n")
	writeEdges(&b, g, axis, drawn, opts, contested)

	if opts.Legend {
		writeLegend(&b, g)
	}

	b.WriteString("}\n")
	return b.String(), nil
}

func axisList(g *core.Graph) string {
	if len(g.Axes) == 0 {
		return "no axes at all"
	}
	names := make([]string, 0, len(g.Axes))
	for _, a := range g.Axes {
		names = append(names, a.ID)
	}
	return strings.Join(names, ", ")
}

func writeCluster(b *strings.Builder, g *core.Graph, axis, groupID string, depth int, drawn map[string]bool, contested contestedTargetSet) error {
	grp, ok := g.Group(groupID)
	if !ok {
		return fmt.Errorf("group %q disappeared while rendering", groupID)
	}
	path, err := g.GroupPath(groupID)
	if err != nil {
		return err
	}
	drawn[groupID] = true

	pal := style.ForGroup(grp.Type)
	ind := indent(depth)

	fmt.Fprintf(b, "%ssubgraph %s {\n", ind, quote("cluster_"+groupID))
	fmt.Fprintf(b, "%s  label=%s;\n", ind, quote(clusterLabel(grp)))
	fmt.Fprintf(b, "%s  labeljust=\"l\";\n", ind)
	fmt.Fprintf(b, "%s  style=\"filled,rounded\";\n", ind)
	fmt.Fprintf(b, "%s  fillcolor=%s;\n", ind, quote(pal.Fill))
	fmt.Fprintf(b, "%s  color=%s;\n", ind, quote(pal.Stroke))
	fmt.Fprintf(b, "%s  fontcolor=%s;\n", ind, quote(pal.Text))
	fmt.Fprintf(b, "%s  fontsize=10;\n", ind)
	fmt.Fprintf(b, "%s  penwidth=1.4;\n", ind)
	fmt.Fprintf(b, "%s  margin=14;\n", ind)

	// Every cluster carries an invisible anchor. It keeps an otherwise empty
	// cluster on the page — Graphviz discards clusters with nothing in them,
	// which would silently erase an empty subnet — and it gives edges that
	// point at the container itself something to attach to, since Graphviz
	// cannot terminate an edge on a cluster boundary directly.
	fmt.Fprintf(b, "%s  %s [shape=point, style=invis, width=0.01, height=0.01, label=\"\"];\n",
		ind, quote(anchor(groupID)))

	for _, child := range g.Children(axis, groupID) {
		if err := writeCluster(b, g, axis, child, depth+1, drawn, contested); err != nil {
			return err
		}
	}
	for _, n := range g.NodesIn(axis, path) {
		writeNode(b, n, depth+1, contested.entities[n.ID])
	}

	fmt.Fprintf(b, "%s}\n", ind)
	return nil
}

func anchor(groupID string) string { return "anchor_" + groupID }

// contestedTargets is the set of things two claims disagree about.
//
// A disagreement is worth seeing even when the document had to display one
// side of it. Without this the graph would show the winning value and look
// exactly like a graph where nobody disagreed, which is the quiet lie the
// Conflicts array exists to prevent.
type contestedTargetSet struct {
	entities map[string]bool
	edges    map[string]bool
}

func contestedTargets(g *core.Graph) contestedTargetSet {
	out := contestedTargetSet{}
	for _, c := range g.Conflicts {
		switch c.TargetKind {
		case core.ConflictTargetEntity:
			if out.entities == nil {
				out.entities = make(map[string]bool)
			}
			out.entities[c.Target] = true
		case core.ConflictTargetEdge:
			if out.edges == nil {
				out.edges = make(map[string]bool)
			}
			out.edges[c.Target] = true
		}
	}
	return out
}

// writeNode draws one resource.
//
// Five channels carry five independent facts, allocated so that none of them
// collides and so that a graph with nothing asserted about it looks exactly as
// it did before any of this existed: fill is the category, stroke is the
// coverage state, the dash pattern is who claimed the node, the pen width says
// whether two claims disagree, and the label carries a word for the state so
// it survives being printed in one colour.
func writeNode(b *strings.Builder, n *core.Node, depth int, contested bool) {
	pal := style.Of(n.Type)

	stroke := pal.Stroke
	attrs := []string{
		fmt.Sprintf("label=%s", quote(nodeLabel(n))),
		fmt.Sprintf("fillcolor=%s", quote(pal.Fill)),
	}

	var styles []string
	if n.Coverage != nil {
		cv := style.ForCoverage(string(n.Coverage.State))
		if cv.Stroke != "" {
			stroke = cv.Stroke
		}
		if cv.Dashes == "dashed" {
			styles = append(styles, "dashed")
		}
		if cv.PenWidth > 0 {
			attrs = append(attrs, fmt.Sprintf("penwidth=%.1f", cv.PenWidth))
		}
	}
	if n.Claim != nil && style.ForOrigin(string(n.Claim.Origin)).Dashed {
		styles = append(styles, "dashed")
	}
	if contested {
		attrs = append(attrs, fmt.Sprintf("penwidth=%.1f", style.Contested))
	}

	attrs = append(attrs,
		fmt.Sprintf("color=%s", quote(stroke)),
		fmt.Sprintf("fontcolor=%s", quote(pal.Text)),
		fmt.Sprintf("width=%.2f", minWidthInches(nodeLabel(n))),
	)
	if len(styles) > 0 {
		attrs = append(attrs, fmt.Sprintf("style=%s", quote("filled,rounded,"+strings.Join(dedupeStyles(styles), ","))))
	}

	fmt.Fprintf(b, "%s%s [%s%s];\n",
		indent(depth), quote(n.ID), strings.Join(attrs, ", "), tooltip(n, contested))
}

// minWidthInches is how wide a box has to be for its label to survive a font
// substitution.
//
// Correct metrics were not enough on their own. Graphviz sizes a box as the
// label plus a fixed margin, but the risk it has to absorb is *multiplicative*
// — a substituted face is a percentage wider, not a fixed number of points
// wider — so the margin covers a short label and runs out on a long one. On a
// real estate, where a name like
// "s3_bucket_server_side_encryption_configuration" is ordinary, that left
// nineteen labels in eighty-nine still able to spill.
//
// So the minimum is stated outright. Graphviz takes the larger of this and the
// size it worked out itself, which means short labels keep exactly the
// proportions they had and only the long ones are widened.
//
// This stays a DOT attribute rather than a correction applied to the SVG
// afterwards, so `-f svg` and `-f dot` remain the same path.
func minWidthInches(label string) float64 {
	var widest float64
	for line := range strings.SplitSeq(label, "\n") {
		if w := textmetrics.FitWidth(line, nodeFontSize); w > widest {
			widest = w
		}
	}
	// The same horizontal padding a short label gets, so a widened box does
	// not look like a different kind of box.
	return (widest + 2*marginPoints) / pointsPerInch
}

func dedupeStyles(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// endpoint resolves an edge end to the DOT node it attaches to, plus any
// compound attribute needed. An end that names a container attaches to that
// cluster's anchor and carries lhead/ltail so the arrow stops at the border.
func endpoint(g *core.Graph, id string, drawn map[string]bool, head bool) (string, string, bool) {
	if _, ok := g.Node(id); ok {
		return id, "", true
	}
	if _, ok := g.Group(id); ok {
		// A container that is not drawn on this axis has no cluster to point
		// at, so the edge cannot be expressed here.
		if !drawn[id] {
			return "", "", false
		}
		attr := "ltail"
		if head {
			attr = "lhead"
		}
		return anchor(id), fmt.Sprintf("%s=%s", attr, quote("cluster_"+id)), true
	}
	return "", "", false
}

func writeEdges(b *strings.Builder, g *core.Graph, axis string, drawn map[string]bool, opts Options, contested contestedTargetSet) {
	want := map[core.EdgeKind]bool{}
	for _, k := range opts.Kinds {
		want[k] = true
	}

	for _, e := range g.Edges {
		if len(want) > 0 && !want[e.Kind] {
			continue
		}

		from, ltail, okFrom := endpoint(g, e.From, drawn, false)
		to, lhead, okTo := endpoint(g, e.To, drawn, true)
		if !okFrom || !okTo {
			continue
		}

		st := style.ForEdge(string(e.Kind))
		if e.Suppressed {
			// Drawn, but faintly. A reader who cannot see the edge cannot
			// judge the claim that it is not real; --hide-suppressed is for
			// when they have judged it already.
			st = style.Suppressed
		}

		attrs := []string{fmt.Sprintf("color=%s", quote(st.Color))}
		switch st.Dashes {
		case "dashed":
			attrs = append(attrs, "style=dashed")
		case "dotted":
			attrs = append(attrs, "style=dotted")
		case "bold":
			attrs = append(attrs, "style=bold", "penwidth=2.0")
		}
		// A hollow arrowhead says somebody asserted this rather than a parser
		// finding it, without taking a colour channel that is already carrying
		// the edge kind.
		if e.Claim != nil {
			if head := style.ForOrigin(string(e.Claim.Origin)).ArrowHead; head != "" && head != "normal" {
				attrs = append(attrs, "arrowhead="+head)
			}
		}
		if contested.edges[core.EdgeKey(e.From, e.To, e.Kind, e.Relation)] {
			attrs = append(attrs, fmt.Sprintf("penwidth=%.1f", style.Contested))
		}
		if ltail != "" {
			attrs = append(attrs, ltail)
		}
		if lhead != "" {
			attrs = append(attrs, lhead)
		}
		if tip := edgeTooltip(e, contested.edges[core.EdgeKey(e.From, e.To, e.Kind, e.Relation)]); tip != "" {
			attrs = append(attrs, fmt.Sprintf("tooltip=%s", quote(tip)))
		}

		fmt.Fprintf(b, "  %s -> %s [%s];\n", quote(from), quote(to), strings.Join(attrs, ", "))
	}
}

func hasCoverage(g *core.Graph) bool {
	for _, n := range g.Nodes {
		if n.Coverage != nil {
			return true
		}
	}
	return false
}

// writeLegend draws a key as a small disconnected cluster. Only the kinds
// actually present are listed, so a graph does not advertise edge kinds the
// user has no data for.
func writeLegend(b *strings.Builder, g *core.Graph) {
	present := map[core.EdgeKind]bool{}
	for _, e := range g.Edges {
		present[e.Kind] = true
	}
	var kinds []core.EdgeKind
	for k := range present {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	if len(kinds) == 0 && !hasCoverage(g) {
		return
	}

	b.WriteString("\n  subgraph \"cluster_legend\" {\n")
	b.WriteString("    label=\"legend\";\n")
	b.WriteString("    labeljust=\"l\";\n")
	b.WriteString("    style=\"filled,rounded\";\n")
	b.WriteString("    fillcolor=\"#fbfbfc\";\n")
	b.WriteString("    color=\"#d0d5da\";\n")
	b.WriteString("    fontcolor=\"#4a5763\";\n")
	b.WriteString("    fontsize=9;\n")
	b.WriteString("    margin=10;\n")

	for i, k := range kinds {
		st := style.ForEdge(string(k))
		from := fmt.Sprintf("legend_%d_a", i)
		to := fmt.Sprintf("legend_%d_b", i)
		fmt.Fprintf(b, "    %s [shape=point, width=0.02, color=%s];\n", quote(from), quote(st.Color))
		fmt.Fprintf(b, "    %s [shape=plaintext, style=\"\", label=%s, fontsize=9, fontcolor=\"#4a5763\"];\n", quote(to), quote(st.Label))

		attrs := fmt.Sprintf("color=%s", quote(st.Color))
		switch st.Dashes {
		case "dashed":
			attrs += ", style=dashed"
		case "bold":
			attrs += ", style=bold, penwidth=2.0"
		}
		fmt.Fprintf(b, "    %s -> %s [%s];\n", quote(from), quote(to), attrs)
	}

	writeCoverageLegend(b, g, len(kinds))
	b.WriteString("  }\n")
}

// writeCoverageLegend explains only the states this graph actually contains.
//
// Same discipline as the edge kinds above: a key that lists a state with no
// examples on the page invites the reader to go looking for something that is
// not there, and makes a graph carrying no coverage at all look as though it
// were withholding something.
func writeCoverageLegend(b *strings.Builder, g *core.Graph, offset int) {
	present := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Coverage != nil {
			present[string(n.Coverage.State)] = true
		}
	}
	if len(present) == 0 {
		return
	}

	i := offset
	for _, state := range style.CoverageStates() {
		if !present[state] {
			continue
		}
		cv := style.ForCoverage(state)

		swatch := fmt.Sprintf("legend_%d_a", i)
		text := fmt.Sprintf("legend_%d_b", i)
		i++

		stroke := cv.Stroke
		if stroke == "" {
			stroke = "#8a9099"
		}
		boxStyle := "filled,rounded"
		if cv.Dashes == "dashed" {
			boxStyle += ",dashed"
		}
		fmt.Fprintf(b, "    %s [shape=box, style=%s, label=\"\", width=0.18, height=0.12, fillcolor=\"#ffffff\", color=%s];\n",
			quote(swatch), quote(boxStyle), quote(stroke))
		fmt.Fprintf(b, "    %s [shape=plaintext, style=\"\", label=%s, fontsize=9, fontcolor=\"#4a5763\"];\n",
			quote(text), quote(cv.Label))
		fmt.Fprintf(b, "    %s -> %s [style=invis];\n", quote(swatch), quote(text))
	}
}

func edgeTooltip(e core.Edge, contested bool) string {
	var parts []string
	if e.Suppressed {
		parts = append(parts, "asserted not to exist")
	}
	if label, ok := e.Attrs["attribute"].(string); ok && label != "" {
		parts = append(parts, label)
	}
	if e.Claim != nil {
		parts = append(parts, claimLine(e.Claim))
	}
	if contested {
		parts = append(parts, "claims about this disagree")
	}
	return strings.Join(parts, "\n")
}

func clusterLabel(g *core.Group) string {
	if g.Label == "" {
		return g.Type
	}
	return g.Type + ": " + g.Label
}

// nodeLabel is the name over the short type, with the coverage state appended
// as a word.
//
// The word matters more than it looks. Colour alone would lose the state to a
// monochrome print, to a projector, and to a reader with a colour deficiency —
// and a coverage map is exactly the kind of picture that gets pasted into an
// incident channel and screenshotted.
func nodeLabel(n *core.Node) string {
	short := style.ShortType(n.Type)
	if badge := coverageBadge(n); badge != "" {
		short += " · " + badge
	}
	if n.Name == "" || n.Name == style.ShortType(n.Type) {
		return short
	}
	return n.Name + "\n" + short
}

func coverageBadge(n *core.Node) string {
	if n.Coverage == nil {
		return ""
	}
	return style.ForCoverage(string(n.Coverage.State)).Badge
}

// tooltip puts the resource address and source location where a mouse can find
// them, which is what makes an SVG in a browser more useful than a PNG.
func tooltip(n *core.Node, contested bool) string {
	parts := []string{n.ID}
	if n.Coverage != nil && n.Coverage.Reason != "" {
		parts = append(parts, "logs: "+string(n.Coverage.State)+" — "+n.Coverage.Reason)
	}
	if n.Claim != nil {
		parts = append(parts, claimLine(n.Claim))
	}
	if contested {
		parts = append(parts, "claims about this disagree")
	}
	if n.Source != nil {
		if n.Source.Line > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", n.Source.File, n.Source.Line))
		} else {
			parts = append(parts, n.Source.File)
		}
	}
	if len(n.Attrs) > 0 {
		keys := make([]string, 0, len(n.Attrs))
		for k := range n.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v, ok := scalar(n.Attrs[k]); ok {
				parts = append(parts, fmt.Sprintf("%s = %s", k, v))
			}
		}
	}
	parts = append(parts, coverageEvidence(n)...)
	return fmt.Sprintf(", tooltip=%s", quote(strings.Join(parts, "\n")))
}

// claimLine says who asserted something, and how sure they were. The
// confidence is shown only when it was stated: an unstated confidence is not
// a confidence of zero.
func claimLine(c *core.Claim) string {
	who := "asserted"
	if c.Author != "" {
		who += " by " + c.Author
	}
	who += " (" + string(c.Origin)
	if c.Confidence != nil {
		who += fmt.Sprintf(", confidence %.2g", *c.Confidence)
	}
	who += ")"
	if c.Note != "" {
		who += ": " + c.Note
	}
	return who
}

// coverageEvidence lists what a state rests on, so that a reader who doubts a
// finding can see the basis for it without leaving the picture.
func coverageEvidence(n *core.Node) []string {
	if n.Coverage == nil {
		return nil
	}
	var out []string
	for _, e := range n.Coverage.Evidence {
		line := "  " + e.Kind
		if e.Sink != "" {
			line += " -> " + e.Sink
		}
		if e.Records != nil {
			line += fmt.Sprintf(" (%.0f records)", *e.Records)
		}
		if e.Via != "" {
			line += ", via " + e.Via
		}
		out = append(out, line)
	}
	return out
}

// scalar formats an attribute for a tooltip, skipping anything structured:
// a serialized ingress rule set in a tooltip helps nobody.
func scalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return fmt.Sprintf("%t", t), true
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t)), true
		}
		return fmt.Sprintf("%g", t), true
	}
	return "", false
}

func indent(depth int) string { return strings.Repeat("  ", depth) }

// quote produces a DOT double-quoted string. Graphviz reads \n inside one as a
// line break, so literal newlines in labels are converted rather than escaped.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
