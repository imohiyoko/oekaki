package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

const LayoutID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/layout.schema.json"

//go:embed layout.schema.json
var LayoutSchema []byte

var compileLayout = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(LayoutID, bytes.NewReader(LayoutSchema)); err != nil {
		return nil, fmt.Errorf("registering embedded layout schema: %w", err)
	}
	sch, err := c.Compile(LayoutID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded layout schema: %w", err)
	}
	return sch, nil
})

func CompiledLayout() (*jsonschema.Schema, error) { return compileLayout() }

func ValidateLayout(doc []byte) error {
	sch, err := compileLayout()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the layout schema: %w", err)
	}
	return nil
}

func IsLayout(doc []byte) bool {
	var head struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(doc, &head) == nil && head.Kind == "oekaki.layout"
}
