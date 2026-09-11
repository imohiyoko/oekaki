package cli

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/parsers/openapi"
)

// applyAPIs reads OpenAPI documents and adds the operations they declare.
//
// The flag carries who the surface belongs to, because the document does not:
//
//	--api service/shop/checkout=openapi.yaml
//
// A document names itself with a title somebody wrote in a repository, and an
// estate names its boxes with ids. Matching the two by how alike they look is
// exactly the invention this program exists to avoid, so the id is written
// down by whoever knows it. Without one the operations are still added — a
// surface read on its own is a listing — and nothing is joined to them.
func applyAPIs(env Env, g *core.Graph, values stringList) error {
	if len(values) == 0 {
		return nil
	}
	for _, value := range values {
		of, path := splitAPI(value)
		if of != "" && !element(g, of) {
			return fmt.Errorf("--api %s: nothing here has the id %q, so there is nobody to declare these operations", value, of)
		}
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("--api %s: nothing after the = to read", value)
		}
		raw, err := readInput(env, path)
		if err != nil {
			return err
		}
		file := path
		if file == "-" {
			file = ""
		}
		res, err := openapi.Parse(raw, openapi.Options{File: file})
		if err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		if err := mergeAPI(g, res.Graph); err != nil {
			return fmt.Errorf("%s: %w", displayName(path), err)
		}
		g.Edges = append(g.Edges, openapi.Declare(res.Graph, of)...)

		fmt.Fprintf(env.Stderr, "openapi: %s", res.Title)
		if res.Version != "" {
			fmt.Fprintf(env.Stderr, " %s", res.Version)
		}
		fmt.Fprintf(env.Stderr, ", %d operations", res.Operations)
		if of != "" {
			fmt.Fprintf(env.Stderr, ", declared by %s", of)
		}
		fmt.Fprintln(env.Stderr)
		if len(res.Skipped) > 0 {
			fmt.Fprintf(env.Stderr, "  %d paths held no operation: %s\n", len(res.Skipped), summarise(res.Skipped))
		}
	}
	return g.Validate()
}

// looksLikeAPI reports whether an input is an OpenAPI document.
//
// It is asked only when the input has already failed to be anything else, and
// only to improve the error. A document handed over as the input is a
// reasonable thing to try — it is a file describing a service — and being told
// it has no apiVersion sends somebody looking for a mistake in a file that has
// none.
func looksLikeAPI(raw []byte) bool {
	var doc struct {
		OpenAPI string `yaml:"openapi"`
		Swagger string `yaml:"swagger"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return false
	}
	return doc.OpenAPI != "" || doc.Swagger != ""
}

// element reports whether an id names something in the graph.
//
// Nodes and containers are one namespace and an edge may point at either, so
// a namespace or a cluster owns a surface as well as a service does. Looking
// only at nodes tells somebody who wrote down a container's id that it is not
// here, which is a thing to go and check that is not wrong.
func element(g *core.Graph, id string) bool {
	if _, ok := g.Node(id); ok {
		return true
	}
	_, ok := g.Group(id)
	return ok
}

// splitAPI takes the owner off the front of a --api value. A value with no
// `=` is a path and nothing else, which is how a surface is read without
// saying whose it is.
//
// A leading `=` says the same for a path that has an `=` in it. The owner is
// what comes before, and nothing is what it says: a file called `a=b.yaml`
// would otherwise be read as a document belonging to something called `a`,
// with no way to write it down otherwise.
func splitAPI(value string) (of, path string) {
	before, after, found := strings.Cut(value, "=")
	if !found {
		return "", value
	}
	if strings.TrimSpace(before) == "" {
		return "", after
	}
	return strings.TrimSpace(before), after
}

// mergeAPI folds a surface into the graph it belongs to.
//
// A collision is an error rather than a merge. Two documents with one title
// are two surfaces the estate cannot tell apart, and quietly putting their
// operations in one container would answer "which API is this" with a box
// holding two of them.
func mergeAPI(g *core.Graph, surface *core.Graph) error {
	for _, a := range surface.Axes {
		if !g.HasAxis(a.ID) {
			g.Axes = append(g.Axes, a)
		}
	}
	for _, grp := range surface.Groups {
		if _, ok := g.Group(grp.ID); ok {
			return fmt.Errorf("%q is already here: two surfaces with one title cannot be told apart", grp.Label)
		}
		g.Groups = append(g.Groups, grp)
	}
	for _, n := range surface.Nodes {
		if _, ok := g.Node(n.ID); ok {
			return fmt.Errorf("%q is already here", n.Name)
		}
		g.Nodes = append(g.Nodes, n)
	}
	g.Edges = append(g.Edges, surface.Edges...)
	return nil
}
