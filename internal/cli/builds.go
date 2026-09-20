package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imohiyoko/oekaki/collectors/builds"
	"github.com/imohiyoko/oekaki/core"
	buildsenricher "github.com/imohiyoko/oekaki/enrichers/builds"
	"github.com/imohiyoko/oekaki/views"
)

type buildFlags struct {
	files        stringList
	repositories stringList
	refuse       bool
}

func (b *buildFlags) register(fs *flag.FlagSet) {
	fs.Var(&b.files, "builds", "apply a CI build record, joining a running image to the repository that built it; repeatable, - reads standard input")
	fs.Var(&b.repositories, "build-repo", "which part of this graph a repository is: an input, as `--build-repo acme/checkout=repo-2-checkout`, or one element of it; repeatable")
	fs.BoolVar(&b.refuse, "no-builds", false, "ignore --builds and --build-repo, however they were passed")
}

// applyBuilds reads build records and joins what is running to what built it.
//
// Reading is the entry point rather than a default, and --no-builds is the
// other end of the same decision: whether a CI system belongs in the picture
// at all is the estate's to make, and the flag exists because the command is
// often assembled by a wrapper — an action.yml, a Makefile — that passes
// --builds unconditionally. Refusing then has to be sayable downstream of
// whoever wrote the wrapper.
func applyBuilds(env Env, g *core.Graph, f buildFlags) error {
	if f.refuse {
		if len(f.files) > 0 || len(f.repositories) > 0 {
			fmt.Fprintln(env.Stderr, "builds: --no-builds, so the build records were not read")
		}
		return nil
	}
	if len(f.files) == 0 {
		if len(f.repositories) > 0 {
			return fmt.Errorf("--build-repo says which part of this graph a repository is, but there are no --builds records to say what it built")
		}
		return nil
	}

	docs := make([]*builds.Document, 0, len(f.files))
	built := map[string]bool{}
	for _, path := range f.files {
		raw, err := readInput(env, path)
		if err != nil {
			return err
		}
		doc, err := builds.Parse(raw, displayName(path))
		if err != nil {
			return err
		}
		for _, b := range doc.Builds {
			built[b.Repository] = true
		}
		docs = append(docs, doc)
	}

	inputs := buildsenricher.InputIDs(g)
	repositories := map[string]string{}
	for _, value := range f.repositories {
		repository, id, found := strings.Cut(value, "=")
		repository, id = strings.TrimSpace(repository), strings.TrimSpace(id)
		if !found || repository == "" || id == "" {
			return fmt.Errorf("--build-repo %s: write it as repository=id, such as acme/checkout=repo-1-checkout:source:dir:cmd", value)
		}
		// An input is the useful thing to name: it is the whole repository,
		// and naming it is what lets the drawing open that repository's code
		// behind the box. One element of it is still accepted, for an estate
		// that would rather point the edge at something it already draws.
		//
		// The same refusal --api makes either way: an id that names nothing is
		// a join somebody meant to make and did not, and letting it pass
		// quietly leaves the record looking applied.
		switch {
		case inputs[id]:
			// Whether that input has a code map to open is asked later, by
			// checkCodeMaps, because a line can still be denied after this
			// point.
		case element(g, id):
		default:
			return fmt.Errorf("--build-repo %s: nothing here is %q — not an input, not a node, not a group", value, id)
		}
		// And the other half of the same sentence. A repository no record
		// mentions is a mapping that will never be consulted: the run still
		// joins, to a repository node invented under the name somebody was
		// trying to override, and nothing would have said so.
		if !built[repository] {
			return fmt.Errorf("--build-repo %s: no record here says %s built anything", value, repository)
		}
		if was, ok := repositories[repository]; ok && was != id {
			return fmt.Errorf("--build-repo %s: %s was already said to be %q", value, repository, was)
		}
		repositories[repository] = id
	}

	report, err := buildsenricher.Enricher{Documents: docs, Repositories: repositories}.Enrich(g)
	if report != nil {
		report.WriteText(env.Stderr)
	}
	if err != nil {
		return err
	}
	return g.Validate()
}

// checkCodeMaps asks, of each repository placed at an input, whether there is
// still a code map to open there.
//
// After the overlays rather than beside the mapping, because that is the first
// moment the answer is settled: an overlay may suppress the only line an input
// had, and a check that ran before it passed a mapping whose box then opened
// onto nothing. Asked of the drawing rather than answered again here, because
// two readings of "this input has code" that differ is exactly how a mapping
// passes every check and then draws nothing.
//
// Not the last word, though: `--view`, `--root`, `--depth` and folding come
// later still and can leave the page with nothing on it. Those are a reader
// narrowing what they want to look at, and refusing to write a graph because
// one of this run's views is narrow would be answering a different question
// from the one asked here, which is whether the estate holds the code the
// mapping names.
func checkCodeMaps(g *core.Graph, f buildFlags) error {
	if f.refuse || len(f.files) == 0 {
		return nil
	}
	inputs := buildsenricher.InputIDs(g)
	for _, value := range f.repositories {
		repository, id, found := strings.Cut(value, "=")
		repository, id = strings.TrimSpace(repository), strings.TrimSpace(id)
		if !found || !inputs[id] {
			continue
		}
		if len(views.CodeOf(g, id)) == 0 {
			return fmt.Errorf("--build-repo %s: %q is here, but there is no code map to draw from it — a repository is named as an input so that its code can be opened", value, id)
		}
		// And that it reached the repository. A box answers about the code
		// inside the input it came from, so a mapping naming an input outside
		// that one is not about it and is not written there — which is right,
		// and silent: the run said the mapping was applied while the box went
		// on opening onto whatever it opened onto before. A repository nothing
		// here runs has no box at all, and that is the ordinary case the
		// report already speaks about.
		landed, boxes := false, 0
		for _, n := range g.Nodes {
			if n.Type != buildsenricher.NodeRepository || n.Name != repository {
				continue
			}
			boxes++
			if of, _ := n.Attrs[buildsenricher.AttrCodeInput].(string); of == id {
				landed = true
			}
		}
		if boxes > 0 && !landed {
			was := "box"
			if boxes > 1 {
				was = "boxes"
			}
			return fmt.Errorf("--build-repo %s: %s is here as %d %s, and none of them is inside %q — the mapping was read and changed nothing",
				value, repository, boxes, was, id)
		}
	}
	return nil
}
