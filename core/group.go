package core

import "strings"

// LowestCommonAncestor returns the deepest group path that contains every path
// given. It is how a resource that spans several subnets gets placed: an ECS
// service in two availability zones belongs to neither subnet, so it is drawn
// in the VPC that holds both.
//
// An empty result means the paths share no container at all.
func LowestCommonAncestor(paths []string) string {
	var segments [][]string
	for _, p := range paths {
		if p == "" {
			// A top-level member forces the whole set to top level.
			return ""
		}
		segments = append(segments, strings.Split(p, GroupSeparator))
	}
	if len(segments) == 0 {
		return ""
	}

	common := segments[0]
	for _, s := range segments[1:] {
		n := 0
		for n < len(common) && n < len(s) && common[n] == s[n] {
			n++
		}
		common = common[:n]
		if n == 0 {
			return ""
		}
	}
	return strings.Join(common, GroupSeparator)
}

// AssignGroupPaths fills in each node's path on one axis from a map of node id
// to the group ids that node sits in, collapsing multi-container membership to
// the lowest common ancestor. Nodes absent from the map are left at that axis's
// top level.
//
// Group ids naming a group on a different axis are ignored: membership is a
// per-axis question, and mixing axes would produce a path that resolves to
// nothing.
func (g *Graph) AssignGroupPaths(axis string, membership map[string][]string) error {
	pathOf := make(map[string]string, len(g.Groups))
	for _, grp := range g.Groups {
		if grp.Axis != axis {
			continue
		}
		p, err := g.GroupPath(grp.ID)
		if err != nil {
			return err
		}
		pathOf[grp.ID] = p
	}

	for i := range g.Nodes {
		ids := membership[g.Nodes[i].ID]
		if len(ids) == 0 {
			continue
		}
		paths := make([]string, 0, len(ids))
		for _, id := range ids {
			p, ok := pathOf[id]
			if !ok {
				continue
			}
			paths = append(paths, p)
		}
		if len(paths) == 0 {
			continue
		}
		g.Nodes[i].SetGroup(axis, LowestCommonAncestor(paths))
	}
	return nil
}

// SetGroup records a node's path on an axis. An empty path means top level,
// which is the absence of an entry rather than an entry holding "".
func (n *Node) SetGroup(axis, path string) {
	if path == "" {
		delete(n.Groups, axis)
		return
	}
	if n.Groups == nil {
		n.Groups = map[string]string{}
	}
	n.Groups[axis] = path
}

// GroupOn returns a node's path on an axis, or "" for top level.
func (n *Node) GroupOn(axis string) string { return n.Groups[axis] }
