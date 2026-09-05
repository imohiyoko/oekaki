// Package schema embeds the IR JSON Schema and validates documents against it.
//
// The schema, not the Go types, is the contract. A parser written in another
// language is a first-class parser as long as its output validates here.
package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// ID is the canonical identifier of the IR schema.
const ID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/graph.schema.json"

// AICandidatesID is the contract for optional model-produced structure and
// relationship suggestions.
const AICandidatesID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/ai-candidates.schema.json"

// ReachabilityID is the contract for effective network paths produced by an
// external policy or cloud collector.
const ReachabilityID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/reachability.schema.json"

// ObservationsID is the contract for metric and sensor snapshots.
const ObservationsID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/observations.schema.json"

// GraphSchema is the schema source, embedded so the binary needs no data files.
//
//go:embed graph.schema.json
var GraphSchema []byte

// LegacyGraphSchema and LegacyGraphSchemaV05 are the frozen contracts of the
// versions Decode still reads, used to validate an input before core migrates
// it. Keeping the exact old schema prevents Go's omitempty behavior from
// laundering invalid legacy fields while re-encoding the migrated document.
//
//go:embed graph-v0.4.schema.json
var LegacyGraphSchema []byte

//go:embed graph-v0.5.schema.json
var LegacyGraphSchemaV05 []byte

//go:embed ai-candidates.schema.json
var AICandidatesSchema []byte

//go:embed reachability.schema.json
var ReachabilitySchema []byte

//go:embed observations.schema.json
var ObservationsSchema []byte

var compile = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(ID, bytes.NewReader(GraphSchema)); err != nil {
		return nil, fmt.Errorf("registering embedded schema: %w", err)
	}
	sch, err := c.Compile(ID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded schema: %w", err)
	}
	return sch, nil
})

// Each frozen document intentionally retains the canonical graph schema ID.
// Each is compiled in its own compiler, so they cannot collide with each other
// or with the current version.
var compileLegacyGraph = sync.OnceValues(func() (*jsonschema.Schema, error) {
	return compileFrozen(LegacyGraphSchema, "0.4")
})

var compileLegacyGraphV05 = sync.OnceValues(func() (*jsonschema.Schema, error) {
	return compileFrozen(LegacyGraphSchemaV05, "0.5")
})

func compileFrozen(doc []byte, version string) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(ID, bytes.NewReader(doc)); err != nil {
		return nil, fmt.Errorf("registering embedded IR %s schema: %w", version, err)
	}
	sch, err := c.Compile(ID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded IR %s schema: %w", version, err)
	}
	return sch, nil
}

var compileAICandidates = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(AICandidatesID, bytes.NewReader(AICandidatesSchema)); err != nil {
		return nil, fmt.Errorf("registering embedded AI candidate schema: %w", err)
	}
	sch, err := c.Compile(AICandidatesID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded AI candidate schema: %w", err)
	}
	return sch, nil
})

var compileReachability = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(ReachabilityID, bytes.NewReader(ReachabilitySchema)); err != nil {
		return nil, fmt.Errorf("registering embedded reachability schema: %w", err)
	}
	if err := c.AddResource(ID, bytes.NewReader(GraphSchema)); err != nil {
		return nil, fmt.Errorf("registering graph schema for reachability claims: %w", err)
	}
	sch, err := c.Compile(ReachabilityID)
	if err != nil {
		return nil, fmt.Errorf("compiling reachability schema: %w", err)
	}
	return sch, nil
})

var compileObservations = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(ObservationsID, bytes.NewReader(ObservationsSchema)); err != nil {
		return nil, fmt.Errorf("registering embedded observations schema: %w", err)
	}
	if err := c.AddResource(ID, bytes.NewReader(GraphSchema)); err != nil {
		return nil, fmt.Errorf("registering graph schema for observations: %w", err)
	}
	sch, err := c.Compile(ObservationsID)
	if err != nil {
		return nil, fmt.Errorf("compiling observations schema: %w", err)
	}
	return sch, nil
})

// Compiled returns the compiled IR schema.
func Compiled() (*jsonschema.Schema, error) { return compile() }

// Validate checks a JSON document against the IR schema. The error, when
// there is one, lists every violation rather than just the first.
func Validate(doc []byte) error {
	sch, err := compile()
	if err != nil {
		return err
	}

	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the IR schema: %w", err)
	}
	return nil
}

// ValidateLegacyGraph checks a document against the exact contract of the
// version it declares. It exists for Decode's migration path; new producers
// must use Validate and emit the current version instead.
//
// The old contract is applied rather than the current one because a document
// has to be judged by the promise it was written to. Reading a broken 0.4
// document against a later schema would let a field that was invalid then pass
// now, and the migration would launder it on the way through.
func ValidateLegacyGraph(version string, doc []byte) error {
	var compile func() (*jsonschema.Schema, error)
	switch version {
	case "0.4":
		compile = compileLegacyGraph
	case "0.5":
		compile = compileLegacyGraphV05
	default:
		return fmt.Errorf("IR %s is not a version this build can read", version)
	}
	sch, err := compile()
	if err != nil {
		return err
	}

	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing legacy document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the IR %s schema: %w", version, err)
	}
	return nil
}

// ValidateAICandidates checks the optional model output contract.
func ValidateAICandidates(doc []byte) error {
	sch, err := compileAICandidates()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the AI candidate schema: %w", err)
	}
	return nil
}

// IsAICandidates identifies model candidate documents without accepting them.
func IsAICandidates(doc []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(doc, &probe) == nil && probe.Kind == "oekaki.ai-candidates"
}

// ValidateReachability checks normalized effective network paths.
func ValidateReachability(doc []byte) error {
	sch, err := compileReachability()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the reachability schema: %w", err)
	}
	return nil
}

// IsReachability identifies normalized network evidence documents.
func IsReachability(doc []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(doc, &probe) == nil && probe.Kind == "oekaki.reachability"
}

// ValidateObservations checks a metric/sensor snapshot document.
func ValidateObservations(doc []byte) error {
	sch, err := compileObservations()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the observations schema: %w", err)
	}
	return nil
}

// IsObservations identifies metric/sensor snapshot documents.
func IsObservations(doc []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(doc, &probe) == nil && probe.Kind == "oekaki.observations"
}
