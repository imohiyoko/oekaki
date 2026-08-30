package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// OverlayID is the canonical identifier of the overlay schema.
const OverlayID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/overlay.schema.json"

// OverlaySchema is the overlay schema source, embedded alongside the IR's.
//
// It is a separate document on purpose. The IR schema freezes at 1.0; the
// overlay vocabulary has to keep growing past that as more kinds of assertion
// arrive. And a model asked to emit a superset of the IR would have to get
// axes, groups and referential integrity right, where a useful assertion here
// is six lines — orthogonality is the biggest lever there is on how often a
// generated overlay is valid.
//
//go:embed overlay.schema.json
var OverlaySchema []byte

var compileOverlay = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(OverlayID, bytes.NewReader(OverlaySchema)); err != nil {
		return nil, fmt.Errorf("registering embedded overlay schema: %w", err)
	}
	sch, err := c.Compile(OverlayID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded overlay schema: %w", err)
	}
	return sch, nil
})

// CompiledOverlay returns the compiled overlay schema.
func CompiledOverlay() (*jsonschema.Schema, error) { return compileOverlay() }

// ValidateOverlay checks a JSON document against the overlay schema.
func ValidateOverlay(doc []byte) error {
	sch, err := compileOverlay()
	if err != nil {
		return err
	}

	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the overlay schema: %w", err)
	}
	return nil
}

// IsOverlay reports whether a document announces itself as an overlay.
//
// Sniffing rather than requiring a flag: `oekaki validate` should take
// whichever of the two documents a user hands it and say something useful, and
// the schema is the product, so both contracts deserve the same front door.
func IsOverlay(doc []byte) bool {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(doc, &head); err != nil {
		return false
	}
	return head.Kind == "oekaki.overlay"
}
