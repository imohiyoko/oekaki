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

// workloadTypes are what a policy peer can turn out to be. A rule with no
// peers allows every source, and every source means these — not only the
// internet node the parser had to point the edge at.
var workloadTypes = map[string]bool{
	"pod": true, "deployment": true, "statefulset": true, "daemonset": true,
	"replicaset": true, "job": true, "cronjob": true,
}

// ports is what a rule opens. Absent ports in a rule means every port, which
// is a different thing from an empty set and has to survive intersection.
type ports struct {
	all bool
	set map[string]bool
}

func allPorts() *ports { return &ports{all: true} }

// parsePorts reads what the parser wrote. Rules are joined with a comma when
// two of them name the same peer, and one rule's own ports with a space.
func parsePorts(text string) *ports {
	if text == "" || text == "all ports" {
		return allPorts()
	}
	p := &ports{set: map[string]bool{}}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' }) {
		if part = strings.TrimSpace(part); part != "" {
			p.set[part] = true
		}
	}
	if len(p.set) == 0 {
		return allPorts()
	}
	return p
}

// union is what two policies allow together. Policies are additive: a second
// policy naming the same peer widens what that peer may use, never narrows it.
func (p *ports) union(q *ports) *ports {
	if p == nil {
		return q
	}
	if p.all || q.all {
		return allPorts()
	}
	out := &ports{set: map[string]bool{}}
	for k := range p.set {
		out.set[k] = true
	}
	for k := range q.set {
		out.set[k] = true
	}
	return out
}

// intersect is what both ends allow. Traffic needs a port both agree on, and
// two ends that name disjoint ports have no path between them however
// enthusiastically each names the other.
func (p *ports) intersect(q *ports) (*ports, bool) {
	switch {
	case p.all && q.all:
		return allPorts(), true
	case p.all:
		return q, len(q.set) > 0
	case q.all:
		return p, len(p.set) > 0
	}
	out := &ports{set: map[string]bool{}}
	for k := range p.set {
		if q.set[k] {
			out.set[k] = true
		}
	}
	return out, len(out.set) > 0
}

func (p *ports) String() string {
	if p.all {
		return ""
	}
	out := make([]string, 0, len(p.set))
	for k := range p.set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// policySet is what the parser's edges say, indexed for the question this
// package asks: for a workload and a direction, is it restricted, and on which
// ports is which peer then allowed.
type policySet struct {
	isolated map[string]map[string]bool   // workload -> direction -> restricted
	allowed  map[string]map[string]*ports // workload+direction -> peer -> ports
	any      map[string]*ports            // workload+direction -> ports open to every peer
	policies map[string]map[string]string // workload+direction -> peer -> policy id
	partial  []string
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
	everything := workloads(g)

	for _, target := range sortedKeys(set.isolated) {
		if set.isolated[target][ingress] {
			for _, peer := range set.peers(target, ingress, everything) {
				set.draw(g, seen, r, peer, target, ingress)
			}
		}
		if set.isolated[target][egress] {
			for _, peer := range set.peers(target, egress, everything) {
				set.draw(g, seen, r, target, peer, egress)
			}
		}
	}
}

// draw emits one path if both ends permit it on a port they agree on. The
// restricted end is the one whose direction is given; the other end is checked
// in the opposite direction, where it may be unrestricted and permit anything.
func (s *policySet) draw(g *core.Graph, seen map[string]bool, r *enrichers.Report, from, to, direction string) {
	if from == to {
		return
	}
	target, peer, opposite := to, from, egress
	if direction == egress {
		target, peer, opposite = from, to, ingress
	}

	mine, ok := s.permits(target, direction, peer)
	if !ok {
		return
	}
	theirs, ok := s.permits(peer, opposite, target)
	if !ok {
		return
	}
	common, shared := mine.intersect(theirs)
	if !shared {
		// Each end names the other and they share no port, so nothing gets
		// through. Drawing the path on the strength of the names alone is how
		// a diagram reports a connection that cannot happen.
		return
	}

	attrs := map[string]any{"via": "NetworkPolicy"}
	if p := common.String(); p != "" {
		attrs["ports"] = p
	}
	if id := s.policies[key(target, direction)][peer]; id != "" {
		attrs["policy"] = id
	}
	add(g, seen, from, to, attrs, r)
}

// permits reports the ports an end allows for a peer, and whether it allows it
// at all. An end that is not restricted in that direction permits everything,
// which is the Kubernetes default.
func (s *policySet) permits(node, direction, peer string) (*ports, bool) {
	if !s.isolated[node][direction] {
		return allPorts(), true
	}
	k := key(node, direction)
	named := s.allowed[k][peer]
	open := s.any[k]
	switch {
	case named != nil && open != nil:
		return named.union(open), true
	case named != nil:
		return named, true
	case open != nil:
		return open, true
	}
	return nil, false
}

// peers is who to consider for a restricted end. A rule with no peers allows
// every source, so it expands to the workloads in the graph rather than only
// to the internet node the parser had to point its edge at.
func (s *policySet) peers(node, direction string, everything []string) []string {
	k := key(node, direction)
	out := sortedKeys(s.allowed[k])
	if s.any[k] == nil {
		return out
	}
	have := map[string]bool{}
	for _, p := range out {
		have[p] = true
	}
	for _, w := range everything {
		if !have[w] {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

// workloads are the nodes a policy peer can turn out to be, plus the internet,
// which an open ipBlock and a peerless rule both reach.
func workloads(g *core.Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		if workloadTypes[n.Type] || n.ID == "external:internet" {
			out = append(out, n.ID)
		}
	}
	sort.Strings(out)
	return out
}

// readPolicies turns the parser's restricts and allows edges back into the
// question this package asks. Nothing is read from the manifests again: the IR
// is the whole input, which is what lets a graph committed to a repository be
// re-derived without the manifests that produced it.
func readPolicies(g *core.Graph) *policySet {
	s := &policySet{
		isolated: map[string]map[string]bool{},
		allowed:  map[string]map[string]*ports{},
		any:      map[string]*ports{},
		policies: map[string]map[string]string{},
	}

	restricts := map[string][]string{}
	for _, e := range g.Edges {
		if e.Kind != core.EdgeIACRef || e.Relation != "restricts" || !isPolicy(g, e.From) {
			continue
		}
		restricts[e.From] = append(restricts[e.From], e.To)
		for _, d := range directions(e.Attrs) {
			if s.isolated[e.To] == nil {
				s.isolated[e.To] = map[string]bool{}
			}
			s.isolated[e.To][d] = true
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
		text, _ := e.Attrs["ports"].(string)
		open := parsePorts(text)
		anyPeer, _ := e.Attrs["peer"].(string)
		for _, target := range restricts[e.From] {
			k := key(target, d)
			if anyPeer != "" {
				s.any[k] = s.any[k].union(open)
				continue
			}
			if s.allowed[k] == nil {
				s.allowed[k] = map[string]*ports{}
				s.policies[k] = map[string]string{}
			}
			s.allowed[k][e.To] = s.allowed[k][e.To].union(open)
			s.policies[k][e.To] = e.From
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

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
