package views

import (
	"fmt"
	"sort"
	"strings"

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

	// A node records the whole path down to it, not the id of the group it
	// sits in, so a vpc holding subnets holding instances gives the instances
	// "vpc/subnet". Comparing that to "vpc" matches nothing, and focusing on
	// any container that has containers of its own would fold its own contents
	// away as though they belonged to somebody else.
	path, err := g.GroupPath(group)
	if err != nil {
		return nil, err
	}
	inside := map[string]bool{}
	for _, n := range g.Nodes {
		if within(n.Groups[axis], path) {
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
	// Folding on the endpoints alone would keep whichever edge the input
	// happened to list first and throw the rest away, so a pair joined by both
	// a declared reference and an observed one would come out as one of the
	// two, chosen by file order. The kind and the relation are part of what
	// makes two lines the same line.
	type fold struct{ from, to, kind, relation string }
	seen := map[fold]bool{}
	var edges []core.Edge
	for _, e := range g.Edges {
		from, to := e.From, e.To
		fromIn, toIn := inside[from], inside[to]
		if !fromIn && !toIn {
			continue
		}
		if !fromIn {
			owner, ok := outermost(g, axis, from)
			if !ok {
				continue
			}
			from = standIn(owner)
		}
		if !toIn {
			owner, ok := outermost(g, axis, to)
			if !ok {
				continue
			}
			to = standIn(owner)
		}
		key := fold{from, to, string(e.Kind), e.Relation}
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

// within reports whether a node's group path is at or below the focused one.
func within(got, path string) bool {
	return got == path || strings.HasPrefix(got, path+core.GroupSeparator)
}

// outermost is the top container a node sits under on this axis.
//
// Everything outside the focus becomes one box, and a box per subnet of
// somebody else's network is not one box — it is the tangle this view exists
// to fold away. The outermost container is the coarsest honest answer to
// "where does this live", which is all a drawing that is not about it needs.
func outermost(g *core.Graph, axis, id string) (string, bool) {
	got, ok := groupOf(g, axis, id)
	if !ok {
		return "", false
	}
	if cut := strings.Index(got, core.GroupSeparator); cut >= 0 {
		got = got[:cut]
	}
	return got, true
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
