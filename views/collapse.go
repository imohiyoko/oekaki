package views

import (
	"fmt"
	"sort"

	"github.com/imohiyoko/oekaki/core"
)

// Collapse folds a whole graph up onto one axis: every group becomes one box,
// and the references between two groups become one line carrying how many
// there were.
//
// This is the drawing you make when the fine-grained one has stopped being
// readable. The question it answers is not "what talks to what" but "which of
// these depends on which, and how much" — and the second half of that is why
// the count travels on the line. Ten references and one look identical
// otherwise, and they are not the same risk when somebody proposes deleting
// the thing at the far end.
//
// References inside a group do not become a line from a box to itself, which
// says nothing and draws badly. They become a number on the box, because
// "this one is mostly self-contained" is worth being able to see.
//
// least drops lines below a weight. A big estate has a long tail of single
// references that are real and are not what anybody is looking at, and there
// is no threshold that is right for everyone — so it is a number the caller
// picks rather than one written in here.
func Collapse(g *core.Graph, axis string, least int) (*core.Graph, error) {
	axis = g.AxisOrDefault(axis)
	if !g.HasAxis(axis) {
		return nil, fmt.Errorf("this graph has no %s axis", axis)
	}

	// How many references run between each ordered pair, and how many stay
	// inside one group.
	between := map[[2]string]int{}
	inside := map[string]int{}
	examples := map[[2]string][]string{}
	for _, e := range g.Edges {
		from, okFrom := groupOf(g, axis, e.From)
		to, okTo := groupOf(g, axis, e.To)
		// An edge with an end that belongs to no group cannot be placed on
		// this axis at all. Guessing where it goes would invent a dependency.
		if !okFrom || !okTo {
			continue
		}
		if from == to {
			inside[from]++
			continue
		}
		key := [2]string{from, to}
		between[key]++
		if len(examples[key]) < 3 {
			examples[key] = append(examples[key], e.From+" -> "+e.To)
		}
	}

	members := map[string]int{}
	for _, n := range g.Nodes {
		if got, ok := n.Groups[axis]; ok && got != "" {
			members[got]++
		}
	}

	kept := map[string]bool{}
	pairs := make([][2]string, 0, len(between))
	for key, n := range between {
		if n < least {
			continue
		}
		pairs = append(pairs, key)
		kept[key[0]] = true
		kept[key[1]] = true
	}
	sort.Slice(pairs, func(i, j int) bool {
		// Heaviest first, then by name, so that the same graph folds to the
		// same bytes and the interesting lines are at the top of the file.
		if between[pairs[i]] != between[pairs[j]] {
			return between[pairs[i]] > between[pairs[j]]
		}
		if pairs[i][0] != pairs[j][0] {
			return pairs[i][0] < pairs[j][0]
		}
		return pairs[i][1] < pairs[j][1]
	})
	for id, n := range inside {
		if n > 0 {
			kept[id] = true
		}
	}

	out := core.New()
	out.Metadata = g.Metadata
	for _, a := range g.Axes {
		if a.ID == axis {
			out.Axes = append(out.Axes, a)
		}
	}

	ids := make([]string, 0, len(kept))
	for id := range kept {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		label, kind := id, "group"
		if src, ok := g.Group(id); ok {
			if src.Label != "" {
				label = src.Label
			}
			if src.Type != "" {
				kind = src.Type
			}
		}
		n := core.Node{ID: id, Type: kind, Name: label, Attrs: map[string]any{
			"members":            members[id],
			"internal_reference": inside[id],
		}}
		out.Nodes = append(out.Nodes, n)
	}

	for _, key := range pairs {
		e := core.Edge{From: key[0], To: key[1], Kind: core.EdgeIACRef, Attrs: map[string]any{
			"references": between[key],
		}}
		if len(examples[key]) > 0 {
			e.Attrs["examples"] = examples[key]
		}
		out.Edges = append(out.Edges, e)
	}

	out.Normalize()
	return out, out.Validate()
}
