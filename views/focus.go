package views

import (
	"fmt"
	"sort"

	"github.com/imohiyoko/oekaki/core"
)

// Focus keeps one group's members and collapses every other group to a single
// box.
//
// A graph of a whole estate is unreadable at the size a person can look at,
// and cutting it down to one group loses the thing you usually came to find
// out: what else touches this. So neither. Everything inside the chosen group
// stays as it is, everything outside becomes one box per group, and the edges
// between them survive as a count.
//
// The collapsed box is not a claim that the other group is one thing. It says
// only that this drawing is not about it, which is why the number of
// references it stands for is on it: somebody reading it can see how much has
// been folded away and go and look if it matters.
//
// Nothing here is about any particular axis. It works on accounts because an
// account is a group, and it works the same way on a namespace or a virtual
// network.
func Focus(g *core.Graph, axis, group string) (*core.Graph, error) {
	if group == "" {
		return nil, fmt.Errorf("focus needs a group to focus on")
	}
	axis = g.AxisOrDefault(axis)

	// A group that is not there produces a drawing of nothing, which looks
	// exactly like a group with nothing in it. Say which it is.
	if _, ok := g.Group(group); !ok {
		return nil, fmt.Errorf("no group %q on the %s axis", group, axis)
	}

	inside := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Groups[axis] == group {
			inside[n.ID] = true
		}
	}

	out := core.New()
	out.Metadata = g.Metadata
	for _, a := range g.Axes {
		if a.ID == axis {
			out.Axes = append(out.Axes, a)
		}
	}
	if len(out.Axes) == 0 {
		out.Axes = append(out.Axes, core.Axis{ID: axis, Label: axis})
	}

	// The one group that stays a container.
	if src, ok := g.Group(group); ok {
		kept := *src
		kept.Parent = nil
		out.Groups = append(out.Groups, kept)
	}
	for _, n := range g.Nodes {
		if !inside[n.ID] {
			continue
		}
		kept := n
		kept.Groups = map[string]string{axis: group}
		out.Nodes = append(out.Nodes, kept)
	}

	// Every other group becomes one node, created only if an edge reaches it.
	stood := map[string]*core.Node{}
	refs := map[string]int{}
	standIn := func(id string) string {
		if n, ok := stood[id]; ok {
			refs[n.ID]++
			return n.ID
		}
		label, kind := id, "group"
		if src, ok := g.Group(id); ok {
			if src.Label != "" {
				label = src.Label
			}
			if src.Type != "" {
				kind = src.Type
			}
		}
		n := &core.Node{ID: id, Type: kind, Name: label,
			Attrs: map[string]any{"collapsed": "this drawing is not about what is inside"}}
		stood[id] = n
		refs[id] = 1
		return id
	}

	// Edges are folded by the pair they connect, so many references between
	// the same two boxes become one arrow. The count of what was folded goes
	// on the collapsed box, because losing it would make a heavily-used
	// neighbour look like a passing one.
	seen := map[[2]string]bool{}
	var edges []core.Edge
	for _, e := range g.Edges {
		from, to := e.From, e.To
		fromIn, toIn := inside[from], inside[to]
		if !fromIn && !toIn {
			continue
		}
		if !fromIn {
			owner, ok := groupOf(g, axis, from)
			if !ok || owner == group {
				continue
			}
			from = standIn(owner)
		}
		if !toIn {
			owner, ok := groupOf(g, axis, to)
			if !ok || owner == group {
				continue
			}
			to = standIn(owner)
		}
		key := [2]string{from, to}
		if seen[key] {
			continue
		}
		seen[key] = true
		kept := e
		kept.From, kept.To = from, to
		edges = append(edges, kept)
	}

	ids := make([]string, 0, len(stood))
	for id := range stood {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := stood[id]
		n.Attrs["references"] = refs[id]
		out.Nodes = append(out.Nodes, *n)
	}
	out.Edges = edges

	out.Normalize()
	return out, out.Validate()
}

// groupOf is which group a node belongs to on this axis, if any.
func groupOf(g *core.Graph, axis, id string) (string, bool) {
	n, ok := g.Node(id)
	if !ok {
		return "", false
	}
	got, ok := n.Groups[axis]
	return got, ok && got != ""
}
