// Package reachable derives conservative network reachability from security
// group ingress rules already present in an IR graph. Unknown/expressive rules
// are skipped rather than turned into a confident edge.
package reachable

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

type Enricher struct {
	Documents []*Document
}

// Document is a normalized result from a network-policy, NACL, proxy, or
// cloud collector. It lets environments outside the AWS security-group model
// contribute effective reachability without putting credentials in this repo.
type Document struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Paths   []Path `json:"paths"`
}

type Path struct {
	From     string      `json:"from"`
	To       string      `json:"to"`
	Protocol string      `json:"protocol,omitempty"`
	Port     int         `json:"port,omitempty"`
	Allowed  bool        `json:"allowed"`
	Reason   string      `json:"reason,omitempty"`
	Claim    *core.Claim `json:"claim,omitempty"`
}

func Parse(raw []byte) (*Document, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Document
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("parsing reachability document: %w", err)
	}
	if d.Kind != "oekaki.reachability" || d.Version == "" {
		return nil, fmt.Errorf("reachability document requires kind oekaki.reachability and version")
	}
	for i, p := range d.Paths {
		if p.From == "" || p.To == "" {
			return nil, fmt.Errorf("paths[%d] requires from and to", i)
		}
	}
	return &d, nil
}

func (Enricher) Name() string { return "reachable" }

func (e Enricher) Enrich(g *core.Graph) (*enrichers.Report, error) {
	r := &enrichers.Report{Enricher: "reachable"}
	attached := map[string][]string{}
	for _, e := range g.Edges {
		if e.Kind != core.EdgeIACRef {
			continue
		}
		if isSecurityGroup(g, e.To) && !isSecurityGroup(g, e.From) && !isSecurityGroupRule(g, e.From) {
			attached[e.To] = append(attached[e.To], e.From)
		}
	}
	seen := map[string]bool{}
	for _, edge := range g.Edges {
		if edge.Kind == core.EdgeReachable {
			seen[core.EdgeKey(edge.From, edge.To, edge.Kind, edge.Relation)] = true
			if edge.Relation == "" {
				seen[core.EdgeKey(edge.From, edge.To, edge.Kind, "reachable")] = true
			}
		}
	}
	for _, sg := range g.Nodes {
		if sg.Type != "aws_security_group" {
			continue
		}
		applyInlineRules(g, sg.ID, attached, sg.Attrs, seen, r)
	}
	for _, ruleNode := range g.Nodes {
		if ruleNode.Type != "aws_security_group_rule" {
			continue
		}
		target, sources := ruleTargets(g, ruleNode.ID)
		if target == "" || !isSecurityGroup(g, target) {
			continue
		}
		attrs := ruleNode.Attrs
		if attrs == nil {
			continue
		}
		if isEgressRule(attrs) {
			for _, source := range sources {
				for _, from := range attached[target] {
					for _, to := range attached[source] {
						add(g, seen, from, to, ruleAttrs(attrs), r)
					}
				}
			}
			if publicCIDR(attrs) {
				ensureInternet(g)
				for _, from := range attached[target] {
					add(g, seen, from, "external:internet", ruleAttrs(attrs), r)
				}
			}
		} else {
			for _, source := range sources {
				for _, from := range attached[source] {
					for _, to := range attached[target] {
						add(g, seen, from, to, ruleAttrs(attrs), r)
					}
				}
			}
			if publicCIDR(attrs) {
				ensureInternet(g)
				for _, to := range attached[target] {
					add(g, seen, "external:internet", to, ruleAttrs(attrs), r)
				}
			}
		}
	}
	applyDocuments(g, e.Documents, seen, r)
	addInternetObservations(g)
	g.Normalize()
	r.Sort()
	return r, nil
}

func applyDocuments(g *core.Graph, docs []*Document, seen map[string]bool, report *enrichers.Report) {
	known := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	for _, group := range g.Groups {
		known[group.ID] = true
	}
	for _, doc := range docs {
		for _, p := range doc.Paths {
			if !known[p.From] || !known[p.To] {
				report.Unmatched = append(report.Unmatched, enrichers.Unmatched{Selector: map[string]string{"from": p.From, "to": p.To}, Assert: "reachability", Reason: "endpoint not found", Action: "reported"})
				continue
			}
			if p.Allowed {
				addWithClaim(g, seen, p.From, p.To, map[string]any{"protocol": p.Protocol, "port": p.Port, "reason": p.Reason}, p.Claim, report)
			}
			value := 0.0
			state := "blocked"
			if p.Allowed {
				value = 1
				state = "allowed"
			}
			labels := map[string]string{"to": p.To}
			if p.Protocol != "" {
				labels["protocol"] = p.Protocol
			}
			if p.Port != 0 {
				labels["port"] = fmt.Sprint(p.Port)
			}
			g.Observations = append(g.Observations, core.Observation{Subject: p.From, Metric: "network_path_allowed", Labels: labels, Value: &value, Unit: "boolean", State: state, Reason: p.Reason, Evidence: p.Claim})
		}
	}
}

func addInternetObservations(g *core.Graph) {
	existing := map[string]bool{}
	for _, o := range g.Observations {
		existing[o.Subject+"\x00"+o.Metric] = true
	}
	for _, e := range g.Edges {
		if e.Kind != core.EdgeReachable || (e.From != "external:internet" && e.To != "external:internet") {
			continue
		}
		subject := e.To
		if subject == "external:internet" {
			subject = e.From
		}
		key := subject + "\x00internet_reachability"
		if existing[key] {
			continue
		}
		value := 1.0
		g.Observations = append(g.Observations, core.Observation{
			Subject: subject,
			Metric:  "internet_reachability",
			Value:   &value,
			Unit:    "boolean",
			State:   "abnormal",
			Reason:  "network rule permits reachability to or from the Internet",
		})
		existing[key] = true
	}
}

func applyInlineRules(g *core.Graph, sgID string, attached map[string][]string, attrs map[string]any, seen map[string]bool, report *enrichers.Report) {
	for _, direction := range []struct {
		key    string
		egress bool
	}{
		{key: "ingress"},
		{key: "egress", egress: true},
	} {
		rules, ok := attrs[direction.key].([]any)
		if !ok {
			continue
		}
		for _, raw := range rules {
			rule, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			base := ruleAttrs(rule)
			for _, source := range ruleSecurityGroups(rule) {
				if !isSecurityGroup(g, source) {
					continue
				}
				if direction.egress {
					for _, from := range attached[sgID] {
						for _, to := range attached[source] {
							add(g, seen, from, to, base, report)
						}
					}
				} else {
					for _, from := range attached[source] {
						for _, to := range attached[sgID] {
							add(g, seen, from, to, base, report)
						}
					}
				}
			}
			if publicCIDR(rule) {
				ensureInternet(g)
				if direction.egress {
					for _, from := range attached[sgID] {
						add(g, seen, from, "external:internet", base, report)
					}
				} else {
					for _, to := range attached[sgID] {
						add(g, seen, "external:internet", to, base, report)
					}
				}
			}
		}
	}
}

func ruleTargets(g *core.Graph, ruleID string) (string, []string) {
	var target string
	var sources []string
	for _, e := range g.Edges {
		if e.From != ruleID || !isSecurityGroup(g, e.To) {
			continue
		}
		attribute, _ := e.Attrs["attribute"].(string)
		if strings.Contains(attribute, "security_group_id") && !strings.Contains(attribute, "source") {
			target = e.To
		} else if strings.Contains(attribute, "source_security_group_id") {
			sources = append(sources, e.To)
		}
	}
	return target, sources
}

func ruleSecurityGroups(rule map[string]any) []string {
	var out []string
	for _, key := range []string{"security_groups", "source_security_group_id"} {
		switch values := rule[key].(type) {
		case string:
			out = append(out, normalizeRef(values))
		case []any:
			for _, value := range values {
				if s, ok := value.(string); ok {
					out = append(out, normalizeRef(s))
				}
			}
		}
	}
	return out
}

func isSecurityGroup(g *core.Graph, id string) bool {
	n, ok := g.Node(id)
	return ok && n.Type == "aws_security_group"
}
func isSecurityGroupRule(g *core.Graph, id string) bool {
	n, ok := g.Node(id)
	if !ok {
		return false
	}
	switch n.Type {
	case "aws_security_group_rule", "aws_vpc_security_group_ingress_rule", "aws_vpc_security_group_egress_rule":
		return true
	}
	return false
}
func isEgressRule(attrs map[string]any) bool {
	if direction, ok := attrs["type"].(string); ok {
		return strings.EqualFold(strings.TrimSpace(direction), "egress")
	}
	egress, _ := attrs["egress"].(bool)
	return egress
}
func normalizeRef(s string) string {
	s = strings.TrimSpace(s)
	return strings.TrimSuffix(s, ".id")
}
func publicCIDR(r map[string]any) bool {
	for _, key := range []string{"cidr_blocks", "ipv6_cidr_blocks"} {
		switch xs := r[key].(type) {
		case []any:
			for _, x := range xs {
				if s, ok := x.(string); ok && (s == "0.0.0.0/0" || s == "::/0") {
					return true
				}
			}
		case string:
			if xs == "0.0.0.0/0" || xs == "::/0" {
				return true
			}
		}
	}
	return false
}
func ruleAttrs(r map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"protocol", "from_port", "to_port", "description", "cidr_blocks", "ipv6_cidr_blocks"} {
		if v, ok := r[k]; ok {
			out[k] = v
		}
	}
	return out
}
func ensureInternet(g *core.Graph) {
	if _, ok := g.Node("external:internet"); !ok {
		g.Nodes = append(g.Nodes, core.Node{ID: "external:internet", Type: "external_endpoint", Name: "Internet", Provider: "external"})
	}
}
func add(g *core.Graph, seen map[string]bool, from, to string, attrs map[string]any, r *enrichers.Report) {
	addWithClaim(g, seen, from, to, attrs, nil, r)
}
func addWithClaim(g *core.Graph, seen map[string]bool, from, to string, attrs map[string]any, claim *core.Claim, r *enrichers.Report) {
	if from == to {
		return
	}
	key := core.EdgeKey(from, to, core.EdgeReachable, "reachable")
	if seen[key] {
		for i := range g.Edges {
			edge := &g.Edges[i]
			if edge.From != from || edge.To != to || edge.Kind != core.EdgeReachable || (edge.Relation != "" && edge.Relation != "reachable") {
				continue
			}
			edge.Attrs = mergeReachabilityAttrs(edge.Attrs, attrs)
			if preferredClaim(claim, edge.Claim) {
				copy := *claim
				edge.Claim = &copy
			}
			break
		}
		return
	}
	seen[key] = true
	var edgeClaim *core.Claim
	if claim != nil {
		copy := *claim
		edgeClaim = &copy
	}
	g.Edges = append(g.Edges, core.Edge{From: from, To: to, Kind: core.EdgeReachable, Relation: "reachable", Attrs: attrs, Claim: edgeClaim})
	r.Applied++
}

func preferredClaim(candidate, current *core.Claim) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return candidate.Origin.Rank() > core.OriginParser.Rank()
	}
	if candidate.Origin.Rank() != current.Origin.Rank() {
		return candidate.Origin.Rank() > current.Origin.Rank()
	}
	if candidate.Author != current.Author {
		return candidate.Author < current.Author
	}
	if comparison := compareOptionalConfidence(candidate.Confidence, current.Confidence); comparison != 0 {
		return comparison < 0
	}
	return candidate.Note < current.Note
}

func compareOptionalConfidence(a, b *float64) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	if *a < *b {
		return -1
	}
	if *a > *b {
		return 1
	}
	return 0
}

func mergeReachabilityAttrs(a, b map[string]any) map[string]any {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	merged := make(map[string]any, len(a)+len(b))
	for key, value := range a {
		merged[key] = value
	}
	for key, value := range b {
		current, exists := merged[key]
		if !exists || canonicalJSON(value) < canonicalJSON(current) {
			merged[key] = value
		}
	}
	return merged
}

func canonicalJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
