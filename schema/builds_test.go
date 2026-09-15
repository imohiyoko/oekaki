package schema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/schema"
)

// The build-record schema is a published contract like the other two, so it
// gets a corpus for the same reason: the files are what define it, and a case
// is added there rather than here.

func TestValidBuildRecordsAreAccepted(t *testing.T) {
	for _, path := range buildsCorpus(t, "valid") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			doc, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.ValidateBuilds(doc); err != nil {
				t.Errorf("valid build record rejected: %v", err)
			}
		})
	}
}

// Where each invalid fixture is wrong, and what the schema says about it. The
// reason is pinned rather than merely the failure, or a fixture would pass for
// whichever mistake the validator happened to notice first.
var buildsRejections = map[string]struct{ at, because string }{
	"build-without-repository.json": {"/builds/0", "missing properties: 'repository'"},
	"build-without-run.json":        {"/builds/0", "missing properties: 'run'"},
	"image-without-reference.json":  {"/builds/0/images/0", "missing properties: 'reference'"},
	"no-builds.json":                {"", "missing properties: 'builds'"},
	"no-images.json":                {"/builds/0/images", "minimum 1 items required"},
	"run-without-id.json":           {"/builds/0/run", "missing properties: 'id'"},
	"unknown-build-field.json":      {"/builds/0", "additionalProperties 'author' not allowed"},
	"wrong-kind.json":               {"/kind", `value must be "oekaki.builds"`},
}

func TestInvalidBuildRecordsAreRejectedForTheStatedReason(t *testing.T) {
	seen := make(map[string]bool, len(buildsRejections))

	for _, path := range buildsCorpus(t, "invalid") {
		name := filepath.Base(path)
		seen[name] = true

		t.Run(name, func(t *testing.T) {
			doc, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			err = schema.ValidateBuilds(doc)
			if err == nil {
				t.Fatal("invalid build record accepted")
			}

			want, ok := buildsRejections[name]
			if !ok {
				t.Fatalf("no expected rejection recorded for this fixture; add one to buildsRejections")
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

	for name := range buildsRejections {
		if !seen[name] {
			t.Errorf("buildsRejections names %s, which is not in the corpus", name)
		}
	}
}

func TestBuildsSchemaCompiles(t *testing.T) {
	if _, err := schema.CompiledBuilds(); err != nil {
		t.Fatalf("the embedded builds schema does not compile: %v", err)
	}
}

// Three document kinds now arrive through the same front door, so each has to
// be told from the others before anything useful can be said about it.
func TestBuildRecordsAreDistinguishableFromTheOtherDocuments(t *testing.T) {
	for _, path := range buildsCorpus(t, "valid") {
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !schema.IsBuilds(doc) {
			t.Errorf("%s is not recognised as build records", filepath.Base(path))
		}
		if schema.IsOverlay(doc) {
			t.Errorf("%s, which is a build record, is recognised as an overlay", filepath.Base(path))
		}
	}

	for _, path := range append(corpus(t, "valid"), overlayCorpus(t, "valid")...) {
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if schema.IsBuilds(doc) {
			t.Errorf("%s is recognised as build records", filepath.Base(path))
		}
	}
}

func TestValidateBuildsReportsBadJSON(t *testing.T) {
	if err := schema.ValidateBuilds([]byte("{not json")); err == nil {
		t.Fatal("expected malformed JSON to be reported")
	}
}

func buildsCorpus(t *testing.T, kind string) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("testdata", "builds", kind, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no %s build-record fixtures found", kind)
	}
	return paths
}
