package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

// Folding is the answer to the complaint underneath every other complaint
// about these drawings: one picture carries more than a picture can carry, and
// what comes out is a grey mat of boxes.
//
// The wrong answers are easy to reach for. Drawing less — a filter — throws
// away what somebody came to see. Drawing smaller makes an unreadable picture
// unreadable at a smaller size. Making the reader fold it themselves asks them
// to find the thing to fold in the picture they cannot read.
//
// So the tool folds, and says what it folded. A fold is not a deletion: the
// box that stands in says how many it stands for, the record says which ones,
// and a viewer can put them back one fold at a time. Nothing here decides
// anything is unimportant — that is the reader's judgement — it decides only
// what can be said in fewer boxes without saying anything different.
//
// # The rules, in the order they run
//
// Each rule is chosen for how little it costs to apply, so a drawing that
// needs a little folding gets the cheap kind and one that needs a lot works
// down the list.
//
//	chain   a run of boxes that pass a thing along with nothing else attached.
//	        The ends stay, the middle becomes one box saying how far it is.
//	twins   four boxes that are the same thing, in the same place, joined to
//	        the same things. The only thing lost is that there were four, and
//	        the count keeps that.
//	leaves  everything hanging off one box by a single line. The shape of the
//	        drawing is untouched; the halo around one box becomes a number.
//
// Chain runs first even though twins is the cheaper fold, because where both
// apply the chain is the true description. Ten services passing a request down
// a line are not ten interchangeable copies — folding them as twins says there
// were ten of the same thing, which is a different claim from ten hops.
//
// # Why a budget rather than a switch
//
// "Fold twins" is a rule; "make this readable" is the request. A budget is the
// request, and it is what lets the same settings work on an estate of forty
// resources and one of four thousand: rules run until the drawing is inside
// the budget and then stop, so nothing is folded that did not need to be.

// Fold kinds, in the order Fold applies them.
const (
	FoldTwins  = "twins"
	FoldLeaves = "leaves"
	FoldChain  = "chain"
)

// FoldKinds lists the rules, in the order they run.
func FoldKinds() []string { return []string{FoldChain, FoldTwins, FoldLeaves} }

// ValidFoldKind reports whether name is one of them.
func ValidFoldKind(name string) bool {
	for _, kind := range FoldKinds() {
		if kind == name {
			return true
		}
	}
	return false
}

// Folded is one group of boxes drawn as one, and what it stands for.
//
// It is recorded rather than left implicit because a fold has to be
// reversible. A viewer puts a fold back by drawing its members again, and the
// only thing it needs for that is this list — the rules themselves run once,
// here, so that folding twice produces the same drawing and the browser is
// not a second implementation of them.
type Folded struct {
	Kind string `json:"kind"`

	// Diagram is the page this fold belongs to, when the folding was done
	// for an atlas. Empty means the only drawing there is.
	//
	// It is here because the same box appears on several pages of an atlas —
	// a workload is on its level and on its own detail page — and a crowd on
	// one of them is not a crowd on another. A record without it would fold a
	// box away on a page whose other members are not even drawn.
	Diagram string `json:"diagram,omitempty"`

	// Stands is the id of the box drawn in their place.
	Stands string `json:"stands"`

	// Members are the ids it stands for, sorted.
	Members []string `json:"members"`

	Label string `json:"label,omitempty"`
}

// FoldOptions asks for a readable drawing rather than for a particular rule.
type FoldOptions struct {
	// Budget is how many boxes the drawing may have. Rules run until it is
	// met and then stop. Zero means DefaultFoldBudget.
	Budget int

	// Rules limits which rules may run. Empty means all of them, in order.
	Rules []string

	// Axis is the containment axis twins are judged in. Empty means the
	// document's default.
	Axis string

	// Keep names boxes that must be drawn as themselves. Whatever the reader
	// is looking at is not something to fold away underneath them.
	Keep []string
}

// DefaultFoldBudget is how many boxes a drawing may have before folding
// starts.
//
// It is a number of boxes rather than a measure of density because it is the
// one a person can check: a reader looking at a drawing of eighty boxes can
// say whether it worked. The value is a starting point taken from the
// diagrams in this repository and the estates they came from, not a measured
// threshold, and it is meant to be overridden.
const DefaultFoldBudget = 80

// Fold returns a copy of the graph with what can be said in fewer boxes said
// in fewer boxes, and the record of what was folded.
//
// The input is never mutated. The result is a valid graph in its own right:
// every fold leaves a box behind, and every line that pointed at a folded box
// points at the box standing in its place, carrying how many lines it stands
// for.
func Fold(g *core.Graph, opts FoldOptions) (*core.Graph, []Folded, error) {
	if g == nil {
		return nil, nil, fmt.Errorf("no graph")
	}
	budget := opts.Budget
	if budget <= 0 {
		budget = DefaultFoldBudget
	}
	rules := opts.Rules
	if len(rules) == 0 {
		rules = FoldKinds()
	}
	for _, rule := range rules {
		if !ValidFoldKind(rule) {
			return nil, nil, fmt.Errorf("unknown fold %q: want %s", rule, strings.Join(FoldKinds(), ", "))
		}
	}

	out, err := clone(g)
	if err != nil {
		return nil, nil, err
	}
	axis := out.AxisOrDefault(opts.Axis)
	keep := map[string]bool{}
	for _, id := range opts.Keep {
		keep[id] = true
	}

	var folds []Folded
	for _, rule := range rules {
		if len(out.Nodes) <= budget {
			break
		}
		var found []Folded
		switch rule {
		case FoldTwins:
			found = twins(out, axis, keep)
		case FoldLeaves:
			found = leaves(out, axis, keep)
		case FoldChain:
			found = chains(out, keep)
		}
		if len(found) == 0 {
			continue
		}
		if err := apply(out, found); err != nil {
			return nil, nil, err
		}
		folds = append(folds, found...)
	}

	out.Normalize()
	if err := out.Validate(); err != nil {
		return nil, nil, fmt.Errorf("folding produced an invalid graph: %w", err)
	}
	sort.SliceStable(folds, func(i, j int) bool {
		if folds[i].Kind != folds[j].Kind {
			return folds[i].Kind < folds[j].Kind
		}
		return folds[i].Stands < folds[j].Stands
	})
	return out, folds, nil
}

// FoldAtlas works out what to fold on each page of an atlas.
//
// The pages themselves are left alone. They keep their whole graphs, because
// the viewer is what folds them and it needs the members to put back — the
// same reason the single-diagram page is handed the unfolded graph and the
// record rather than the folded graph.
//
// Each page is folded on its own terms. That is the whole point of doing it
// here rather than once over the estate: a level page draws twelve namespaces
// and a detail page draws one workload and its neighbours, and a crowd in one
// is not a crowd in the other. A budget spent against the estate would land on
// a page holding three of its twelve members and still say twelve.
//
// Openings are not touched. A box that is folded away is not drawn, so nothing
// asks whether it opens anything; when the reader puts the fold back, the way
// down comes back with it.
func FoldAtlas(a *Atlas, opts FoldOptions) ([]Folded, error) {
	if a == nil {
		return nil, fmt.Errorf("no atlas")
	}
	var all []Folded
	for i := range a.Diagrams {
		// The folded graph is discarded: it is the record that travels. It is
		// still built, because building it is what checks that the fold
		// produces a document that holds together.
		_, folds, err := Fold(a.Diagrams[i].Graph, opts)
		if err != nil {
			return nil, fmt.Errorf("folding %s: %w", a.Diagrams[i].ID, err)
		}
		for _, f := range folds {
			f.Diagram = a.Diagrams[i].ID
			all = append(all, f)
		}
	}
	return all, nil
}

// apply replaces every fold's members with the box that stands for them.
func apply(g *core.Graph, folds []Folded) error {
	at := map[string]string{}
	for _, f := range folds {
		for _, id := range f.Members {
			at[id] = f.Stands
		}
	}

	// A line whose ends both land inside one fold said something about what is
	// in there — four pods that talk to each other — and lifting it would draw
	// a box joined to itself, which says nothing and draws badly. It becomes a
	// number on the box instead, because "this one is mostly self-contained"
	// is worth being able to see.
	inside := map[string]int{}
	for _, e := range g.Edges {
		if e.Suppressed {
			continue
		}
		from, okFrom := at[e.From]
		to, okTo := at[e.To]
		if okFrom && okTo && from == to {
			inside[from]++
		}
	}

	kept := g.Nodes[:0]
	byID := map[string]core.Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	for _, n := range g.Nodes {
		if _, isMember := at[n.ID]; isMember {
			continue
		}
		kept = append(kept, n)
	}
	for _, f := range folds {
		kept = append(kept, standIn(f, byID, inside[f.Stands]))
	}
	g.Nodes = kept

	for id := range at {
		delete(byID, id)
	}
	// Everything still drawn stands for itself. Containers included: either
	// end of an edge may be a group, and a lift table that only knows about
	// nodes drops those lines entirely — a fold nobody asked for, of a line
	// nothing folded.
	for _, n := range g.Nodes {
		at[n.ID] = n.ID
	}
	for _, grp := range g.Groups {
		at[grp.ID] = grp.ID
	}
	g.Edges = liftEdges(g.Edges, at)

	// A route through a folded box is a route through the box that stands for
	// it. Consecutive participants that land on the same box collapse, and a
	// route that has nothing left to say — every participant inside one fold —
	// goes with them.
	var routes []core.Path
	// A route that folded is still the same route, so a reading about it
	// follows it to its new name. Only a route that lost its way entirely —
	// every participant inside one fold — takes its readings with it.
	renamed := map[string]string{}
	for _, p := range g.Paths {
		was := p.Key()
		walk := make([]string, 0, len(p.Nodes))
		for _, id := range p.Nodes {
			stands, isMember := at[id]
			if !isMember {
				stands = id
			}
			if len(walk) > 0 && walk[len(walk)-1] == stands {
				continue
			}
			walk = append(walk, stands)
		}
		if len(walk) < 2 {
			continue
		}
		p.Nodes = walk
		renamed[was] = p.Key()
		routes = append(routes, p)
	}
	g.Paths = routes

	// A reading about a box that is no longer drawn has nothing to attach to.
	// Rolling it up onto the box standing in would be this package inventing a
	// measurement nobody took.
	present := map[string]bool{}
	for _, n := range g.Nodes {
		present[n.ID] = true
	}
	for _, grp := range g.Groups {
		present[grp.ID] = true
	}
	keys := map[string]bool{}
	for _, p := range g.Paths {
		keys[p.Key()] = true
	}
	observations := g.Observations[:0]
	for _, o := range g.Observations {
		if now, moved := renamed[o.Subject]; moved {
			o.Subject = now
		}
		if present[o.Subject] || keys[o.Subject] {
			observations = append(observations, o)
		}
	}
	g.Observations = observations

	records := g.LogRecords[:0]
	for _, r := range g.LogRecords {
		if r.Source == "" || present[r.Source] {
			records = append(records, r)
		}
	}
	g.LogRecords = records
	filterConflicts(g)
	return nil
}

// standIn builds the box drawn in a fold's place.
//
// It carries what every member agrees on and nothing else. That is the whole
// rule, and it is what keeps a fold from making a claim none of its members
// made: a box drawn in the production namespace because the first member
// happened to be there, or drawn as a workload whose logs are flowing because
// the alphabetically first of a mixed group's were.
//
// Twins agree on all of it by construction — the cohort key is exactly this
// list — so nothing is lost there. A chain of a queue, a worker and a bucket
// agrees on almost none of it, and says so by carrying almost none of it.
func standIn(f Folded, members map[string]core.Node, internal int) core.Node {
	agreed := func(of func(core.Node) string) string {
		first, settled := "", false
		for _, id := range f.Members {
			m, ok := members[id]
			if !ok {
				continue
			}
			value := of(m)
			if !settled {
				first, settled = value, true
				continue
			}
			if value != first {
				return ""
			}
		}
		return first
	}

	kind := agreed(func(n core.Node) string { return n.Type })
	if kind == "" {
		// A run of different things is not any of them. Naming it after the
		// fold is the only description that is true of all of it.
		kind = f.Kind
	}
	n := core.Node{
		ID:   f.Stands,
		Type: kind,
		Name: f.Label,
		Attrs: map[string]any{
			"fold":    f.Kind,
			"members": len(f.Members),
		},
		Provider: agreed(func(n core.Node) string { return n.Provider }),
	}
	if internal > 0 {
		n.Attrs["internal_references"] = internal
	}

	// Where it is drawn, when they are all in the same place. A box that
	// stands for things in two containers belongs to neither.
	for axis := range members[f.Members[0]].Groups {
		path := agreed(func(m core.Node) string { return m.Groups[axis] })
		if path == "" {
			continue
		}
		if n.Groups == nil {
			n.Groups = map[string]string{}
		}
		n.Groups[axis] = path
	}

	// Coverage is the subject of a whole class of these drawings, and absent
	// coverage means nobody knows — so dropping it turns a box everybody has
	// looked at into one nobody has.
	if state := agreed(coverageOf); state != "" {
		n.Coverage = members[f.Members[0]].Coverage
	}

	n.Claim = agreedClaim(f, members)
	return n
}

// agreedClaim is the part of its members' claims that all of them made.
//
// A box standing for one thing a person asserted and three a parser found is
// not an assertion, and drawing it as one would put somebody's name on three
// things they never said. That much is obvious. The quieter version of the
// same mistake is a claim whose origin and author agree but whose note or
// confidence do not: the box would then show one member's sentence as though
// it were said about all of them, and a reader has no way to tell.
//
// So the claim is assembled field by field. Every field the members agree on
// survives; every field they do not agree on is left off, which is the honest
// thing to say about it and is exactly what an absent optional field means
// here. An unstated confidence is not the same as a confidence of zero, so
// the two are told apart rather than compared as numbers.
func agreedClaim(f Folded, members map[string]core.Node) *core.Claim {
	claims := make([]*core.Claim, 0, len(f.Members))
	for _, id := range f.Members {
		if m, ok := members[id]; ok {
			claims = append(claims, m.Claim)
		}
	}
	if len(claims) == 0 {
		return nil
	}

	// Absent means a parser found it, which is the overwhelmingly common case
	// and costs no bytes. A fold of things nobody claimed claims nothing.
	stated := false
	for _, c := range claims {
		if c != nil {
			stated = true
		}
	}
	if !stated {
		return nil
	}

	origin := func(c *core.Claim) core.Origin {
		if c == nil {
			return originOfAbsentClaim
		}
		return c.Origin
	}
	for _, c := range claims[1:] {
		if origin(c) != origin(claims[0]) {
			return nil
		}
	}
	out := &core.Claim{Origin: origin(claims[0])}

	agreesOn := func(of func(*core.Claim) string) (string, bool) {
		first := of(claims[0])
		for _, c := range claims[1:] {
			if of(c) != first {
				return "", false
			}
		}
		return first, true
	}
	if author, same := agreesOn(func(c *core.Claim) string {
		if c == nil {
			return ""
		}
		return c.Author
	}); same {
		out.Author = author
	}
	if note, same := agreesOn(func(c *core.Claim) string {
		if c == nil {
			return ""
		}
		return c.Note
	}); same {
		out.Note = note
	}
	if _, same := agreesOn(func(c *core.Claim) string {
		if c == nil || c.Confidence == nil {
			return "unstated"
		}
		return fmt.Sprintf("%v", *c.Confidence)
	}); same && claims[0] != nil {
		out.Confidence = claims[0].Confidence
	}
	return out
}

// originOfAbsentClaim is what a missing claim means: a parser derived it from
// an input file.
const originOfAbsentClaim = core.OriginParser

// twins finds boxes that are the same thing, in the same place, joined to the
// same things.
//
// Every part of that is load-bearing. Same type and same container is what
// makes them the same *kind* of thing; the same neighbours is what makes them
// interchangeable in this drawing. Two pods where one also talks to a
// database are not twins, and folding them would hide the only interesting
// thing about that pod.
//
// Coverage and claim are part of the signature too. A workload nothing logs
// and one that logs is not the same box in a drawing whose subject is which is
// which, and a thing a person asserted is not the same as a thing a parser
// found.
func twins(g *core.Graph, axis string, keep map[string]bool) []Folded {
	// Two passes. The first sorts boxes into cohorts of the same kind of
	// thing; the second compares what each is joined to, ignoring the members
	// of its own cohort.
	//
	// Ignoring them is what lets a mesh fold at all. Four workers that call
	// each other have four different neighbour sets — each other's — and
	// comparing those means a set of peers can never be folded, which is the
	// commonest crowd there is. The identity of a peer that is going into the
	// same box is not a difference between them; the *number* of peer links
	// is, so that stays in the signature and an odd one out stays out.
	cohort := map[string]string{}
	for _, n := range g.Nodes {
		if keep[n.ID] || strings.HasPrefix(n.ID, foldPrefix) {
			continue
		}
		cohort[n.ID] = cohortKey(n, axis)
	}

	sig := map[string][]string{}
	for _, n := range g.Nodes {
		key, candidate := cohort[n.ID]
		if !candidate {
			continue
		}
		signature := key + "\x03" + joinedKey(g, n, cohort)
		sig[signature] = append(sig[signature], n.ID)
	}

	var out []Folded
	for _, ids := range sig {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		out = append(out, Folded{
			Kind: FoldTwins, Stands: foldID(FoldTwins, ids[0]), Members: ids,
			Label: countLabel(commonName(g, ids), len(ids)),
		})
	}
	return out
}

// cohortKey is what makes two boxes the same kind of thing in the same place.
//
// Coverage and claim are part of it. A workload nothing logs and one that logs
// is not the same box in a drawing whose subject is which is which, and a
// thing a person asserted is not the same as a thing a parser found.
func cohortKey(n core.Node, axis string) string {
	return strings.Join([]string{
		n.Type, n.Groups[axis], n.Provider, coverageOf(n), claimKey(n.Claim),
	}, "\x01")
}

// joinedKey is what a box is joined to, with its own cohort left out and
// counted instead.
func joinedKey(g *core.Graph, n core.Node, cohort map[string]string) string {
	mine := cohort[n.ID]
	peers := 0
	var joined []string
	for _, e := range g.Edges {
		var direction, other string
		switch {
		case e.From == n.ID:
			direction, other = "out", e.To
		case e.To == n.ID:
			direction, other = "in", e.From
		default:
			continue
		}
		if other != n.ID && cohort[other] == mine {
			peers++
			continue
		}
		joined = append(joined, direction+"\x00"+other+"\x00"+string(e.Kind)+"\x00"+e.Relation)
	}
	sort.Strings(joined)
	return fmt.Sprintf("peers=%d\x02%s", peers, strings.Join(joined, "\x02"))
}

func coverageOf(n core.Node) string {
	if n.Coverage == nil {
		return ""
	}
	return string(n.Coverage.State)
}

// claimKey is who said so, for the purpose of deciding whether two boxes are
// the same kind of thing. It is deliberately coarser than the claim itself:
// two things the same person asserted with different notes are still two
// things that person asserted, and a fold should not be prevented by a
// sentence. What the *box* then carries is settled separately, field by
// field, by agreedClaim.
func claimKey(c *core.Claim) string {
	if c == nil {
		return ""
	}
	return string(c.Origin) + "\x00" + c.Author
}

// leaves folds everything hanging off one box by a single line.
//
// The shape of the drawing is what a reader follows, and a leaf is not part of
// it: a config map attached to one workload adds a box and a line and no path
// through the picture. Folding them per neighbour and per type keeps the one
// thing they say — this workload has seven of these — and gives back the room.
func leaves(g *core.Graph, axis string, keep map[string]bool) []Folded {
	neighbours := map[string]map[string]bool{}
	for _, e := range g.Edges {
		if e.From == e.To {
			continue
		}
		if neighbours[e.From] == nil {
			neighbours[e.From] = map[string]bool{}
		}
		if neighbours[e.To] == nil {
			neighbours[e.To] = map[string]bool{}
		}
		neighbours[e.From][e.To] = true
		neighbours[e.To][e.From] = true
	}

	type key struct{ host, cohort string }
	groups := map[key][]string{}
	for _, n := range g.Nodes {
		if keep[n.ID] || strings.HasPrefix(n.ID, foldPrefix) {
			continue
		}
		around := neighbours[n.ID]
		if len(around) != 1 {
			continue
		}
		var host string
		for id := range around {
			host = id
		}
		// The same cohort as twins, not merely the same type: two config maps
		// in different namespaces, or from different providers, are not one
		// box however alike they look from the host they hang off.
		groups[key{host, cohortKey(n, axis)}] = append(groups[key{host, cohortKey(n, axis)}], n.ID)
	}

	var out []Folded
	for _, ids := range groups {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		out = append(out, Folded{
			Kind: FoldLeaves, Stands: foldID(FoldLeaves, ids[0]), Members: ids,
			Label: countLabel(commonName(g, ids), len(ids)),
		})
	}
	return out
}

// chains folds a run of boxes that pass something along with nothing else
// attached.
//
// A queue feeding a worker feeding a bucket is three boxes and two lines that
// say one thing: this goes there. The ends are where a reader joins the story,
// so they stay; the middle becomes one box that says how far it is.
func chains(g *core.Graph, keep map[string]bool) []Folded {
	out, in := map[string][]string{}, map[string][]string{}
	for _, e := range g.Edges {
		if e.From == e.To {
			continue
		}
		out[e.From] = append(out[e.From], e.To)
		in[e.To] = append(in[e.To], e.From)
	}
	passthrough := func(id string) bool {
		if keep[id] || strings.HasPrefix(id, foldPrefix) {
			return false
		}
		return len(dedupe(out[id])) == 1 && len(dedupe(in[id])) == 1 &&
			len(out[id]) == 1 && len(in[id]) == 1 && dedupe(out[id])[0] != dedupe(in[id])[0]
	}

	seen := map[string]bool{}
	var folds []Folded
	ids := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		ids = append(ids, n.ID)
	}
	sort.Strings(ids)

	for _, id := range ids {
		if seen[id] || !passthrough(id) {
			continue
		}
		// Walk to the start of the run so the same chain is found once
		// wherever it is entered.
		start := id
		for {
			previous := dedupe(in[start])[0]
			if !passthrough(previous) || previous == id {
				break
			}
			start = previous
		}
		run := []string{}
		for at := start; passthrough(at); at = dedupe(out[at])[0] {
			if seen[at] {
				break
			}
			seen[at] = true
			run = append(run, at)
		}
		if len(run) < 2 {
			continue
		}
		sorted := append([]string(nil), run...)
		sort.Strings(sorted)
		folds = append(folds, Folded{
			Kind: FoldChain, Stands: foldID(FoldChain, sorted[0]), Members: sorted,
			Label: fmt.Sprintf("%d hops", len(run)),
		})
	}
	return folds
}

// foldPrefix marks an id this package made up, so a second pass does not fold
// a box that already stands for others into a third thing.
const foldPrefix = "fold:"

func foldID(kind, first string) string { return foldPrefix + kind + ":" + first }

// IsFold reports whether an id names a box that stands for several.
func IsFold(id string) bool { return strings.HasPrefix(id, foldPrefix) }

func countLabel(name string, n int) string { return fmt.Sprintf("%s ×%d", name, n) }

// commonName is what to call a group of boxes: the part of their names they
// agree on, or their type when they agree on nothing.
func commonName(g *core.Graph, ids []string) string {
	var names []string
	kind := ""
	for _, id := range ids {
		n, ok := g.Node(id)
		if !ok {
			continue
		}
		names = append(names, n.Name)
		kind = n.Type
	}
	if len(names) == 0 {
		return kind
	}
	// Trimmed a character at a time rather than a byte at a time. Cutting a
	// multi-byte character in half leaves bytes that are not text: the prefix
	// check is a byte comparison and matches happily, the length check counts
	// each broken byte as a character and passes, and what reaches the label
	// is mojibake.
	prefix := []rune(names[0])
	for _, name := range names[1:] {
		for len(prefix) > 0 && !strings.HasPrefix(name, string(prefix)) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	trimmed := strings.TrimRight(string(prefix), "-_. ")
	if len([]rune(trimmed)) < 2 {
		return kind
	}
	return trimmed
}
