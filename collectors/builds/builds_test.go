package builds_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/collectors/builds"
)

const full = `{
  "kind": "oekaki.builds",
  "version": "0.1",
  "builds": [{
    "repository": "acme/checkout",
    "commit": "9f1c0f2e",
    "ref": "refs/heads/main",
    "run": { "id": "17243", "workflow": "release", "url": "https://ci.example/17243", "completed_at": "2026-09-14T10:02:11Z" },
    "images": [{ "reference": "registry.example/checkout:1.4.0", "digest": "sha256:aaaa" }]
  }]
}`

func TestParseReadsTheRecord(t *testing.T) {
	doc, err := builds.Parse([]byte(full), "builds.json")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Source != "builds.json" {
		t.Errorf("source = %q", doc.Source)
	}
	if len(doc.Builds) != 1 {
		t.Fatalf("got %d builds", len(doc.Builds))
	}
	b := doc.Builds[0]
	if b.Repository != "acme/checkout" || b.Commit != "9f1c0f2e" || b.Run.ID != "17243" {
		t.Errorf("build read as %+v", b)
	}
	if b.Images[0].Reference != "registry.example/checkout:1.4.0" {
		t.Errorf("image read as %+v", b.Images[0])
	}
}

func TestParseRejectsWhatTheSchemaRejects(t *testing.T) {
	_, err := builds.Parse([]byte(`{"kind":"oekaki.builds","version":"0.1","builds":[{"repository":"a/b"}]}`), "builds.json")
	if err == nil {
		t.Fatal("a build with no run and no images was accepted")
	}
	if !strings.Contains(err.Error(), "builds.json") {
		t.Errorf("the error does not name the file:\n%v", err)
	}
}

// A reference pinned by digest says the digest twice. Two different answers
// join a running container to a build that did not produce it, whichever one
// is true, so the record is refused rather than half-believed.
func TestParseRefusesARecordThatContradictsItself(t *testing.T) {
	doc := `{
      "kind": "oekaki.builds", "version": "0.1",
      "builds": [{
        "repository": "acme/checkout",
        "run": { "id": "1" },
        "images": [{ "reference": "registry.example/checkout@sha256:aaaa", "digest": "sha256:bbbb" }]
      }]
    }`
	_, err := builds.Parse([]byte(doc), "builds.json")
	if err == nil {
		t.Fatal("a record whose reference and digest disagree was accepted")
	}
	if !strings.Contains(err.Error(), "sha256:aaaa") || !strings.Contains(err.Error(), "sha256:bbbb") {
		t.Errorf("the error names neither digest:\n%v", err)
	}
}

func TestKeysAreEveryWayAnImageIsNamed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		image builds.Image
		want  []string
	}{
		{"field only", builds.Image{Reference: "img:1", Digest: "sha256:aaaa"}, []string{"img:1", "sha256:aaaa"}},
		{"pinned only", builds.Image{Reference: "img@sha256:aaaa"}, []string{"img@sha256:aaaa", "sha256:aaaa"}},
		{"both, agreeing", builds.Image{Reference: "img@sha256:aaaa", Digest: "sha256:aaaa"}, []string{"img@sha256:aaaa", "sha256:aaaa"}},
		{"no digest at all", builds.Image{Reference: "img:1"}, []string{"img:1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.image.Keys(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Keys() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDigestOfReadsAPinnedReference(t *testing.T) {
	if got, ok := builds.DigestOf("img@sha256:aaaa"); !ok || got != "sha256:aaaa" {
		t.Errorf("DigestOf() = %q, %v", got, ok)
	}
	if _, ok := builds.DigestOf("img:1"); ok {
		t.Error("a tagged reference reported a digest")
	}
	if _, ok := builds.DigestOf("img@"); ok {
		t.Error("an empty digest reported one")
	}
}

func TestRunLabelNamesTheWorkflowWhenThereIsOne(t *testing.T) {
	if got := (builds.Run{ID: "8", Workflow: "release"}).Label(); got != "release run 8" {
		t.Errorf("Label() = %q", got)
	}
	if got := (builds.Run{ID: "8"}).Label(); got != "run 8" {
		t.Errorf("Label() = %q", got)
	}
}

// Rebuilding a tag is ordinary, so two records of one reference have to
// resolve the same way on every machine.
func TestLaterIsATotalOrder(t *testing.T) {
	early := builds.Run{ID: "9", CompletedAt: "2026-09-01T00:00:00Z"}
	late := builds.Run{ID: "1", CompletedAt: "2026-09-02T00:00:00Z"}
	if !late.Later(early) || early.Later(late) {
		t.Error("completion time does not decide")
	}

	untimed := builds.Run{ID: "9"}
	if !early.Later(untimed) || untimed.Later(early) {
		t.Error("a run with no time should be the older one")
	}
	if !(builds.Run{ID: "2"}).Later(builds.Run{ID: "1"}) {
		t.Error("the id does not break the tie")
	}
}

// Run ids are numbers a CI system counts up, and "9" sorts after "10" as
// text. The older build would win, silently.
func TestRunIdsAreComparedAsNumbers(t *testing.T) {
	if !(builds.Run{ID: "10"}).Later(builds.Run{ID: "9"}) {
		t.Error("run 10 did not beat run 9")
	}
	if (builds.Run{ID: "9"}).Later(builds.Run{ID: "10"}) {
		t.Error("run 9 beat run 10")
	}
	// Not every CI system counts in decimal; text is the honest fallback.
	if !(builds.Run{ID: "b"}).Later(builds.Run{ID: "a"}) {
		t.Error("non-numeric ids no longer order at all")
	}
}

// Two instants in different offsets are two ways of writing a moment, and as
// text the earlier one can sort later.
func TestCompletionIsComparedAsAnInstant(t *testing.T) {
	tokyo := builds.Run{ID: "1", CompletedAt: "2026-09-14T19:00:00+09:00"} // 10:00Z
	utc := builds.Run{ID: "2", CompletedAt: "2026-09-14T11:00:00Z"}
	if !utc.Later(tokyo) || tokyo.Later(utc) {
		t.Error("the offset was compared as text")
	}
}

func TestATimeNothingCanReadIsRefused(t *testing.T) {
	doc := `{
      "kind": "oekaki.builds", "version": "0.1",
      "builds": [{
        "repository": "acme/checkout",
        "run": { "id": "1", "completed_at": "last tuesday" },
        "images": [{ "reference": "img:1" }]
      }]
    }`
	_, err := builds.Parse([]byte(doc), "builds.json")
	if err == nil {
		t.Fatal("an unreadable completion time was accepted")
	}
	if !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("the error does not say what was wrong:\n%v", err)
	}
}

// The same contradiction as a pinned reference disagreeing with its digest
// field, spelled with two entries instead of one.
func TestOneReferenceWithTwoDigestsIsRefused(t *testing.T) {
	doc := `{
      "kind": "oekaki.builds", "version": "0.1",
      "builds": [{
        "repository": "acme/checkout",
        "run": { "id": "1" },
        "images": [
          { "reference": "img:1", "digest": "sha256:aaaa" },
          { "reference": "img:1", "digest": "sha256:bbbb" }
        ]
      }]
    }`
	_, err := builds.Parse([]byte(doc), "builds.json")
	if err == nil {
		t.Fatal("one reference with two digests was accepted")
	}
	if !strings.Contains(err.Error(), "sha256:aaaa") || !strings.Contains(err.Error(), "sha256:bbbb") {
		t.Errorf("the error names neither digest:\n%v", err)
	}
}
