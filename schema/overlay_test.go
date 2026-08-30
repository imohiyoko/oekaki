package schema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/schema"
)

// The overlay schema is the second contract this project publishes, and it is
// the one a model writes against. So it gets a corpus for the same reason the
// IR does: the files are what define the contract, and a case is added there
// rather than here.
//
// The invalid fixtures are chosen to be the mistakes a generator actually
// makes — an invented assertion kind, a plausible-but-wrong selector key, a
// required field left off — because those are the ones that have to fail
// loudly enough for whoever wrote the file to fix it.
//
// This checks shape only. Referential integrity — an assertion naming a sink
// the document never declared, a field that does not belong to the kind being
// asserted — cannot be expressed in JSON Schema and is checked by
// enrichers/overlay.Document.Validate, which has its own tests. Both run, for
// the same reason both run on a graph.

func TestValidOverlaysAreAccepted(t *testing.T) {
	for _, path := range overlayCorpus(t, "valid") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			doc, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.ValidateOverlay(doc); err != nil {
				t.Errorf("valid overlay rejected: %v", err)
			}
		})
	}
}

// Why each invalid fixture has to be rejected: where the document is wrong,
// and what the schema says about it.
//
// Asserting only that validation failed would let a fixture pass for the wrong
// reason — confidence-out-of-range.json is rejected just as loudly if a typo
// in its `assert` value is what the validator noticed first, and the
// constraint the fixture exists to defend would then be untested. It is also
// what the error a generator has to act on actually consists of, so pinning it
// keeps the messages useful and not merely present.
var overlayRejections = map[string]struct{ at, because string }{
	"confidence-out-of-range.json":   {"/assertions/0/confidence", "must be <= 1"},
	"declared-without-sink.json":     {"/assertions/0", "missing properties: 'sink'"},
	"edge-without-from.json":         {"/assertions/0", "missing properties: 'from'"},
	"empty-selector.json":            {"/assertions/0/subject", "minimum 1 properties allowed"},
	"no-assertions.json":             {"", "missing properties: 'assertions'"},
	"node-without-type-or-name.json": {"/assertions/0", "missing properties: 'type'"},
	"origin-parser.json":             {"/assertions/0/origin", `value must be one of "human", "ai"`},
	"sink-without-name.json":         {"/sinks/0", "missing properties: 'name'"},
	"unknown-assert.json":            {"/assertions/0/assert", `value must be one of "logs.declared"`},
	"unknown-assertion-field.json":   {"/assertions/0", "additionalProperties 'reason' not allowed"},
	"unknown-edge-kind.json":         {"/assertions/0/kind", `value must be one of "iac_ref"`},
	"unknown-selector-key.json":      {"/assertions/0/subject", "additionalProperties 'svc' not allowed"},
	"wrong-kind.json":                {"/kind", `value must be "oekaki.overlay"`},
	"wrong-version.json":             {"/version", `value must be "0.1"`},
}

func TestInvalidOverlaysAreRejectedForTheStatedReason(t *testing.T) {
	seen := make(map[string]bool, len(overlayRejections))

	for _, path := range overlayCorpus(t, "invalid") {
		name := filepath.Base(path)
		seen[name] = true

		t.Run(name, func(t *testing.T) {
			doc, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			err = schema.ValidateOverlay(doc)
			if err == nil {
				t.Fatal("invalid overlay accepted")
			}

			// A fixture with no stated reason is a fixture nobody has said
			// what it defends, which is how a corpus rots into decoration.
			want, ok := overlayRejections[name]
			if !ok {
				t.Fatalf("no expected rejection recorded for this fixture; add one to overlayRejections")
			}

			got := err.Error()
			if !strings.Contains(got, "'"+want.at+"'") {
				t.Errorf("rejected somewhere other than %q:\n%s", want.at, got)
			}
			if !strings.Contains(got, want.because) {
				t.Errorf("rejected for a reason other than %q:\n%s", want.because, got)
			}
		})
	}

	for name := range overlayRejections {
		if !seen[name] {
			t.Errorf("overlayRejections names %s, which is not in the corpus", name)
		}
	}
}

func TestOverlaySchemaCompiles(t *testing.T) {
	if _, err := schema.CompiledOverlay(); err != nil {
		t.Fatalf("the embedded overlay schema does not compile: %v", err)
	}
}

// One command takes either document, so it has to be able to tell them apart
// before it can say anything useful about which it got.
func TestOverlaysAreDistinguishableFromGraphs(t *testing.T) {
	for _, path := range overlayCorpus(t, "valid") {
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !schema.IsOverlay(doc) {
			t.Errorf("%s is not recognised as an overlay", filepath.Base(path))
		}
	}

	for _, path := range corpus(t, "valid") {
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if schema.IsOverlay(doc) {
			t.Errorf("%s, which is a graph, is recognised as an overlay", filepath.Base(path))
		}
	}
}

func TestValidateOverlayReportsBadJSON(t *testing.T) {
	if err := schema.ValidateOverlay([]byte("{not json")); err == nil {
		t.Fatal("expected malformed JSON to be reported")
	}
}

func overlayCorpus(t *testing.T, kind string) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("testdata", "overlay", kind, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no %s overlay fixtures found", kind)
	}
	return paths
}
