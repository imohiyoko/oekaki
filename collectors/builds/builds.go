// Package builds reads what a CI system recorded about a build.
//
// Between the code graph and the infrastructure graph there is an image tag,
// and nothing else here reads where it came from. In an enterprise that join
// is already automated and already written down — a pipeline builds an image
// from a commit, and a pull request writes that image into the IaC — so the
// record of "this container is that repository at that commit" exists in the
// CI system and nowhere else.
//
// This package reads that record, and only that record. The vendor API and
// the credentials stay outside, exactly as they do for every other collector:
// what arrives here is a document somebody can read, review and check in.
package builds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/imohiyoko/oekaki/schema"
)

// Document is one build-record file.
type Document struct {
	Kind    string  `json:"kind"`
	Version string  `json:"version"`
	Builds  []Build `json:"builds"`

	// Source is the file this came from, for reports. Not part of the format.
	Source string `json:"-"`
}

// Build is one run of a pipeline, and what it produced.
type Build struct {
	Repository string  `json:"repository"`
	Commit     string  `json:"commit,omitempty"`
	Ref        string  `json:"ref,omitempty"`
	Run        Run     `json:"run"`
	Images     []Image `json:"images"`
}

// Run is the pipeline run itself: the thing a claim names when it says why
// any of this is believed.
type Run struct {
	ID          string `json:"id"`
	Workflow    string `json:"workflow,omitempty"`
	URL         string `json:"url,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// Image is one thing a build pushed.
type Image struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest,omitempty"`
}

// Parse validates a document against the builds schema, decodes it, and then
// checks what the schema cannot.
//
// Schema first, for the same reason overlays do it: the error somebody sees is
// then the one with a JSON path in it, rather than a Go decoder complaining
// about a type.
func Parse(raw []byte, source string) (*Document, error) {
	if err := schema.ValidateBuilds(raw); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}

	var doc Document
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	doc.Source = source

	if err := doc.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &doc, nil
}

// Validate checks the invariants the JSON Schema cannot express.
func (d *Document) Validate() error {
	var problems []string
	for i, b := range d.Builds {
		// Which digest each reference was said to have in this build. One
		// reference with two digests is the same contradiction as a pinned
		// reference disagreeing with its own digest field, spelled with two
		// entries instead of one — and the loser of that pair is never
		// reported, so nothing downstream could notice.
		digests := map[string]string{}

		// An unreadable instant is refused rather than carried: the order two
		// records resolve in is decided by comparing them, and a time nothing
		// can parse would fall back to comparing run ids without saying so.
		if _, ok := b.Run.completed(); !ok && b.Run.CompletedAt != "" {
			problems = append(problems, fmt.Sprintf(
				"builds[%d].run.completed_at: %q is not an RFC 3339 time",
				i, b.Run.CompletedAt))
		}

		for j, img := range b.Images {
			// A reference pinned by digest carries the digest twice, and the
			// two are matched against a running container separately. Two
			// different answers in one record is not a small inconsistency:
			// whichever one is true, the other joins a container to a build
			// that did not produce it.
			pinned, ok := DigestOf(img.Reference)
			if ok && img.Digest != "" && pinned != img.Digest {
				problems = append(problems, fmt.Sprintf(
					"builds[%d].images[%d]: %q is pinned to %s but the record says it is %s",
					i, j, img.Reference, pinned, img.Digest))
			}

			digest := img.Digest
			if digest == "" && ok {
				digest = pinned
			}
			if was, seen := digests[img.Reference]; seen && was != digest {
				problems = append(problems, fmt.Sprintf(
					"builds[%d].images[%d]: %q is %s here and %s earlier in the same build",
					i, j, img.Reference, digest, was))
			}
			digests[img.Reference] = digest
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("invalid build record:\n  %s", strings.Join(problems, "\n  "))
}

// DigestOf reads the digest out of a reference pinned by one.
//
// Exported because both halves of the join need it and they are in different
// packages: a record writes `name@sha256:…` and so does an estate that pins
// its images, and reading the two with one function is what keeps them
// meaning the same thing.
func DigestOf(reference string) (string, bool) {
	_, digest, found := strings.Cut(reference, "@")
	if !found || digest == "" {
		return "", false
	}
	return digest, true
}

// Keys returns every way this image can be named: the reference as written,
// and the digests it is known by. A record may carry either and an estate may
// pin either, so both are indexed.
func (i Image) Keys() []string {
	out := make([]string, 0, 3)
	out = append(out, i.Reference)
	if i.Digest != "" {
		out = append(out, i.Digest)
	}
	if pinned, ok := DigestOf(i.Reference); ok && pinned != i.Digest {
		out = append(out, pinned)
	}
	return out
}

// Label names a run the way a claim should: by the workflow somebody knows it
// as, when the record says, and by its id either way.
func (r Run) Label() string {
	if r.Workflow != "" {
		return fmt.Sprintf("%s run %s", r.Workflow, r.ID)
	}
	return "run " + r.ID
}

// Later reports whether r happened after other.
//
// Completion time decides, and the id breaks the tie — a run without a time is
// older than one with, so two records of the same image reference resolve the
// same way on every machine. Rebuilding a tag is ordinary; picking a different
// winner each run would make the drawing non-deterministic, which is the one
// property the whole pipeline is built on.
//
// Both halves are compared as what they are rather than as text. Two instants
// written in different offsets are the same instant written twice, and
// `2026-09-14T19:00:00+09:00` sorts after `2026-09-14T11:00:00Z` while being
// an hour earlier. Run ids are numbers a CI system counts up, and run 9 sorts
// after run 10 as a string. Either comparison silently prefers the older
// build — silently, because nothing downstream can tell which one it picked.
func (r Run) Later(other Run) bool {
	mine, iSay := r.completed()
	theirs, theySay := other.completed()
	switch {
	case iSay != theySay:
		return iSay
	case iSay && !mine.Equal(theirs):
		return mine.After(theirs)
	}
	if mine, theirs, ok := numbers(r.ID, other.ID); ok {
		return mine > theirs
	}
	return r.ID > other.ID
}

// completed is when the run finished, and whether it said.
//
// Parsing cannot fail here: a record whose time cannot be read is refused by
// Validate, so this is total by the time anything compares two runs.
func (r Run) completed() (time.Time, bool) {
	if r.CompletedAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, r.CompletedAt)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// numbers reads two run ids as the numbers a CI system counts up, when both
// of them are.
func numbers(a, b string) (int64, int64, bool) {
	x, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	y, err := strconv.ParseInt(b, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return x, y, true
}
