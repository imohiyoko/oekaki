package overlay

import (
	"sort"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/providers"
)

// Resolution rule names, recorded on evidence so a surprising join can be
// traced without rerunning anything.
const (
	RuleID        = "id"
	RuleScopedID  = "scoped-id"
	RuleIdentity  = "identity"
	RuleTypeName  = "type+name"
	RuleName      = "name"
	RuleNoneGiven = "empty-selector"
)

// Resolution is what a selector found.
//
// Exactly one of the three outcomes holds: ID set (resolved), Candidates
// longer than one (ambiguous), or neither (unmatched).
type Resolution struct {
	ID         string
	Rule       string
	Candidates []string

	// Stopped is set when an exact id was given and did not exist. The ladder
	// halts there rather than falling through to a fuzzier rule: somebody who
	// typed an id meant that id, and rescuing them with a name match would
	// silently attach their assertion to a different resource.
	Stopped bool
}

// Index answers selectors against one graph. Built once per run.
type Index struct {
	scope string

	ids        map[string]bool
	byIdentity map[string]map[string][]string
	byTypeName map[string][]string
	byName     map[string][]string
}

// NewIndex builds the lookup tables a resolver needs.
func NewIndex(g *core.Graph) *Index {
	ix := &Index{
		ids:        map[string]bool{},
		byIdentity: map[string]map[string][]string{},
		byTypeName: map[string][]string{},
		byName:     map[string][]string{},
	}
	if g.Metadata != nil {
		ix.scope = g.Metadata.Scope
	}

	for _, grp := range g.Groups {
		ix.ids[grp.ID] = true
		ix.byName[grp.Label] = append(ix.byName[grp.Label], grp.ID)
	}
	for _, n := range g.Nodes {
		ix.ids[n.ID] = true
		ix.byTypeName[n.Type+"\x00"+n.Name] = append(ix.byTypeName[n.Type+"\x00"+n.Name], n.ID)
		ix.byName[n.Name] = append(ix.byName[n.Name], n.ID)

		for key := range providers.Identities(n.Type) {
			value, ok := providers.IdentityOf(n.Type, key, n.Attrs)
			if !ok {
				continue
			}
			if ix.byIdentity[key] == nil {
				ix.byIdentity[key] = map[string][]string{}
			}
			ix.byIdentity[key][value] = append(ix.byIdentity[key][value], n.ID)
		}
	}
	return ix
}

// Has reports whether an id is in the graph.
func (ix *Index) Has(id string) bool { return ix.ids[id] }

// Add registers an id created during enrichment, so a later assertion can
// resolve against something an earlier one adopted.
//
// The selector it was created from is registered too. Without that, a second
// assertion naming the same subject resolves to nothing again — the identity
// rule finds no entry and does not fall through — and the subject is reported
// unmatched twice for one thing. Pass a nil selector when there is nothing to
// register under.
func (ix *Index) Add(id, typ, name string, sel Selector) {
	ix.ids[id] = true
	ix.byTypeName[typ+"\x00"+name] = append(ix.byTypeName[typ+"\x00"+name], id)
	ix.byName[name] = append(ix.byName[name], id)

	for key, value := range sel {
		if !providers.IsSelectorKey(key) {
			continue
		}
		if ix.byIdentity[key] == nil {
			ix.byIdentity[key] = map[string][]string{}
		}
		ix.byIdentity[key][value] = append(ix.byIdentity[key][value], id)
	}
}

// Resolve runs the ladder.
//
// Rules are tried in order and the first one that produces any candidate wins;
// later rules are not tried. Falling through would mean a precise selector
// that misses gets quietly rescued by a sloppy one, which is how a coverage
// map ends up attached to the wrong resource while looking entirely healthy.
func (ix *Index) Resolve(s Selector) Resolution {
	if len(s) == 0 {
		return Resolution{Rule: RuleNoneGiven}
	}

	// R1 and R2: an exact id, then the same id under the document's scope.
	// The parser qualifies ids as "scope:id" when a graph covers more than one
	// state, and somebody reading a console has no way to know that happened.
	if id, ok := firstOf(s, "node", "id", "group"); ok {
		if ix.ids[id] {
			return Resolution{ID: id, Rule: RuleID}
		}
		if ix.scope != "" && ix.ids[ix.scope+":"+id] {
			return Resolution{ID: ix.scope + ":" + id, Rule: RuleScopedID}
		}
		return Resolution{Rule: RuleID, Stopped: true}
	}

	// R3: identity keys, all of which must match the same node. A workload
	// name in one namespace is not the same workload as that name in another,
	// so the keys are ANDed rather than tried in turn.
	if got, ok := ix.resolveIdentity(s); ok {
		return finish(got, RuleIdentity)
	}

	// R4: type and name together.
	if typ, ok := s["type"]; ok {
		if name, ok := s["name"]; ok {
			return finish(ix.byTypeName[typ+"\x00"+name], RuleTypeName)
		}
	}

	// R5: a bare name. Usually ambiguous, which is the point — it will say so
	// rather than pick one.
	if name, ok := s["name"]; ok {
		return finish(ix.byName[name], RuleName)
	}

	return Resolution{}
}

func (ix *Index) resolveIdentity(s Selector) ([]string, bool) {
	var acc []string
	var any bool

	for _, key := range sortedSelector(s) {
		if !providers.IsSelectorKey(key) {
			continue
		}
		got := ix.byIdentity[key][s[key]]
		if !any {
			acc, any = got, true
			continue
		}
		acc = intersect(acc, got)
	}
	return acc, any
}

func finish(candidates []string, rule string) Resolution {
	switch len(candidates) {
	case 0:
		return Resolution{Rule: rule}
	case 1:
		return Resolution{ID: candidates[0], Rule: rule}
	default:
		out := append([]string(nil), candidates...)
		sort.Strings(out)
		return Resolution{Rule: rule, Candidates: out}
	}
}

func firstOf(s Selector, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := s[k]; ok {
			return v, true
		}
	}
	return "", false
}

func intersect(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range a {
		if in[x] {
			out = append(out, x)
		}
	}
	return out
}

func sortedSelector(s Selector) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// label picks the most specific human-readable value in a selector, for
// naming a node adopted from it.
func (s Selector) label() string {
	for _, k := range []string{"workload", "service", "function", "log_group", "search_index", "load_balancer", "bucket", "name", "node", "id", "group"} {
		if v, ok := s[k]; ok && v != "" {
			return v
		}
	}
	for _, k := range sortedSelector(s) {
		if s[k] != "" {
			return s[k]
		}
	}
	return "asserted"
}

// key is a stable identity for a selector, used to name adopted nodes so that
// re-running with the same overlay produces the same id.
func (s Selector) key() string {
	var out string
	for _, k := range sortedSelector(s) {
		if out != "" {
			out += ","
		}
		out += k + "=" + s[k]
	}
	return out
}

// asMap copies a selector for a report.
func (s Selector) asMap() map[string]string {
	out := make(map[string]string, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}
