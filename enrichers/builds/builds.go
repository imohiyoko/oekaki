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
	"github.com/imohiyoko/oekaki/collectors/builds"
	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

// Relation is what the edge says: this element runs what that repository built.
const Relation = "built_from"

// NodeRepository is the type of a node standing for a repository.
const NodeRepository = "repository"

// Enricher applies build records to a graph.
type Enricher struct {
	Documents []*builds.Document

	// Repositories says which element in this graph is which repository, for
	// the repositories somebody has written down. A repository not named here
	// becomes a node of its own: the record says it exists and built this, and
	// that much is known without anybody deciding where it sits in the estate.
	//
	// An id here must name something. It comes from whoever called this rather
	// than from the evidence, so an id that names nothing is their mistake to
	// hear about before the run starts — the command line checks it, and a
	// graph left pointing at a box that is not there fails validation.
	Repositories map[string]string
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
		matched[b.image.Reference] = true
		to, invented := e.target(g, b)
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
		if key == b.image.Reference && !matched[key] {
			r.Unmatched = append(r.Unmatched, enrichers.Unmatched{
				Selector: map[string]string{"image": b.image.Reference},
				Assert:   "build",
				Reason:   "nothing here runs it (built by " + b.repository + ")",
				Action:   "reported",
			})
		}
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
	contested := map[string]map[string]bool{}

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
						if contested[key] == nil {
							contested[key] = map[string]bool{seen.repository: true}
						}
						contested[key][this.repository] = true
					case this.run.Later(seen.run):
						byKey[key] = this
					}
				}
			}
		}
	}

	out := make(map[string][]string, len(contested))
	for key, repositories := range contested {
		delete(byKey, key)
		names := make([]string, 0, len(repositories))
		for name := range repositories {
			names = append(names, name)
		}
		out[key] = names
	}

	return byKey, out
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
func (e Enricher) target(g *core.Graph, b built) (string, bool) {
	if id, ok := e.Repositories[b.repository]; ok {
		return id, false
	}

	id := NodeRepository + ":" + b.repository
	if _, ok := g.Node(id); ok {
		return id, false
	}
	g.Nodes = append(g.Nodes, core.Node{
		ID: id, Type: NodeRepository, Name: b.repository,
		Claim: &core.Claim{Origin: core.OriginParser, Note: b.run.Label()},
	})
	return id, true
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
