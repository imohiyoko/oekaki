// Package views projects one evidence graph into a focused diagram.
//
// A view is deliberately a graph transformation, not a renderer feature. The
// same graph can therefore be shown as architecture, network, ER, workflow,
// or a request path by CLI, HTML, and future API clients alike.
package views

import (
	"fmt"
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

const (
	Architecture      = "architecture"
	Network           = "network"
	ER                = "er"
	Workflow          = "workflow"
	RequestPath       = "request-path"
	SecurityExposure  = "security-exposure"
	CodeDependency    = "code-dependency"
	ServiceDependency = "service-dependency"
	Reachability      = "reachability"
)

// Options controls a projection. Root is used by request-path; Depth limits
// traversal and prevents an accidental whole-estate expansion.
type Options struct {
	Name  string
	Root  string
	File  string
	Depth int
}

func Valid(name string) bool {
	switch name {
	case "", Architecture, Network, ER, Workflow, RequestPath, SecurityExposure, CodeDependency, ServiceDependency, Reachability:
		return true
	default:
		return false
	}
}

// Apply returns a normalized copy. The input is never mutated.
func Apply(in *core.Graph, opts Options) (*core.Graph, error) {
	name := opts.Name
	if (name == "" || name == Architecture || name == Network) && opts.File == "" {
		return clone(in)
	}
	if !Valid(name) {
		return nil, fmt.Errorf("unknown view %q: want architecture, network, er, workflow, request-path, security-exposure, code-dependency, service-dependency or reachability", name)
	}
	if opts.File != "" && (name == RequestPath || name == Reachability) {
		return nil, fmt.Errorf("view %q cannot be combined with --file; use --root and --depth to focus traversal views", name)
	}

	keep := map[string]bool{}
	for _, n := range in.Nodes {
		if includeNode(name, n) {
			keep[n.ID] = true
		}
	}
	for _, e := range in.Edges {
		if relationMatches(name, e) {
			keep[e.From], keep[e.To] = true, true
		}
	}
	if name == RequestPath {
		var err error
		keep, err = pathNodes(in, opts.Root, opts.Depth)
		if err != nil {
			return nil, err
		}
	}
	if name == Reachability {
		var err error
		keep, err = impactNodes(in, opts.Root, opts.Depth)
		if err != nil {
			return nil, err
		}
	}
	if opts.File != "" {
		fileKeep := map[string]bool{}
		for _, n := range in.Nodes {
			if sourceMatches(n.Source, opts.File) {
				fileKeep[n.ID] = true
			}
		}
		for _, group := range in.Groups {
			if sourceMatches(group.Source, opts.File) {
				fileKeep[group.ID] = true
			}
		}
		if len(fileKeep) == 0 {
			return nil, fmt.Errorf("no graph entities were declared in file %q", opts.File)
		}
		direct := make(map[string]bool, len(fileKeep))
		for id := range fileKeep {
			direct[id] = true
		}
		for _, e := range in.Edges {
			if direct[e.From] || direct[e.To] {
				fileKeep[e.From], fileKeep[e.To] = true, true
			}
		}
		// File focus is authoritative for the entity set. The selected view still
		// decides which relationships between those entities are drawn, but it
		// must not pull in every other entity of the same broad type.
		keep = fileKeep
	}

	out, err := clone(in)
	if err != nil {
		return nil, err
	}
	out.Nodes = out.Nodes[:0]
	for _, n := range in.Nodes {
		if keep[n.ID] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	out.Edges = out.Edges[:0]
	for _, e := range in.Edges {
		if keep[e.From] && keep[e.To] && (name == RequestPath || relationMatches(name, e)) {
			out.Edges = append(out.Edges, e)
		}
	}
	// Groups are retained only when they still contain a selected node.
	usedGroups := map[string]bool{}
	for _, g := range in.Groups {
		if keep[g.ID] {
			usedGroups[g.ID] = true
		}
	}
	for _, n := range out.Nodes {
		for _, p := range n.Groups {
			for _, id := range strings.Split(p, core.GroupSeparator) {
				if id != "" {
					usedGroups[id] = true
				}
			}
		}
	}
	groupsByID := make(map[string]core.Group, len(in.Groups))
	for _, group := range in.Groups {
		groupsByID[group.ID] = group
	}
	// A selected child group is not a standalone hierarchy. Retain its complete
	// ancestor chain even when those containers were declared in another file.
	for id := range usedGroups {
		for current := id; current != ""; {
			group, ok := groupsByID[current]
			if !ok || group.Parent == nil {
				break
			}
			parent := *group.Parent
			if usedGroups[parent] {
				break
			}
			usedGroups[parent] = true
			current = parent
		}
	}
	out.Groups = out.Groups[:0]
	for _, g := range in.Groups {
		if usedGroups[g.ID] {
			out.Groups = append(out.Groups, g)
		}
	}
	out.Observations = out.Observations[:0]
	for _, observation := range in.Observations {
		if keep[observation.Subject] {
			out.Observations = append(out.Observations, observation)
		}
	}
	out.LogRecords = out.LogRecords[:0]
	for _, record := range in.LogRecords {
		if keep[record.Source] {
			out.LogRecords = append(out.LogRecords, record)
		}
	}
	filterConflicts(out)
	out.Normalize()
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}

func sourceMatches(source *core.Source, file string) bool {
	return source != nil && (source.File == file || strings.HasSuffix(source.File, "/"+file))
}

// filterConflicts keeps only disagreements whose target survived the
// projection. A focused view is a standalone graph document, so a conflict on
// an omitted node or edge would be a dangling reference rather than useful
// provenance.
func filterConflicts(g *core.Graph) {
	filtered := g.Conflicts[:0]
	for _, conflict := range g.Conflicts {
		if g.HasConflictTarget(conflict.Target, conflict.TargetKind) {
			filtered = append(filtered, conflict)
		}
	}
	g.Conflicts = filtered
}

func includeNode(view string, n core.Node) bool {
	t := strings.ToLower(n.Type + " " + n.Name)
	switch view {
	case ER:
		return strings.Contains(t, "db") || strings.Contains(t, "database") || strings.Contains(t, "table") || strings.Contains(t, "schema") || strings.Contains(t, "sql")
	case Workflow:
		return true
	case SecurityExposure:
		return strings.Contains(t, "external") || strings.Contains(t, "security") || strings.Contains(t, "firewall") || strings.Contains(t, "service")
	case CodeDependency:
		return strings.Contains(t, "code") || strings.Contains(t, "function") || strings.Contains(t, "package") || strings.Contains(t, "file")
	case ServiceDependency:
		return strings.Contains(t, "service") || strings.Contains(t, "api") || strings.Contains(t, "function") || strings.Contains(t, "container")
	default:
		return true
	}
}

func relationMatches(view string, e core.Edge) bool {
	r := strings.ToLower(e.Relation)
	if r == "" {
		if x, ok := e.Attrs["relation"].(string); ok {
			r = strings.ToLower(x)
		}
	}
	switch view {
	case ER:
		return containsAny(r, "read", "write", "foreign", "query", "persist", "db")
	case SecurityExposure:
		return containsAny(r, "expose", "allow", "public", "route") || e.Kind == core.EdgeReachable
	case CodeDependency:
		return containsAny(r, "import", "contain", "call", "invoke", "depend")
	case ServiceDependency:
		return containsAny(r, "call", "invoke", "route", "serve", "publish", "subscribe") || e.Kind == core.EdgeObserved
	case Workflow, RequestPath:
		return containsAny(r, "call", "invoke", "trigger", "publish", "subscribe", "depends", "route", "read", "write") || r == "" || e.Kind == core.EdgeObserved
	case Reachability:
		return reachabilityEdge(e, r)
	default:
		return true
	}
}

func reachabilityEdge(e core.Edge, _ string) bool {
	return e.Kind == core.EdgeReachable || e.Kind == core.EdgeObserved
}

func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func pathNodes(g *core.Graph, root string, depth int) (map[string]bool, error) {
	if root == "" {
		return nil, fmt.Errorf("request-path view requires --root")
	}
	known := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	if !known[root] {
		return nil, fmt.Errorf("request-path root %q was not found", root)
	}
	if depth <= 0 {
		depth = 4
	}
	seen := map[string]bool{root: true}
	frontier := []string{root}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		next := []string{}
		for _, id := range frontier {
			for _, e := range g.Edges {
				if !relationMatches(Workflow, e) {
					continue
				}
				var other string
				if e.From == id {
					other = e.To
				} else if e.To == id {
					other = e.From
				} else {
					continue
				}
				if known[other] && !seen[other] {
					seen[other] = true
					next = append(next, other)
				}
			}
		}
		frontier = next
	}
	return seen, nil
}

func impactNodes(g *core.Graph, root string, depth int) (map[string]bool, error) {
	if root == "" {
		return nil, fmt.Errorf("reachability view requires --root")
	}
	known := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	if !known[root] {
		return nil, fmt.Errorf("reachability root %q was not found", root)
	}
	if depth <= 0 {
		depth = 4
	}
	seen := map[string]bool{root: true}
	frontier := []string{root}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		next := []string{}
		for _, id := range frontier {
			for _, e := range g.Edges {
				r := strings.ToLower(e.Relation)
				if r == "" {
					if x, ok := e.Attrs["relation"].(string); ok {
						r = strings.ToLower(x)
					}
				}
				if !reachabilityEdge(e, r) {
					continue
				}
				if e.From != id {
					continue
				}
				if known[e.To] && !seen[e.To] {
					seen[e.To] = true
					next = append(next, e.To)
				}
			}
		}
		frontier = next
	}
	return seen, nil
}

func clone(in *core.Graph) (*core.Graph, error) {
	b, err := in.MarshalIndent()
	if err != nil {
		return nil, err
	}
	out, err := core.Decode(strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	return out, nil
}
