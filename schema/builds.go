package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// BuildsID is the canonical identifier of the build-record schema.
const BuildsID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/builds.schema.json"

// BuildsSchema is the contract for what a CI system says it built.
//
// A document of its own rather than a corner of the overlay vocabulary: an
// overlay is an assertion somebody makes about a graph, and this is a record a
// pipeline already wrote about itself. The difference matters at the point of
// trust — an overlay carries an author, and a build record carries a run.
//
//go:embed builds.schema.json
var BuildsSchema []byte

var compileBuilds = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(BuildsID, bytes.NewReader(BuildsSchema)); err != nil {
		return nil, fmt.Errorf("registering embedded builds schema: %w", err)
	}
	sch, err := c.Compile(BuildsID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded builds schema: %w", err)
	}
	return sch, nil
})

// CompiledBuilds returns the compiled build-record schema.
func CompiledBuilds() (*jsonschema.Schema, error) { return compileBuilds() }

// ValidateBuilds checks a JSON document against the build-record schema.
func ValidateBuilds(doc []byte) error {
	sch, err := compileBuilds()
	if err != nil {
		return err
	}

	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the builds schema: %w", err)
	}
	return nil
}

// IsBuilds reports whether a document announces itself as build records.
func IsBuilds(doc []byte) bool {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(doc, &head); err != nil {
		return false
	}
	return head.Kind == "oekaki.builds"
}
