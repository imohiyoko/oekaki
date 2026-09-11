package openapi

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func read(t *testing.T) *Result {
	t.Helper()
	raw, err := os.ReadFile("testdata/checkout.yaml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(raw, Options{File: "testdata/checkout.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func parse(t *testing.T, body string) (*Result, error) {
	t.Helper()
	return Parse([]byte(body), Options{})
}

func node(t *testing.T, g *core.Graph, id string) core.Node {
	t.Helper()
	n, ok := g.Node(id)
	if !ok {
		var have []string
		for _, n := range g.Nodes {
			have = append(have, n.ID)
		}
		t.Fatalf("no %s; there is %s", id, strings.Join(have, ", "))
	}
	return *n
}

// An operation is a node, which is the whole point of reading the document:
// the graph could say a request arrived on a host and a path, and could not
// say that this operation exists.
func TestAnOperationIsANode(t *testing.T) {
	res := read(t)

	n := node(t, res.Graph, "api/checkout/get/orders/{id}")
	if n.Type != NodeOperation || n.Name != "GET /orders/{id}" {
		t.Errorf("the operation is %s %q", n.Type, n.Name)
	}
	if n.Attrs["method"] != "GET" || n.Attrs["path"] != "/orders/{id}" {
		t.Errorf("the method and the path are not kept apart from the name: %#v", n.Attrs)
	}
	if n.Attrs["operation_id"] != "getOrder" {
		t.Errorf("the operationId is %#v", n.Attrs["operation_id"])
	}
	if res.Operations != 4 {
		t.Errorf("read %d operations, want the 4 the document declares", res.Operations)
	}
}

// The line an operation was declared on, not the line its path was. Two
// operations written under one path are two places in the file, and a link
// that landed on the path would be the same link twice.
func TestAnOperationPointsAtTheLineItWasDeclaredOn(t *testing.T) {
	res := read(t)

	get := node(t, res.Graph, "api/checkout/get/orders/{id}")
	del := node(t, res.Graph, "api/checkout/delete/orders/{id}")
	if get.Source == nil || del.Source == nil {
		t.Fatalf("an operation does not say where it came from")
	}
	if get.Source.Line == del.Source.Line {
		t.Errorf("two operations under one path point at one line: %d", get.Source.Line)
	}
}

// The surface is the document's own claim about itself — these operations are
// one API — and it holds whether or not anybody has said which element serves
// them.
func TestTheOperationsAreOneSurface(t *testing.T) {
	res := read(t)

	grp, ok := res.Graph.Group("api:checkout")
	if !ok {
		t.Fatalf("the surface is not a container: %#v", res.Graph.Groups)
	}
	if grp.Axis != Axis || grp.Label != "Checkout" {
		t.Errorf("the surface is %q on %q", grp.Label, grp.Axis)
	}
	servers, _ := grp.Attrs["servers"].([]string)
	if len(servers) != 1 || servers[0] != "https://shop.example.com" {
		t.Errorf("the servers are %q", servers)
	}
	for _, n := range res.Graph.Nodes {
		if n.Groups[Axis] != "api:checkout" {
			t.Errorf("%s is not on the surface: %#v", n.ID, n.Groups)
		}
	}
}

// A document does not say which box in the estate it is. Whoever knows says
// so, and until they do the operations are attached to nothing rather than to
// something that looked close enough.
func TestNothingIsJoinedUntilSomebodySaysWhoseItIs(t *testing.T) {
	surface := read(t).Graph
	if len(surface.Edges) != 0 || len(Declare(surface, "")) != 0 {
		t.Fatalf("a document nobody placed was joined to something: %#v", surface.Edges)
	}

	res := read(t)
	edges := Declare(res.Graph, "service/shop/checkout")
	if len(edges) != res.Operations {
		t.Fatalf("%d operations and %d edges", res.Operations, len(edges))
	}
	for _, e := range edges {
		if e.From != "service/shop/checkout" || e.Relation != RelationDeclares || e.Kind != core.EdgeIACRef {
			t.Errorf("the edge is %#v", e)
		}
	}
}

// What is only a description of the path is not an operation, and neither is
// an extension key. A box drawn for either would be a box for something the
// document never offered.
func TestOnlyAnOperationBecomesABox(t *testing.T) {
	res := read(t)

	for _, n := range res.Graph.Nodes {
		if strings.Contains(n.ID, "x-internal") {
			t.Errorf("an extension key was read as a path: %s", n.ID)
		}
	}
	if !slices.Contains(res.Skipped, "/health") {
		t.Errorf("a path that held no operation was not reported: %q", res.Skipped)
	}
}

// The fields somebody filters on: which of these is deprecated, which belong
// to this team.
func TestWhatAnOperationCarries(t *testing.T) {
	res := read(t)

	del := node(t, res.Graph, "api/checkout/delete/orders/{id}")
	if del.Attrs["deprecated"] != true {
		t.Errorf("the deprecated operation does not say so: %#v", del.Attrs)
	}
	get := node(t, res.Graph, "api/checkout/get/orders")
	if get.Description != "Every order this customer has placed" {
		t.Errorf("the summary is %q", get.Description)
	}
	tags, _ := get.Attrs["tags"].([]string)
	if len(tags) != 2 || tags[0] != "orders" || tags[1] != "reporting" {
		t.Errorf("the tags are %q, and two runs would have to agree on them", tags)
	}
	if _, said := node(t, res.Graph, "api/checkout/post/orders").Attrs["deprecated"]; said {
		t.Error("an operation that is not deprecated says it is not, where absent means nobody said")
	}
}

// A format this does not read is refused by name. A graph built from a
// document whose paths mean something else is worse than no graph: swagger 2.0
// writes its paths relative to basePath, so every operation would be at a path
// the service does not serve.
func TestWhatIsNotReadIsRefusedByName(t *testing.T) {
	for _, body := range []string{
		"swagger: \"2.0\"\ninfo:\n  title: Checkout\nbasePath: /v1\npaths: {}\n",
		"openapi: 2.0.0\ninfo:\n  title: Checkout\npaths: {}\n",
		"openapi: 3.0.3\npaths: {}\n",
		"- openapi: 3.0.3\n",
		"nothing to do with it\n",
	} {
		if _, err := parse(t, body); err == nil {
			t.Errorf("read a document it cannot read: %q", body)
		}
	}
}

// A document with no operations is still a surface. "This service declares
// nothing" is an answer, and an error there would report the tool's opinion
// rather than the document's content.
func TestASurfaceWithNoOperationsIsStillASurface(t *testing.T) {
	res, err := parse(t, "openapi: 3.0.3\ninfo:\n  title: Checkout\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Graph.Groups) != 1 || len(res.Graph.Nodes) != 0 {
		t.Errorf("%d surfaces and %d operations", len(res.Graph.Groups), len(res.Graph.Nodes))
	}
}

// The same document read twice is the same document. Everything downstream of
// this — diff above all — is only meaningful if that holds.
func TestTwoReadsAgree(t *testing.T) {
	first, err := read(t).Graph.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := read(t).Graph.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatal("two reads of one document disagree")
		}
	}
}

// A title is written in whatever alphabet its author writes in, and the id it
// becomes has to tell two of them apart. Keeping only the ASCII letters puts
// 注文 API and API under one id, and reports the second as a surface that
// shares the first one's title — which is not what happened.
func TestATitleKeepsTheLettersItIsWrittenIn(t *testing.T) {
	surface := func(title string) *Result {
		t.Helper()
		res, err := parse(t, "openapi: 3.0.3\ninfo:\n  title: "+title+"\npaths:\n  /orders:\n    get: {}\n")
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	if jp, en := surface("注文 API"), surface("API"); jp.Graph.Groups[0].ID == en.Graph.Groups[0].ID {
		t.Errorf("注文 API and API are one surface: %q", en.Graph.Groups[0].ID)
	}

	only := surface("注文")
	if id := only.Graph.Groups[0].ID; id == "api:" {
		t.Errorf("a title written in no ASCII at all left no id: %q", id)
	}
	if id := only.Graph.Nodes[0].ID; strings.Contains(id, "//") {
		t.Errorf("the surface left no name in the operation's id: %q", id)
	}
}

// A title with nothing in it to read still names this surface and not the
// next one. An id that is empty collides with every other empty one.
func TestATitleWithNoLettersStillNamesOneSurface(t *testing.T) {
	first, err := parse(t, "openapi: 3.0.3\ninfo:\n  title: \"***\"\npaths: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := parse(t, "openapi: 3.0.3\ninfo:\n  title: \"###\"\npaths: {}\n")
	if err != nil {
		t.Fatal(err)
	}

	if first.Graph.Groups[0].ID == second.Graph.Groups[0].ID {
		t.Errorf("two titles are one surface: %q", first.Graph.Groups[0].ID)
	}
	if first.Graph.Groups[0].ID == "api:" {
		t.Error("the surface has no id at all")
	}
}

// `true`, `True` and `TRUE` are one value written three ways, and yaml keeps
// the word somebody typed. Which of these is deprecated is one of the
// questions an operation is a node for, and two of the three ways of
// answering it must not come out as the opposite.
func TestDeprecatedIsReadHoweverItIsWritten(t *testing.T) {
	for _, written := range []string{"true", "True", "TRUE"} {
		res, err := parse(t, "openapi: 3.0.3\ninfo:\n  title: Checkout\npaths:\n  /orders:\n    get:\n      deprecated: "+written+"\n")
		if err != nil {
			t.Fatal(err)
		}
		if node(t, res.Graph, "api/checkout/get/orders").Attrs["deprecated"] != true {
			t.Errorf("`deprecated: %s` is read as a live endpoint", written)
		}
	}

	res, err := parse(t, "openapi: 3.0.3\ninfo:\n  title: Checkout\npaths:\n  /orders:\n    get:\n      deprecated: false\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, said := node(t, res.Graph, "api/checkout/get/orders").Attrs["deprecated"]; said {
		t.Error("an operation the document says is not deprecated carries the attribute anyway")
	}
}

// An alias is not a $ref. There is no second file to go and read: the
// document has already resolved it, and an operation written once and used
// twice is declared twice. Dropping the second loses it in silence, because
// the path it was under is not reported as having held no operation.
func TestAnAliasIsADeclarationTheDocumentAlreadyMade(t *testing.T) {
	res, err := parse(t, `openapi: 3.0.3
info:
  title: Checkout
paths:
  /orders:
    get: &listing
      operationId: listOrders
      tags: [orders]
  /baskets:
    get: *listing
`)
	if err != nil {
		t.Fatal(err)
	}

	n := node(t, res.Graph, "api/checkout/get/baskets")
	if n.Attrs["operation_id"] != "listOrders" {
		t.Errorf("the operation behind the alias arrived with nothing in it: %#v", n.Attrs)
	}
	if tags, _ := n.Attrs["tags"].([]string); len(tags) != 1 || tags[0] != "orders" {
		t.Errorf("the tags behind the alias are %q", tags)
	}
}

// `<<: *defaults` is the document saying these operations are here too. A
// reader that passed over it counts fewer operations than the document
// declares, and cannot say which ones — the path held the others, so it is
// not reported as skipped either.
func TestAMergeKeyDeclaresWhatItBringsIn(t *testing.T) {
	res, err := parse(t, `openapi: 3.0.3
info:
  title: Checkout
x-defaults: &defaults
  get:
    operationId: read
paths:
  /orders:
    <<: *defaults
    post:
      operationId: place
  /baskets:
    <<: *defaults
    get:
      operationId: readBaskets
`)
	if err != nil {
		t.Fatal(err)
	}

	if res.Operations != 3 {
		t.Fatalf("read %d operations, want the 3 the document declares: %#v", res.Operations, res.Skipped)
	}
	if slices.Contains(res.Skipped, "/orders") {
		t.Error("a path whose operations were dropped is not reported as holding none")
	}
	if node(t, res.Graph, "api/checkout/get/orders").Attrs["operation_id"] != "read" {
		t.Error("the merged operation did not arrive")
	}
	// What a mapping writes out wins over what it merges in, which is what a
	// merge key means.
	if got := node(t, res.Graph, "api/checkout/get/baskets").Attrs["operation_id"]; got != "readBaskets" {
		t.Errorf("the merged default beat what the path itself says: %#v", got)
	}
}
