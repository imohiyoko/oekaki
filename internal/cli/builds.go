package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imohiyoko/oekaki/collectors/builds"
	"github.com/imohiyoko/oekaki/core"
	buildsenricher "github.com/imohiyoko/oekaki/enrichers/builds"
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
			return fmt.Errorf("--build-repo says which element a repository is, but there are no --builds records to say what it built")
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
		if !input(g, id) && !element(g, id) {
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

	report, err := buildsenricher.Enricher{Documents: docs, Repositories: repositories, Inputs: inputIDs(g)}.Enrich(g)
	if report != nil {
		report.WriteText(env.Stderr)
	}
	if err != nil {
		return err
	}
	return g.Validate()
}

// input reports whether an id names one of the documents this graph was read
// from, which is how a whole repository is named.
func input(g *core.Graph, id string) bool {
	return inputIDs(g)[id]
}

func inputIDs(g *core.Graph) map[string]bool {
	out := map[string]bool{}
	if g.Metadata == nil {
		return out
	}
	for _, in := range g.Metadata.Inputs {
		out[in.ID] = true
	}
	return out
}
