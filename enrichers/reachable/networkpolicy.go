package reachable

import (
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

// NetworkPolicy is the Kubernetes security group, and it is expanded here for
// the same reason security group rules are: the parser writes down what the
// object says, and what the network then permits is a derivation.
//
// Two properties shape the derivation, and both cut against drawing more.
//
// The default is allow. A pod nothing selects accepts everything, so drawing
// "what can reach it" would draw every other pod, and a diagram that says
// everything reaches everything has said nothing. Only paths where at least
// one end is actually restricted are drawn; the rest are left out, and the
// `restricts` edges the parser wrote are what tell a reader which ends those
// are. Absence of a reachable edge is not a claim that a path is blocked.
//
// The other is that enforcement belongs to the CNI, which no manifest
// mentions. On a cluster whose plugin ignores NetworkPolicy, every policy here
// is inert. "This policy permits A" survives that; "only A gets through" does
// not, and only the first is drawn.

const (
	ingress = "Ingress"
	egress  = "Egress"
)

// policySet is what the parser's edges say, indexed for the question this
// package asks: for a workload and a direction, is it restricted, and by whom
// is it then allowed.
type policySet struct {
	isolated map[string]map[string]bool   // workload -> direction -> restricted
	allowed  map[string]map[string]bool   // workload -> direction -> peer -> allowed
	ports    map[string]string            // workload+direction+peer -> ports
	policies map[string]map[string]string // workload+direction+peer -> policy id
	partial  []string                     // policies whose reach could not be fully read
}

func applyNetworkPolicies(g *core.Graph, seen map[string]bool, r *enrichers.Report) {
	set := readPolicies(g)

	// Reported before the early return. A graph holding only policies that
	// could not be read has nothing to draw and everything to warn about, and
	// warning below the guard would drop exactly that case.
	for _, id := range set.partial {
		r.Unmatched = append(r.Unmatched, enrichers.Unmatched{
			Selector: map[string]string{"policy": id},
			Assert:   "reachable",
			Reason:   "the policy has peers this input could not resolve, so what it permits is wider than what is drawn",
			Action:   "dropped",
		})
	}
	if len(set.isolated) == 0 {
		return
	}

	// A path is drawn when every restricted end permits it, and at least one
	// end is restricted. Walking the allowances rather than every pair keeps
	// this linear in what the policies actually say.
	for target, directions := range set.isolated {
		if directions[ingress] {
			for peer := range set.allowed[key(target, ingress)] {
				if set.permits(peer, egress, target) {
					add(g, seen, peer, target, set.attrs(target, ingress, peer), r)
				}
			}
		}
		if directions[egress] {
			for peer := range set.allowed[key(target, egress)] {
				if set.permits(peer, ingress, target) {
					add(g, seen, target, peer, set.attrs(target, egress, peer), r)
				}
			}
		}
	}

}

// permits reports whether an end allows the path. An end that is not
// restricted in that direction permits everything, which is the Kubernetes
// default and the reason this returns true rather than false when nothing is
// known.
func (s *policySet) permits(node, direction, peer string) bool {
	if !s.isolated[node][direction] {
		return true
	}
	return s.allowed[key(node, direction)][peer]
}

func (s *policySet) attrs(target, direction, peer string) map[string]any {
	attrs := map[string]any{"via": "NetworkPolicy"}
	if p := s.ports[key(target, direction)+"\x00"+peer]; p != "" && p != "all ports" {
		attrs["ports"] = p
	}
	if id := s.policies[key(target, direction)][peer]; id != "" {
		attrs["policy"] = id
	}
	return attrs
}

// readPolicies turns the parser's restricts and allows edges back into the
// question this package asks. Nothing is read from the manifests again: the IR
// is the whole input, which is what lets a graph committed to a repository be
// re-derived without the manifests that produced it.
func readPolicies(g *core.Graph) *policySet {
	s := &policySet{
		isolated: map[string]map[string]bool{},
		allowed:  map[string]map[string]bool{},
		ports:    map[string]string{},
		policies: map[string]map[string]string{},
	}

	restricts := map[string][]string{} // policy -> workloads
	for _, e := range g.Edges {
		if e.Kind != core.EdgeIACRef || !isPolicy(g, e.From) {
			continue
		}
		switch e.Relation {
		case "restricts":
			restricts[e.From] = append(restricts[e.From], e.To)
			for _, d := range directions(e.Attrs) {
				if s.isolated[e.To] == nil {
					s.isolated[e.To] = map[string]bool{}
				}
				s.isolated[e.To][d] = true
			}
		}
	}
	for _, e := range g.Edges {
		if e.Kind != core.EdgeIACRef || !isPolicy(g, e.From) {
			continue
		}
		d := allowDirection(e.Relation)
		if d == "" {
			continue
		}
		{
			for _, target := range restricts[e.From] {
				k := key(target, d)
				if s.allowed[k] == nil {
					s.allowed[k] = map[string]bool{}
					s.policies[k] = map[string]string{}
				}
				s.allowed[k][e.To] = true
				s.policies[k][e.To] = e.From
				if p, ok := e.Attrs["ports"].(string); ok {
					s.ports[k+"\x00"+e.To] = p
				}
			}
		}
	}

	for _, n := range g.Nodes {
		if n.Type != "networkpolicy" {
			continue
		}
		for attr := range n.Attrs {
			if strings.HasSuffix(attr, "_unresolved") {
				s.partial = append(s.partial, n.ID)
				break
			}
		}
	}
	sort.Strings(s.partial)
	return s
}

func isPolicy(g *core.Graph, id string) bool {
	n, ok := g.Node(id)
	return ok && n.Type == "networkpolicy"
}

// directions reads the direction a restricts edge applies to. One edge can
// name both.
func directions(attrs map[string]any) []string {
	d, _ := attrs["direction"].(string)
	if d == "" {
		return nil
	}
	return strings.Split(d, ",")
}

// allowDirection reads the direction out of an allows relation. It lives in
// the relation rather than an attribute because Normalize merges edges that
// agree on from, to, kind and relation without reading attributes, so a peer
// allowed both ways would arrive here as one edge naming one direction.
func allowDirection(relation string) string {
	switch relation {
	case "allows-ingress":
		return ingress
	case "allows-egress":
		return egress
	}
	return ""
}

func key(node, direction string) string { return node + "\x00" + direction }
