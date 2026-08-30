package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

const RolesID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/roles.schema.json"

//go:embed roles.schema.json
var RolesSchema []byte

var compileRoles = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(RolesID, bytes.NewReader(RolesSchema)); err != nil {
		return nil, fmt.Errorf("registering embedded roles schema: %w", err)
	}
	sch, err := c.Compile(RolesID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded roles schema: %w", err)
	}
	return sch, nil
})

// ValidateRoles checks a roles document that has already been turned into JSON.
//
// The file people write is YAML, for the same reason the conventions file is:
// it is written by hand and the reason for each entry belongs next to it. The
// schema stays JSON so there is one description of the shape rather than two.
func ValidateRoles(doc []byte) error {
	sch, err := compileRoles()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the roles schema: %w", err)
	}
	return nil
}
