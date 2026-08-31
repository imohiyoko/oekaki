package kubernetes

import (
	"fmt"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

// A NetworkPolicy is the Kubernetes security group, and it is read the same
// way one is: what the object says, and nothing about what the network then
// does. Whether a path is permitted is derived later, by an enricher, from
// the edges written here.
//
// Two things make that division necessary rather than tidy.
//
// The default is allow. A namespace with no policy lets every pod reach every
// other, so "what can reach this" is the complete graph until somebody
// restricts it, and the interesting fact lives in the edges that are missing.
// Missing edges cannot be drawn, so what is drawn here is the restriction and
// the exception, never the permission.
//
// And a policy is enforced by the CNI, which no manifest mentions. On a
// cluster whose plugin does not implement NetworkPolicy the object is accepted
// and changes nothing. "This policy allows A" stays true there; "only A can
// reach B" does not. Only the first is written down.

// external is the peer every open ipBlock collapses to. The reachable
// enricher and the exposure enricher already use this id for the same idea,
// and the node has to be built the way they build it: two definitions of one
// id are a conflict when graphs are combined, and the difference would only
// surface for somebody drawing a Kubernetes estate beside a cloud one.
const external = "external:internet"

// allowRelation puts the direction in the relation rather than in an
// attribute. Normalize merges edges that share from, to, kind and relation
// without looking at attributes, so a policy allowing one peer both ways
// would otherwise become a single edge and lose a direction on the way out.
func allowRelation(direction string) string {
	return "allows-" + strings.ToLower(direction)
}

// restricts reads one NetworkPolicy: which workloads it isolates, and which
// peers it then lets through.
func (b *builder) restricts(np *object) {
	directions := policyTypes(np)

	sel, everything, why := labelSelector(np.body, "spec", "podSelector")
	if why != "" {
		// The name has to end in _unresolved: that suffix is what tells the
		// reachable enricher this policy reaches further than the edges say.
		// A policy whose own selector could not be read reaches further than
		// any of them.
		b.setAttr(np.id(), "restricts_unresolved", "the pod selector is "+why)
		return
	}
	targets := b.workloadsMatching(np.namespace, sel, everything)
	if len(targets) == 0 {
		b.setAttr(np.id(), "restricts", "nothing in this input carries "+describeSelector(sel, everything))
	}
	for _, t := range targets {
		b.edge(np.id(), t, "restricts", map[string]any{"direction": strings.Join(directions, ",")})
	}

	for _, direction := range directions {
		section, peers := "ingress", "from"
		if direction == "Egress" {
			section, peers = "egress", "to"
		}
		rules := seq(np.body, "spec", section)
		if len(rules) == 0 {
			// An absent or empty section under a declared policy type is a
			// closed door, not an unwritten one. It needs no edge: a policy
			// that restricts and allows nothing is exactly the absence of
			// allows edges beneath a restricts edge.
			b.setAttr(np.id(), strings.ToLower(direction)+"_allows", "nothing")
			continue
		}
		for _, rule := range rules {
			ports := describePorts(rule)
			for _, peer := range seq(rule, peers) {
				b.allow(np, peer, direction, ports)
			}
			if len(seq(rule, peers)) == 0 {
				// A rule with ports but no peers allows every source on those
				// ports. Saying so needs a peer, and the only honest one is
				// everything.
				b.edge(np.id(), b.internet(), allowRelation(direction),
					map[string]any{"ports": ports, "peer": "any source"})
			}
		}
	}
}

// allow resolves one peer of one rule. A peer this cannot evaluate is recorded
// on the policy rather than dropped: a rule whose reach is unknown must not
// read as a rule that reaches nothing.
func (b *builder) allow(np *object, peer any, direction, ports string) {
	if cidr := str(peer, "ipBlock", "cidr"); cidr != "" {
		attrs := map[string]any{"ports": ports, "cidr": cidr}
		if except := seq(peer, "ipBlock", "except"); len(except) > 0 {
			attrs["except"] = fmt.Sprint(except)
		}
		b.edge(np.id(), b.cidrNode(cidr), allowRelation(direction), attrs)
		return
	}

	pods, podsAll, podsWhy := labelSelector(peer, "podSelector")
	spaces, spacesAll, spacesWhy := labelSelector(peer, "namespaceSelector")
	hasPods, hasSpaces := dig(peer, "podSelector") != nil, dig(peer, "namespaceSelector") != nil
	if why := firstReason(podsWhy, spacesWhy); why != "" {
		b.unresolved(np, direction, "a peer selector is "+why)
		return
	}

	// Within one peer the two selectors are an AND: pods matching the pod
	// selector, in namespaces matching the namespace selector. Reading them as
	// alternatives is the classic way to widen a policy by accident.
	namespaces := []string{np.namespace}
	if hasSpaces {
		namespaces = b.namespacesMatching(spaces, spacesAll)
		if len(namespaces) == 0 {
			b.unresolved(np, direction,
				"a namespace selector matched nothing: namespace labels are not in this input")
			return
		}
	}
	matched := 0
	for _, ns := range namespaces {
		for _, id := range b.workloadsMatching(ns, pods, podsAll || !hasPods) {
			matched++
			b.edge(np.id(), id, allowRelation(direction), map[string]any{"ports": ports})
		}
	}
	if matched == 0 {
		b.unresolved(np, direction, "a peer matched no workload in this input")
	}
}

// unresolved records a rule this input cannot evaluate. The enricher must not
// read the resulting allows edges as the whole of what a policy permits.
func (b *builder) unresolved(np *object, direction, why string) {
	key := strings.ToLower(direction) + "_unresolved"
	if n, ok := b.g.Node(np.id()); ok {
		if n.Attrs == nil {
			n.Attrs = map[string]any{}
		}
		existing, _ := n.Attrs[key].(string)
		if existing == "" {
			n.Attrs[key] = why
		} else if !strings.Contains(existing, why) {
			n.Attrs[key] = existing + "; " + why
		}
	}
}

// policyTypes reports which directions a policy isolates. Omitted, it is
// inferred the way Kubernetes infers it: ingress always, egress only when the
// object has an egress section.
func policyTypes(np *object) []string {
	var declared []string
	for _, t := range seq(np.body, "spec", "policyTypes") {
		if s, ok := t.(string); ok {
			declared = append(declared, s)
		}
	}
	if len(declared) > 0 {
		return declared
	}
	if dig(np.body, "spec", "egress") != nil {
		return []string{"Ingress", "Egress"}
	}
	return []string{"Ingress"}
}

// labelSelector reads a LabelSelector. It reports the matchLabels, whether the
// selector is empty and therefore selects everything, and whether it could be
// read at all.
//
// A selector carrying matchExpressions cannot be evaluated here, and reading
// it as its matchLabels alone would produce a wider selector wearing the same
// colour as an exact one. Those are refused rather than approximated.
// The third return is why the selector could not be read, empty when it was.
// There are three ways to fail and they need different words: a reader told
// that `podSelector: whatever` uses matchExpressions goes looking for syntax
// that is not there.
func labelSelector(v any, path ...string) (match map[string]string, everything bool, why string) {
	sel := dig(v, path...)
	if sel == nil {
		return nil, false, ""
	}
	if _, isMap := sel.(map[string]any); !isMap {
		// `podSelector: something` is not a selector. An empty selector
		// selects every pod in the namespace, so reading a malformed one as
		// empty would restrict the whole namespace on the strength of a typo.
		return nil, false, "not a selector"
	}
	if len(seq(sel, "matchExpressions")) > 0 {
		return nil, false, "written with matchExpressions, which this does not evaluate"
	}
	labels, whole := strMapAll(sel, "matchLabels")
	if !whole {
		return nil, false, "carrying a label value that is not a string"
	}
	return labels, len(labels) == 0, ""
}

func firstReason(reasons ...string) string {
	for _, r := range reasons {
		if r != "" {
			return r
		}
	}
	return ""
}

// workloadsMatching finds the workloads in a namespace whose pod template
// carries every label in a selector.
func (b *builder) workloadsMatching(namespace string, sel map[string]string, everything bool) []string {
	var out []string
	for i := range b.all {
		o := &b.all[i]
		labels := podLabels(o)
		if o.namespace != namespace || labels == nil {
			continue
		}
		if everything || covers(labels, sel) {
			out = append(out, o.id())
		}
	}
	sort.Strings(out)
	return out
}

// namespacesMatching needs Namespace objects to have been part of the input.
// A namespace selector cannot be evaluated against namespaces nobody sent.
func (b *builder) namespacesMatching(sel map[string]string, everything bool) []string {
	var out []string
	for name, labels := range b.nsLabels {
		if everything || covers(labels, sel) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// cidrNode gives a CIDR peer somewhere to point. An open block is the same
// idea the reachable enricher already draws as the internet, so it reuses that
// node rather than introducing a second name for it.
func (b *builder) cidrNode(cidr string) string {
	if cidr == "0.0.0.0/0" || cidr == "::/0" {
		return b.internet()
	}
	id := "cidr:" + cidr
	if _, ok := b.g.Node(id); !ok {
		b.g.Nodes = append(b.g.Nodes, core.Node{
			ID: id, Type: "cidr", Name: cidr, Attrs: map[string]any{"cidr": cidr},
		})
	}
	return id
}

// internet is the node an open block points at. Its shape is copied from the
// enrichers rather than chosen here, because an id defined two ways is a
// conflict the moment two graphs are combined.
func (b *builder) internet() string {
	if _, ok := b.g.Node(external); !ok {
		b.g.Nodes = append(b.g.Nodes, core.Node{
			ID: external, Type: "external_endpoint", Name: "Internet", Provider: "external",
		})
	}
	return external
}

func describeSelector(sel map[string]string, everything bool) string {
	if everything {
		return "any label"
	}
	return joined(sel)
}

// describePorts renders a rule's ports for an edge attribute. A rule with no
// ports allows every port, which is worth saying rather than leaving blank.
func describePorts(rule any) string {
	ports := seq(rule, "ports")
	if len(ports) == 0 {
		return "all ports"
	}
	var out []string
	for _, p := range ports {
		protocol := str(p, "protocol")
		if protocol == "" {
			protocol = "TCP"
		}
		port := str(p, "port")
		if port == "" {
			if n, ok := num(p, "port"); ok {
				port = fmt.Sprintf("%d", int(n))
			}
		}
		if port == "" {
			port = "any"
		}
		if end, ok := num(p, "endPort"); ok {
			port += fmt.Sprintf("-%d", int(end))
		}
		out = append(out, protocol+"/"+port)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}
