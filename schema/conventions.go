package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

const ConventionsID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/conventions.schema.json"

//go:embed conventions.schema.json
var ConventionsSchema []byte

var compileConventions = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(ConventionsID, bytes.NewReader(ConventionsSchema)); err != nil {
		return nil, fmt.Errorf("registering embedded conventions schema: %w", err)
	}
	sch, err := c.Compile(ConventionsID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded conventions schema: %w", err)
	}
	return sch, nil
})

// ValidateConventions checks a conventions document that has already been
// turned into JSON.
//
// The file people write is YAML, because it is written by hand and the reason
// for each entry belongs next to it — and JSON cannot carry a comment. The
// schema stays JSON so there is one description of the shape rather than two.
func ValidateConventions(doc []byte) error {
	sch, err := compileConventions()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the conventions schema: %w", err)
	}
	return nil
}
