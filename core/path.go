package core

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Path is an ordered walk through the estate: this one called that one, and
// that one called the next.
//
// # Why this is an entity and not a query
//
// Everything the graph can already say about use is said one hop at a time. An
// edge records that the checkout service calls the ledger; nothing records
// that a request arrives at the gateway, passes through checkout, and ends at
// the ledger — and that is the thing an operator acts on. A route nobody has
// walked in ninety days is a route to delete. A route that fires at three in
// the morning when nothing should be calling it is an incident. Neither
// statement can be made about an edge, because both are about an order.
//
// Making it an entity is also what turns three separate features into one. The
// unused list, the count that spiked or went silent, and the path that fired
// when nothing declared it are the same noun with different evidence attached:
// a subject observations can name, a set the declared and the observed can be
// compared as, and a thing a claim can be about.
//
// # Kind means what it means everywhere else
//
// A path carries the same three kinds an edge does, and for the same reason. A
// path derived from what the configuration references is a claim about what
// *can* happen; a path built from traces is a claim about what *did*. Reusing
// the vocabulary is what lets the two be compared at all — the whole point of
// the entity is the gap between them.
type Path struct {
	// Nodes is the walk, in order, from the first participant to the last. At
	// least two, because one node is not a path, and repeats are allowed
	// because a real request can come back through something it already went
	// through.
	Nodes []string `json:"nodes"`

	Kind EdgeKind `json:"kind"`

	// Label is what to call this route in a listing, when somebody has a name
	// for it that is better than its ends.
	Label string `json:"label,omitempty"`

	Attrs map[string]any `json:"attrs,omitempty"`

	// Claim is who says this path exists. Absent means a parser derived it.
	Claim *Claim `json:"claim,omitempty"`
}

// PathKey names a path for an observation subject or a conflict target.
//
// Each participant is independently base64url encoded, exactly as EdgeKey
// encodes its components, so the key is reversible and cannot collide with
// another path whose node ids happen to contain the separator. A metric about
// a route is therefore an ordinary observation with an ordinary subject, and
// every threshold, window and claim that already applies to observations
// applies to it without a second mechanism.
func PathKey(nodes []string) string {
	encoded := make([]string, 0, len(nodes))
	for _, n := range nodes {
		encoded = append(encoded, base64.RawURLEncoding.EncodeToString([]byte(n)))
	}
	return "path:" + strings.Join(encoded, ".")
}

// ParsePathKey recovers the walk from a key, and reports whether the string
// was one.
func ParsePathKey(key string) ([]string, bool) {
	if !strings.HasPrefix(key, "path:") {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(key, "path:"), ".")
	if len(parts) < 2 {
		return nil, false
	}
	nodes := make([]string, 0, len(parts))
	for _, part := range parts {
		b, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil || !utf8.Valid(b) || len(b) == 0 {
			return nil, false
		}
		nodes = append(nodes, string(b))
	}
	// Round-tripping is the check: an encoding that is not the one PathKey
	// produces is not this key, however well it happened to decode.
	if PathKey(nodes) != key {
		return nil, false
	}
	return nodes, true
}

// QualifySubject rewrites an observation's subject through a renaming of node
// ids, and reports whether it was a path key.
//
// A path key is not an id: it is an encoding of several. Putting a scope in
// front of the whole string produces something that is neither an id nor a
// key, so a document combined from two repositories ends up with readings
// about routes that do not exist — which the validator catches, turning a
// second repository into a hard error rather than a bigger diagram.
func QualifySubject(subject string, qualify func(string) string) (string, bool) {
	nodes, ok := ParsePathKey(subject)
	if !ok {
		return subject, false
	}
	renamed := make([]string, 0, len(nodes))
	for _, id := range nodes {
		renamed = append(renamed, qualify(id))
	}
	return PathKey(renamed), true
}

// QualifyPaths rewrites every participant through a renaming of node ids.
func (g *Graph) QualifyPaths(qualify func(string) string) {
	for i := range g.Paths {
		for j := range g.Paths[i].Nodes {
			g.Paths[i].Nodes[j] = qualify(g.Paths[i].Nodes[j])
		}
	}
}

// Key is this path's name as an observation subject.
func (p Path) Key() string { return PathKey(p.Nodes) }

// Path returns the path with the given walk and kind.
func (g *Graph) Path(nodes []string, kind EdgeKind) (*Path, bool) {
	want := PathKey(nodes)
	for i := range g.Paths {
		if g.Paths[i].Kind == kind && g.Paths[i].Key() == want {
			return &g.Paths[i], true
		}
	}
	return nil, false
}

// PathsOfKind returns the paths of a single kind, in stored order.
func (g *Graph) PathsOfKind(kind EdgeKind) []Path {
	var out []Path
	for _, p := range g.Paths {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// normalizePaths sorts and folds duplicates.
//
// Two records of the same walk with the same kind are one path: traces are
// full of the same route walked again, and a document that kept one entry per
// walk would grow with traffic rather than with the estate. Their attributes
// merge the way an edge's do; the best claim among them is the one kept.
func (g *Graph) normalizePaths() {
	if len(g.Paths) == 0 {
		return
	}
	sort.SliceStable(g.Paths, func(i, j int) bool {
		a, b := g.Paths[i], g.Paths[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if ak, bk := a.Key(), b.Key(); ak != bk {
			return ak < bk
		}
		// The best claim first, on the same terms an edge is ranked by:
		// human, then ai, then parser. Ordering by the origin's spelling
		// instead — which is what comparing claims alone does — would put an
		// "ai" claim ahead of a "human" one, and the fold keeps the first.
		ac, bc := claimOrParser(a.Claim), claimOrParser(b.Claim)
		if ac.Origin.Rank() != bc.Origin.Rank() {
			return ac.Origin.Rank() > bc.Origin.Rank()
		}
		return claimLess(ac, bc)
	})

	folded := g.Paths[:0]
	for _, p := range g.Paths {
		if len(folded) > 0 {
			at := &folded[len(folded)-1]
			if at.Kind == p.Kind && at.Key() == p.Key() {
				at.Attrs = mergeAttrs(at.Attrs, p.Attrs)
				if at.Label == "" {
					at.Label = p.Label
				}
				continue
			}
		}
		folded = append(folded, p)
	}
	g.Paths = folded
}

// checkPaths reports what is wrong with the paths in this document.
func (g *Graph) checkPaths(nodeIDs map[string]bool) []string {
	var problems []string
	seen := map[string]bool{}
	for i, p := range g.Paths {
		where := fmt.Sprintf("path %d", i)
		if p.Label != "" {
			where += " (" + p.Label + ")"
		}
		if len(p.Nodes) < 2 {
			problems = append(problems, where+": a path needs at least two participants")
			continue
		}
		if !p.Kind.Valid() {
			problems = append(problems, fmt.Sprintf("%s: unknown kind %q", where, p.Kind))
		}
		for _, id := range p.Nodes {
			if id == "" {
				problems = append(problems, where+": empty participant")
				continue
			}
			// A container cannot be a participant. A path is a sequence of
			// things that act, and a subnet does not call anything; allowing
			// one would also make the key ambiguous about what it names.
			if !nodeIDs[id] {
				problems = append(problems, fmt.Sprintf("%s: unknown participant %q", where, id))
			}
		}
		key := string(p.Kind) + "\x00" + p.Key()
		if seen[key] {
			problems = append(problems, where+": duplicate path")
		}
		seen[key] = true
		problems = append(problems, checkClaim(p.Claim, where)...)
	}
	return problems
}
