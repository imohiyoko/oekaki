// Package openapi reads an OpenAPI document as one service's surface.
//
// # What a document says, and what it does not
//
// An OpenAPI document declares which operations exist — `GET /orders/{id}` —
// and that is all it declares. It does not say what the service calls, so
// nothing here produces a route: routes come from the things that actually
// carry a request, which is [the Ingress and the overlay][paths].
//
// It also does not say which box in the estate it belongs to. One document
// describes one service, and the name it gives itself is a title somebody
// wrote in a repository, not an id in a cluster. So the join is not guessed
// here: Options.Of names the element whose surface this is, and somebody has
// to say it. Without it the operations still exist — a document read on its
// own is a listing of a surface — they are just not attached to anything.
//
// # Why an operation is a node
//
// Until now the graph could say a request came in on `shop.example.com/orders`
// and could not say that `GET /orders/{id}` exists. An entry names a way in;
// an operation is the thing on the other side of it, and a great many
// questions — which of these is deprecated, which are undocumented, which
// belong to this team — are about operations rather than about services.
//
// Matching one to the other is deliberately not done here. A route's entry is
// a rule's host and path, and whether a request belongs to a rule is decided
// by reading the rule, not by comparing strings.
//
// [paths]: ../../docs/paths.md
package openapi

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/imohiyoko/oekaki/core"
)

const (
	// NodeOperation is one operation: a method and a path.
	NodeOperation = "api"

	// GroupSurface is the container a document's operations sit in: one
	// document, one surface. It is the document's own claim about itself —
	// these operations are one API — and it holds whether or not anybody has
	// said which element in the estate serves them.
	GroupSurface = "api_surface"

	// RelationDeclares joins the element whose surface this is to an
	// operation on it. It is the same word the code graph uses for a method
	// on a type, and for the same reason: the operation is the thing, seen
	// from the side of whoever offers it.
	//
	// Nothing here draws one. The document does not say who serves it, so the
	// edge belongs to whoever does say — see Declare.
	RelationDeclares = "declares"

	// Axis is the axis surfaces group their operations on. It is its own axis
	// because a surface is not network containment: an operation is not in a
	// namespace, it is on a service that is.
	Axis = "api"
)

// methods are the fields of a path item that are operations. Everything else
// a path item may hold — parameters, servers, a summary, a $ref — describes
// the path rather than declaring an operation on it.
var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Options tunes a read.
type Options struct {
	// File names the document the surface came from, so operations can point
	// back at the line that declared them.
	File string
}

// Result reports what a read found, alongside the graph.
type Result struct {
	Graph *core.Graph

	// Title is what the document calls itself, and Version the version of the
	// API it describes — `info.version`, which is the API's own version and
	// not the version of the OpenAPI format.
	Title   string
	Version string

	// Operations is how many were read.
	Operations int

	// Skipped lists the paths that held no operation. A path item with
	// nothing but a `$ref` is the ordinary shape of a split document, and a
	// reader who gets fewer operations than they expected should be told
	// where they went rather than left to count.
	Skipped []string
}

// Parse reads an OpenAPI 3 document.
func Parse(raw []byte, opts Options) (*Result, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("reading the document: %w", err)
	}
	root := unwrap(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, errors.New("not an OpenAPI document: the top level is not a mapping")
	}

	if err := readable(root); err != nil {
		return nil, err
	}

	title := text(field(field(root, "info"), "title"))
	if title == "" {
		return nil, errors.New("the document does not name itself: info.title is what the surface is named after")
	}

	res := &Result{
		Graph:   core.New(),
		Title:   title,
		Version: text(field(field(root, "info"), "version")),
	}
	res.Graph.Metadata = &core.Metadata{Source: "openapi"}
	res.Graph.Axes = []core.Axis{{ID: Axis, Label: "API"}}

	surface := "api:" + slug(title)
	res.Graph.Groups = append(res.Graph.Groups, core.Group{
		ID:     surface,
		Axis:   Axis,
		Type:   GroupSurface,
		Label:  title,
		Attrs:  surfaceAttrs(root, res.Version),
		Source: source(opts.File, root.Line),
	})

	paths := field(root, "paths")
	if paths == nil || paths.Kind != yaml.MappingNode {
		return res, nil
	}

	for i := 0; i+1 < len(paths.Content); i += 2 {
		path, item := paths.Content[i].Value, paths.Content[i+1]
		if !strings.HasPrefix(path, "/") {
			// A key that is not a path is an extension (`x-...`) or a
			// mistake, and reading either as an endpoint would draw a box for
			// something the document never offered.
			continue
		}
		found := 0
		for _, method := range methods {
			key, op := entry(item, method)
			if op == nil {
				continue
			}
			found++
			res.Graph.Nodes = append(res.Graph.Nodes, core.Node{
				ID:          operationID(surface, method, path),
				Type:        NodeOperation,
				Name:        strings.ToUpper(method) + " " + path,
				Description: text(field(op, "summary")),
				Groups:      map[string]string{Axis: surface},
				Attrs:       operationAttrs(op, method, path),
				Source:      source(opts.File, key.Line),
			})
		}
		res.Operations += found
		if found == 0 {
			res.Skipped = append(res.Skipped, path)
		}
	}

	res.Graph.Normalize()
	return res, nil
}

// Declare returns the edges that say `of` serves this surface's operations.
//
// It is separate from Parse because it is a different claim by a different
// author. The document says which operations exist; somebody who knows the
// estate says whose they are, and a reading that quietly did both would be
// this program inventing the half it was not told.
func Declare(surface *core.Graph, of string) []core.Edge {
	if of == "" {
		return nil
	}
	var out []core.Edge
	for _, n := range surface.Nodes {
		if n.Type != NodeOperation {
			continue
		}
		out = append(out, core.Edge{
			From: of, To: n.ID, Kind: core.EdgeIACRef, Relation: RelationDeclares,
		})
	}
	return out
}

// readable refuses what this does not read, by name. A document that is not
// read at all is a better answer than a graph built from a format whose paths
// mean something else.
func readable(root *yaml.Node) error {
	version := text(field(root, "openapi"))
	if version == "" {
		if old := text(field(root, "swagger")); old != "" {
			return fmt.Errorf("swagger %s is not read here: its paths are relative to basePath, "+
				"so an operation read as written would be at a path the service does not serve", old)
		}
		return errors.New(`not an OpenAPI document: no "openapi" version at the top level`)
	}
	if !strings.HasPrefix(version, "3.") {
		return fmt.Errorf("openapi %s is not read here: this reads 3.x", version)
	}
	return nil
}

// surfaceAttrs records what the document says about itself as a whole.
//
// The servers are the URLs it says it is served at. They are recorded and not
// resolved against anything: a server URL and an Ingress host may well be the
// same estate said twice, and joining them here would be this program deciding
// that on the strength of two strings looking alike.
func surfaceAttrs(root *yaml.Node, version string) map[string]any {
	attrs := map[string]any{}
	if version != "" {
		attrs["api_version"] = version
	}
	var servers []string
	if list := field(root, "servers"); list != nil && list.Kind == yaml.SequenceNode {
		for _, s := range list.Content {
			if url := text(field(s, "url")); url != "" {
				servers = append(servers, url)
			}
		}
	}
	if len(servers) > 0 {
		attrs["servers"] = servers
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

// operationAttrs keeps the fields somebody filters on. The method and the path
// are kept apart from the name, because a listing that wants every DELETE, or
// everything under /admin, should not have to take the name back apart.
func operationAttrs(op *yaml.Node, method, path string) map[string]any {
	attrs := map[string]any{"method": strings.ToUpper(method), "path": path}
	if id := text(field(op, "operationId")); id != "" {
		attrs["operation_id"] = id
	}
	if deprecated := field(op, "deprecated"); says(deprecated) {
		attrs["deprecated"] = true
	}
	if tags := field(op, "tags"); tags != nil && tags.Kind == yaml.SequenceNode {
		var out []string
		for _, t := range tags.Content {
			if t.Value != "" {
				out = append(out, t.Value)
			}
		}
		sort.Strings(out)
		if len(out) > 0 {
			attrs["tags"] = out
		}
	}
	return attrs
}

// operationID names an operation by the two things that identify it, under
// the surface that declares it. The path's leading slash is the separator, so
// `get` and `/orders/{id}` become `get/orders/{id}` rather than growing a
// second one.
//
// The id says `api/` first because an id is read by people as well as by
// programs, and `checkout/get/orders` beside `service/shop/checkout` reads as
// something of a kind called checkout.
func operationID(surface, method, path string) string {
	return "api/" + strings.TrimPrefix(surface, "api:") + "/" + method + path
}

func source(file string, line int) *core.Source {
	if file == "" {
		return nil
	}
	return &core.Source{File: file, Line: line}
}

// slug turns a title into something usable in an id: an id is read in a URL,
// a fragment and a command line, and a title is written for a person.
//
// What it takes out is punctuation and space, not the alphabet. Keeping only
// the ASCII letters would leave 注文 with nothing at all, and would put two
// documents that share no character between them under one id — reported, if
// they are read together, as "two surfaces with one title", which is not what
// happened.
func slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	if out := strings.TrimSuffix(b.String(), "-"); out != "" {
		return out
	}
	// A title of nothing but punctuation still has to name this surface and
	// not every other one. There is nothing in it to read, so the id says so
	// rather than being empty and colliding with the next one.
	digest := sha256.Sum256([]byte(title))
	return fmt.Sprintf("untitled-%x", digest[:4])
}

// unwrap returns the mapping inside a decoded document.
func unwrap(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return n.Content[0]
	}
	return n
}

// field returns the value of a mapping key, or nil.
func field(n *yaml.Node, key string) *yaml.Node {
	_, value := entry(n, key)
	return value
}

// entry returns both halves of a mapping entry. The key is what carries the
// line an operation was declared on: the value's line is its first field,
// which for two operations written under one path is the same line twice.
func entry(n *yaml.Node, key string) (k, value *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}

// says reports a scalar the document wrote as true.
//
// `true`, `True` and `TRUE` are one value written three ways, and yaml keeps
// the word somebody typed: comparing that word to "true" makes two of the
// three mean the opposite of what they say, and an endpoint that was retired
// is drawn as a live one.
func says(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && strings.EqualFold(n.Value, "true")
}

// text returns a scalar's value, and "" for anything else — a mapping where a
// string was expected is not a title.
func text(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}
