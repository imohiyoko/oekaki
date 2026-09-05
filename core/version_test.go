package core

import (
	"strings"
	"testing"
)

// 0.6 adds paths and changes nothing else, so a 0.5 document is already the
// right shape — but it declares a different contract, and a build that read it
// without saying so would be claiming the document promised something it did
// not.
func TestAVersion05DocumentIsReadAndRestamped(t *testing.T) {
	const doc = `{"version":"0.5","axes":[],"nodes":[{"id":"a","type":"service","name":"a"},` +
		`{"id":"b","type":"service","name":"b"}],` +
		`"edges":[{"from":"a","to":"b","kind":"observed","relation":"calls"}],"groups":[]}`

	g, err := Decode(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("a 0.5 document could not be read: %v", err)
	}
	if g.Version != Version {
		t.Fatalf("came back as %q, want %q", g.Version, Version)
	}
	if len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Fatalf("the document lost something on the way through: %d nodes, %d edges", len(g.Nodes), len(g.Edges))
	}
}

// The old contract is applied to the old document. Reading a broken 0.5
// document against the 0.6 schema would let something that was invalid then
// pass now, and the migration would launder it on the way through.
func TestABroken05DocumentIsStillBroken(t *testing.T) {
	const doc = `{"version":"0.5","axes":[],"nodes":[{"id":"a","name":"a"}],"edges":[],"groups":[]}`

	_, err := Decode(strings.NewReader(doc))
	if err == nil {
		t.Fatal("a 0.5 document missing a node type was accepted")
	}
	if !strings.Contains(err.Error(), "0.5") {
		t.Fatalf("the error does not say which contract it was judged by: %v", err)
	}
}

// A 0.5 document cannot carry routes: the version that has them is the one
// that describes them, and every published schema refuses what it does not
// declare.
func TestAVersion05DocumentMayNotCarryRoutes(t *testing.T) {
	const doc = `{"version":"0.5","axes":[],"nodes":[{"id":"a","type":"service","name":"a"},` +
		`{"id":"b","type":"service","name":"b"}],"edges":[],"groups":[],` +
		`"paths":[{"nodes":["a","b"],"kind":"observed"}]}`

	if _, err := Decode(strings.NewReader(doc)); err == nil {
		t.Fatal("a 0.5 document carrying routes was accepted")
	}
}

// And a version this build has never heard of is refused with the message that
// says what to do about it, rather than a schema violation about a constant.
func TestAnUnknownVersionSaysWhatToDo(t *testing.T) {
	const doc = `{"version":"9.9","axes":[],"nodes":[],"edges":[],"groups":[]}`

	_, err := Decode(strings.NewReader(doc))
	if err == nil || !strings.Contains(err.Error(), "regenerate it from its source") {
		t.Fatalf("error = %v", err)
	}
}
