package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

const checkoutSurface = `openapi: 3.0.3
info:
  title: Checkout
  version: 2.1.0
paths:
  /orders:
    get:
      operationId: listOrders
    post:
      operationId: placeOrder
`

func surfaceFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The estate one document was read into: a service, so there is something for
// the surface to belong to.
func estateFile(t *testing.T) string {
	t.Helper()
	g := core.New()
	g.Nodes = []core.Node{{ID: "service/shop/checkout", Type: "service", Name: "checkout"}}
	g.Normalize()
	return graphFile(t, g)
}

func graphOf(t *testing.T, raw string) *core.Graph {
	t.Helper()
	var g core.Graph
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		t.Fatalf("the output is not a graph: %v", err)
	}
	return &g
}

// The surface arrives attached to the element somebody named, and the
// operations are boxes of their own.
func TestASurfaceIsReadIntoTheEstate(t *testing.T) {
	r := mustRun(t, "", "graph", estateFile(t),
		"--api", "service/shop/checkout="+surfaceFile(t, checkoutSurface))

	g := graphOf(t, r.stdout)
	if _, ok := g.Node("api/checkout/get/orders"); !ok {
		t.Fatalf("the operations are not here: %#v", g.Nodes)
	}
	declared := 0
	for _, e := range g.Edges {
		if e.From == "service/shop/checkout" && e.Relation == "declares" {
			declared++
		}
	}
	if declared != 2 {
		t.Errorf("%d operations are declared by the service, want 2", declared)
	}
	if !strings.Contains(r.stderr, "Checkout 2.1.0, 2 operations") {
		t.Errorf("the run does not say what it read: %q", r.stderr)
	}
}

// An id that names nothing is a mistake worth stopping for. Adding the
// operations anyway and dropping the join would answer "whose API is this"
// with silence, which is the question the flag was answering.
func TestAnOwnerThatNamesNothingIsAnError(t *testing.T) {
	r := run(t, "", "graph", estateFile(t),
		"--api", "service/shop/checkuot="+surfaceFile(t, checkoutSurface))

	if r.code == 0 {
		t.Fatal("a surface was attached to something that is not here")
	}
	if !strings.Contains(r.stderr, "service/shop/checkuot") {
		t.Errorf("the error does not name the id that is not here: %q", r.stderr)
	}
}

// Without an owner the operations still arrive. A surface read on its own is
// a listing, and the graph says so by joining it to nothing.
func TestASurfaceCanBeReadWithoutSayingWhoseItIs(t *testing.T) {
	r := mustRun(t, "", "graph", estateFile(t), "--api", surfaceFile(t, checkoutSurface))

	g := graphOf(t, r.stdout)
	if _, ok := g.Node("api/checkout/post/orders"); !ok {
		t.Fatalf("the operations are not here: %#v", g.Nodes)
	}
	for _, e := range g.Edges {
		if e.Relation == "declares" {
			t.Errorf("nobody said whose this is, and it was joined anyway: %#v", e)
		}
	}
}

// Handing the document over as the input is a reasonable thing to try, and
// being told it has no apiVersion sends somebody looking for a mistake in a
// file that has none.
func TestASurfaceHandedOverAsTheInputSaysWhereItGoes(t *testing.T) {
	r := run(t, "", "graph", surfaceFile(t, checkoutSurface))
	if r.code == 0 {
		t.Fatal("a surface was read as an estate")
	}
	if !strings.Contains(r.stderr, "--api") {
		t.Errorf("the error does not say where the document goes: %q", r.stderr)
	}
}

// Two documents with one title are two surfaces the estate cannot tell apart.
// Putting their operations in one container would answer "which API is this"
// with a box holding two of them.
func TestTwoSurfacesWithOneTitleAreRefused(t *testing.T) {
	first := surfaceFile(t, checkoutSurface)
	second := surfaceFile(t, strings.Replace(checkoutSurface, "/orders", "/baskets", 1))

	r := run(t, "", "graph", estateFile(t), "--api", first, "--api", second)
	if r.code == 0 {
		t.Fatal("two surfaces with one title were merged")
	}
	if !strings.Contains(r.stderr, "Checkout") {
		t.Errorf("the error does not name the title they share: %q", r.stderr)
	}
}

// Nodes and containers are one namespace and an edge may point at either, so
// a namespace owns a surface as well as a service does. Telling somebody who
// wrote down a container's id that nothing here has it sends them looking for
// a mistake they did not make.
func TestAContainerCanOwnASurface(t *testing.T) {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork, Label: "Network"}}
	g.Groups = []core.Group{{ID: "ns-shop", Axis: core.AxisNetwork, Type: "namespace", Label: "shop"}}
	g.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, g),
		"--api", "ns-shop="+surfaceFile(t, checkoutSurface))

	declared := 0
	for _, e := range graphOf(t, r.stdout).Edges {
		if e.From == "ns-shop" && e.Relation == "declares" {
			declared++
		}
	}
	if declared != 2 {
		t.Errorf("%d operations are declared by the container, want 2", declared)
	}
}

// The owner is what comes before the `=`, and a leading `=` says the owner is
// nothing. Without that there is no way to write down a path that has an `=`
// in it: it is read as a document belonging to something called `a`.
func TestAPathWithAnEqualsSignCanBeWrittenDown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a=b.yaml")
	if err := os.WriteFile(path, []byte(checkoutSurface), 0o600); err != nil {
		t.Fatal(err)
	}

	r := mustRun(t, "", "graph", estateFile(t), "--api", "="+path)
	if _, ok := graphOf(t, r.stdout).Node("api/checkout/get/orders"); !ok {
		t.Error("the document was not read")
	}
}

// An owner with no document after it is a flag half written. Reading "" and
// reporting that it could not be opened names neither the flag nor the
// mistake.
func TestAnOwnerWithNoDocumentSaysSo(t *testing.T) {
	r := run(t, "", "graph", estateFile(t), "--api", "service/shop/checkout=")
	if r.code == 0 {
		t.Fatal("a flag with no document was read")
	}
	if !strings.Contains(r.stderr, "--api") {
		t.Errorf("the error does not say which flag it is about: %q", r.stderr)
	}
}
