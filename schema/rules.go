package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

const RulesID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/rules.schema.json"

//go:embed rules.schema.json
var RulesSchema []byte

var compileRules = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(RulesID, bytes.NewReader(RulesSchema)); err != nil {
		return nil, fmt.Errorf("registering embedded rules schema: %w", err)
	}
	sch, err := c.Compile(RulesID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded rules schema: %w", err)
	}
	return sch, nil
})

// ValidateRules checks a rules document that has already been turned into JSON.
//
// Rules are a document rather than an expression language on purpose. A
// document can be read by somebody who did not write it, reviewed in a pull
// request, and diffed between two versions of an estate; an expression needs an
// evaluator, and an evaluator needs a sandbox, and neither is a thing this
// project has to have. What a rule cannot say belongs in a collector, which
// already holds the credentials and the vendor's own query language and writes
// its conclusion back as an ordinary observation.
func ValidateRules(doc []byte) error {
	sch, err := compileRules()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the rules schema: %w", err)
	}
	return nil
}

// IsRules reports whether a document says it is one, so a single front door can
// tell it apart from the others.
func IsRules(doc []byte) bool {
	var head struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(doc, &head) == nil && head.Kind == "oekaki.rules"
}
