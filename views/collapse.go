package views

import (
	"fmt"
	"sort"

	"github.com/imohiyoko/oekaki/core"
)

// Collapse folds a whole graph up onto one axis: a group becomes one box, and
// the references between two groups become one line carrying how many there
// were.
//
// least is a threshold on how many references something stands for, and it
// means the same thing for a box as for a line: a group is drawn when it is an
// end of a surviving line, or when it holds at least that many references of
// its own. At zero that is every group there is, so nothing is hidden by
// asking for no filtering. Raised, it asks for the busy part of the estate and
// leaves out what nothing reaches — which is the point, and does mean the
// threshold changes which boxes exist and not only which lines are drawn.
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
	// The kind is part of the key. Folding it away would report a reachability
	// finding or an observation as a declared reference, which is the one
	// distinction this whole program is built to keep.
	type pair struct {
		from, to string
		kind     core.EdgeKind
	}
	between := map[pair]int{}
	// Inside references are counted per kind too. Counting them together and
	// then comparing the total against a threshold that lines are held to per
	// kind makes the two mean different things: a group joined to itself by
	// one declared and one observed reference would outrank a pair of groups
	// joined by exactly the same two, and survive as a box with no lines while
	// the pair vanishes. That is the drawing least exists to prevent.
	inside := map[pair]int{}
	examples := map[pair][]string{}
	for _, e := range g.Edges {
		from, okFrom := groupOf(g, axis, e.From)
		to, okTo := groupOf(g, axis, e.To)
		// An edge with an end that belongs to no group cannot be placed on
		// this axis at all. Guessing where it goes would invent a dependency.
		if !okFrom || !okTo {
			continue
		}
		if from == to {
			inside[pair{from, from, e.Kind}]++
			continue
		}
		key := pair{from, to, e.Kind}
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
	// A container somebody declared and put nothing in is still a container
	// they declared — an empty subnet is a normal thing for a parser to find.
	// Leaving it out would make "no filtering draws every group" untrue for
	// exactly the groups whose emptiness is the interesting part.
	for _, group := range g.Groups {
		if group.Axis != axis {
			continue
		}
		path, err := g.GroupPath(group.ID)
		if err != nil {
			continue
		}
		if _, known := members[path]; !known {
			members[path] = 0
		}
	}

	// The most any one kind of reference stays inside a group. Compared
	// against least the same way a line's own count is.
	busiest := map[string]int{}
	for key, n := range inside {
		if n > busiest[key.from] {
			busiest[key.from] = n
		}
	}

	kept := map[string]bool{}
	pairs := make([]pair, 0, len(between))
	for key, n := range between {
		if n < least {
			continue
		}
		pairs = append(pairs, key)
		kept[key.from] = true
		kept[key.to] = true
	}
	sort.Slice(pairs, func(i, j int) bool {
		// Heaviest first, then by name, so that the same graph folds to the
		// same bytes and the interesting lines are at the top of the file.
		if between[pairs[i]] != between[pairs[j]] {
			return between[pairs[i]] > between[pairs[j]]
		}
		if pairs[i].from != pairs[j].from {
			return pairs[i].from < pairs[j].from
		}
		if pairs[i].to != pairs[j].to {
			return pairs[i].to < pairs[j].to
		}
		return pairs[i].kind < pairs[j].kind
	})
	// least is a threshold on how many references something stands for, and it
	// has to mean the same thing for a group as it does for a line. Keeping
	// every group that has any reference of its own, however high the threshold
	// was set, fills a drawing asked to show only the busy part with boxes that
	// nothing reaches — which is the picture the threshold was raised to
	// escape. At zero it still means "draw what exists", so a group nothing
	// touches is drawn when nothing was filtered.
	for id := range members {
		if busiest[id] >= least {
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
			"internal_reference": busiest[id],
		}}
		out.Nodes = append(out.Nodes, n)
	}

	for _, key := range pairs {
		e := core.Edge{From: key.from, To: key.to, Kind: key.kind, Attrs: map[string]any{
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
