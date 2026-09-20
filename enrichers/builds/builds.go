// Package builds joins a running container to the repository it was built
// from, on the strength of a record the CI system wrote.
//
// The join is made on the image reference, which is the one identifier both
// halves actually share: the IaC says which image a workload runs, and the
// build record says which repository produced that image. Nothing here
// compares a repository name to an image name, strips a tag, or completes a
// registry — that is the invention this program refuses, and an estate where
// `checkout` the repository does not build `checkout` the image is exactly
// where the guess would be both plausible and wrong.
package builds

import (
	"fmt"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/collectors/builds"
	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

// Relation is what the edge says: this element runs what that repository built.
const Relation = "built_from"

// NodeRepository is the type of a node standing for a repository.
const NodeRepository = "repository"

// AttrCodeInput is the input a repository's code was read from, on the
// repository node, when somebody said which one it is.
//
// Deliberately not `repository`: that attribute is already how a combined
// graph records which input each node came from, and two meanings on one key
// means whichever was written last wins.
const AttrCodeInput = "code_input"

// Enricher applies build records to a graph.
type Enricher struct {
	Documents []*builds.Document

	// Repositories says which element in this graph is which repository, for
	// the repositories somebody has written down. A repository not named here
	// becomes a node of its own: the record says it exists and built this, and
	// that much is known without anybody deciding where it sits in the estate.
	//
	// An id may name one of the graph's inputs — a whole repository — in
	// which case the edge still points at a node for the repository, and that
	// node records which input it is so a drawing can open its code.
	//
	// An id here must name something. It comes from whoever called this rather
	// than from the evidence, so an id that names nothing is their mistake to
	// hear about before the run starts — the command line checks it, and a
	// graph left pointing at a box that is not there fails validation.
	Repositories map[string]string
}

// InputIDs are the ids of the documents a graph was read from, which is how a
// mapping that names a whole repository is told from one that names an element.
//
// Read from the graph rather than handed in. It used to be a field, and a
// field of this shape has no unset state: a caller who forgot it turned every
// mapping into an element mapping, pointed the edge at an id no node wears,
// and erased what the last run had recorded — three wrong answers from one
// omission, none of which look like an omission. The graph already carries
// this, so nobody has to remember to say it.
func InputIDs(g *core.Graph) map[string]bool {
	out := map[string]bool{}
	if g == nil || g.Metadata == nil {
		return out
	}
	for _, in := range g.Metadata.Inputs {
		out[in.ID] = true
	}
	return out
}

func (Enricher) Name() string { return "builds" }

// built is one image, resolved to the build that produced it.
type built struct {
	repository string
	commit     string
	ref        string
	image      builds.Image
	run        builds.Run
}

func (e Enricher) Enrich(g *core.Graph) (*enrichers.Report, error) {
	r := &enrichers.Report{Enricher: e.Name()}
	for _, d := range e.Documents {
		if d.Source != "" {
			r.Sources = append(r.Sources, d.Source)
		}
	}

	byKey, contested := e.resolve()
	for key, repositories := range contested {
		r.Ambiguous = append(r.Ambiguous, enrichers.Ambiguous{
			Selector:   map[string]string{"image": key},
			Assert:     "build",
			Candidates: repositories,
		})
	}

	// What this run was told about a repository is written on it, whether or
	// not anything here happens to be running an image these records name.
	//
	// Doing it where the join happens meant an estate that had moved on to a
	// tag no record covers kept the answer the last run wrote: the mapping was
	// accepted, the box stayed open, and what it opened onto was the input the
	// operator had just stopped naming. A mapping is a sentence about a
	// repository, not about what is running today.
	//
	// A repository this run says nothing about keeps what it was told before.
	// Not repeating a flag is not a retraction, and throwing away somebody's
	// answer because they did not say it twice is the same kind of quiet loss
	// this is fixing.
	//
	// Written only where this run is entitled to speak. Combining two outputs
	// leaves a box per input, each already carrying the right answer about the
	// code inside its own input, and a mapping names one input — so writing it
	// on every box that shares the name made the other boxes claim code that
	// lives somewhere else, and put the code they did have out of reach of
	// every page. An element mapping names no input at all, so it speaks only
	// for the boxes this run made.
	inputs := InputIDs(g)
	for repository, id := range e.Repositories {
		boxes := repositoriesNamed(g, repository)
		for _, n := range boxes {
			from, _ := n.Attrs["repository"].(string)
			if !inputs[id] {
				if from == "" {
					delete(n.Attrs, AttrCodeInput)
				}
				continue
			}
			// A box answers about the code inside its own input, so only a
			// mapping naming something inside that input is about it. Whether
			// it has answered yet makes no difference: a box of input B taking
			// A's answer puts B's own code out of reach of every page, and it
			// does that just as thoroughly when B had said nothing.
			//
			// Unless it is the only box there is, in which case there is
			// nothing to tell it apart from — the ordinary case of reading a
			// previous output back beside the repository it was missing.
			if len(boxes) > 1 && !within(id, from) {
				continue
			}
			if n.Attrs == nil {
				n.Attrs = map[string]any{}
			}
			n.Attrs[AttrCodeInput] = id
		}
	}

	matched := map[string]bool{}
	for _, n := range g.Nodes {
		image, ok := n.Attrs["image"].(string)
		if !ok || image == "" {
			continue
		}
		b, ok := lookup(byKey, image)
		if !ok {
			continue
		}
		matched[b.image.Identity()] = true
		to, invented, err := e.target(g, n, b)
		if err != nil {
			return r, err
		}
		if invented {
			// A box that was not in the estate a moment ago, said out loud.
			// An edge to an invented repository and an edge to parsed
			// infrastructure are indistinguishable in a drawing, and this is
			// the field that exists for telling them apart.
			r.Adopted = append(r.Adopted, to)
		}
		g.Edges = append(g.Edges, edge(n.ID, to, image, b))
		r.Applied++
	}

	// An image nothing here runs is the ordinary case for a record covering a
	// whole organisation, and it is still worth saying: it is the difference
	// between "this estate does not run that" and "the join silently found
	// nothing", which look identical in a drawing.
	for key, b := range byKey {
		// One entry per image rather than per key: an image is indexed under
		// its reference and under its digest, and an image nothing runs
		// should be reported once, not once per way of naming it.
		//
		// And not at all if the estate runs it under one of its other names.
		// One build pushing :1.4.0 and :latest at one digest, joined to a
		// workload that pins the digest, would otherwise be reported as
		// something nothing here runs — which is the opposite of true, in the
		// one line a reader is meant to act on.
		//
		// What settles that is the digest, not the tag they share. Two builds
		// of one tag at two digests are two images, and the estate running
		// the older one is not a reason to stay quiet about the newer.
		if key != b.image.Reference || matched[b.image.Identity()] {
			continue
		}
		r.Unmatched = append(r.Unmatched, enrichers.Unmatched{
			Selector: map[string]string{"image": b.image.Reference},
			Assert:   "build",
			Reason:   "nothing here runs it (built by " + b.repository + ")",
			Action:   "reported",
		})
	}

	g.Normalize()
	r.Sort()
	return r, nil
}

// resolve indexes every image a record mentions, by reference and by digest.
//
// Two builds of one image from one repository is ordinary — a tag gets rebuilt
// — and the later run wins. Two *repositories* claiming one image is not
// something to pick a winner for: one of them did not build it, and nothing
// here knows which.
func (e Enricher) resolve() (map[string]built, map[string][]string) {
	byKey := map[string]built{}
	contested := map[string]*clash{}

	for _, d := range e.Documents {
		for _, b := range d.Builds {
			for _, img := range b.Images {
				this := built{repository: b.Repository, commit: b.Commit, ref: b.Ref, image: img, run: b.Run}
				for _, key := range img.Keys() {
					seen, ok := byKey[key]
					switch {
					case !ok:
						byKey[key] = this
					case seen.repository != this.repository:
						c := contested[key]
						if c == nil {
							c = &clash{
								repositories: map[string]bool{seen.repository: true},
								references:   map[string]bool{seen.image.Reference: true},
							}
							contested[key] = c
						}
						c.repositories[this.repository] = true
						c.references[this.image.Reference] = true
					case this.run.Later(seen.run):
						byKey[key] = this
					}
				}
			}
		}
	}

	for key := range contested {
		delete(byKey, key)
	}

	out := make(map[string][]string, len(contested))
	for key, c := range contested {
		// The same conflict, said once. Two repositories claiming one tag at
		// one digest contest the reference and the digest both, and reporting
		// the digest as well would put a second line under a bare sha256:…
		// that reads as a second conflict somebody has to go and look into.
		if !c.references[key] && c.alsoSaidOf(contested) {
			continue
		}
		out[key] = sorted(c.repositories)
	}
	return byKey, out
}

// clash is one image two repositories both claim.
type clash struct {
	repositories map[string]bool
	references   map[string]bool
}

// alsoSaidOf reports whether one of this clash's references carries the same
// clash, which is where it is worth reading.
func (c *clash) alsoSaidOf(contested map[string]*clash) bool {
	for ref := range c.references {
		if other, ok := contested[ref]; ok && sameSet(c.repositories, other.repositories) {
			return true
		}
	}
	return false
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// within reports whether an input id lies inside the input a node came from.
//
// An empty scope is a node this run made, or one that came in at the top
// level: it is this run's to speak for, and every input is inside it. A node
// that arrived from an input answers only for what is inside that input, which
// after qualification is exactly the ids that scope prefixes.
func within(id, scope string) bool {
	return scope == "" || id == scope || strings.HasPrefix(id, scope+":")
}

// lookup finds the build behind what a node is running: the reference as
// written, or the digest it is pinned to.
func lookup(byKey map[string]built, image string) (built, bool) {
	if b, ok := byKey[image]; ok {
		return b, true
	}
	if digest, ok := builds.DigestOf(image); ok {
		if b, ok := byKey[digest]; ok {
			return b, true
		}
	}
	return built{}, false
}

// target is the element the edge points at, and whether this invented it: the
// one somebody wrote down, or a node for the repository itself.
func (e Enricher) target(g *core.Graph, running core.Node, b built) (string, bool, error) {
	inputs := InputIDs(g)
	// The repository this graph already holds, found by what it is rather than
	// by the id this run would give it.
	//
	// A graph that ran this once is an input the next time, and everything in
	// it arrives qualified with the scope it was read under — so the node is
	// no longer at `repository:<name>`, while the repository is the same
	// repository. Matching on the id alone missed it, and then invented a
	// second box for the same thing: two boxes for one repository, disagreeing
	// about whether it has code.
	// The box that speaks for the input the thing running it came from.
	// Several inputs combined leave a box apiece, all of them the same
	// repository and none of them the same box — and the first in id order is
	// a box some other input's workloads were joined to, so pointing here
	// draws a deployment built from two repositories.
	//
	// Speaks for, not equals. A box this enricher invented had no input
	// attribute, so reading that output back stamps it with the outer scope
	// alone while the workload beside it keeps a nested one. Demanding the two
	// be equal missed the box that was right there and invented a bare one
	// next to it, which is the doubling this matching was added to stop. The
	// most specific box that covers the workload wins; a box this run made
	// covers everything, and is the last resort rather than the first.
	from, _ := running.Attrs["repository"].(string)
	existing := repositoriesNamed(g, b.repository)
	var mine *core.Node
	for _, n := range existing {
		of, _ := n.Attrs["repository"].(string)
		if !within(from, of) {
			continue
		}
		if mine == nil {
			mine = n
			continue
		}
		if was, _ := mine.Attrs["repository"].(string); len(of) > len(was) {
			mine = n
		}
	}

	// The input this repository is, when somebody said so. It goes on the node
	// rather than on the edge because it is a fact about the repository and
	// not about this build.
	//
	// Under its own key rather than `repository`. That one already means
	// something else — combining inputs stamps every node with the input it
	// came from — so a repository node arriving inside a previous output had
	// this answer overwritten with the input it was read from, and the code
	// map then drew that whole input's code.
	of := ""
	if id, ok := e.Repositories[b.repository]; ok {
		if !inputs[id] {
			// Pointed at an element instead. Whatever an earlier run wrote on
			// the repository node was cleared before any of this, because it
			// has to happen whether or not a record matched anything.
			return id, false, nil
		}
		of = id
	}

	if mine != nil {
		// Already told what its code is, before any of this.
		return mine.ID, false, nil
	}

	id := NodeRepository + ":" + b.repository
	if n, ok := g.Node(id); ok {
		// Not a repository, then, and not this one: a different thing with the
		// same name. Pointing the edge at it would answer "what built this"
		// with somebody else's box, and nothing downstream could tell, because
		// the graph would still validate.
		return "", false, fmt.Errorf(
			"%q is already here as %s %q: that and the repository the record names cannot be told apart",
			id, n.Type, n.Name)
	}
	node := core.Node{
		ID: id, Type: NodeRepository, Name: b.repository,
		Claim: &core.Claim{Origin: core.OriginParser, Note: b.run.Label()},
	}
	if of != "" {
		node.Attrs = map[string]any{AttrCodeInput: of}
	}
	g.Nodes = append(g.Nodes, node)
	return id, true, nil
}

// repositoriesNamed are the nodes already standing for one repository,
// whatever id they are wearing, in a fixed order so that a graph holding more
// than one of them is read the same way twice.
func repositoriesNamed(g *core.Graph, name string) []*core.Node {
	var out []*core.Node
	for i := range g.Nodes {
		if g.Nodes[i].Type == NodeRepository && g.Nodes[i].Name == name {
			out = append(out, &g.Nodes[i])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func edge(from, to, image string, b built) core.Edge {
	attrs := map[string]any{"image": image}
	if b.commit != "" {
		attrs["commit"] = b.commit
	}
	if b.ref != "" {
		attrs["ref"] = b.ref
	}
	if b.image.Digest != "" {
		attrs["digest"] = b.image.Digest
	}
	if b.run.URL != "" {
		attrs["run_url"] = b.run.URL
	}
	return core.Edge{
		From: from, To: to, Kind: core.EdgeObserved, Relation: Relation, Attrs: attrs,
		Claim: &core.Claim{Origin: core.OriginParser, Note: b.run.Label()},
	}
}
