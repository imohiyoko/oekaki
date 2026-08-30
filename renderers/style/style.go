// Package style turns what a resource *is* into how it is drawn, so that a
// database looks the same whether you asked for SVG or Mermaid.
//
// Classification lives in package providers, not here. This package knows only
// about colour: given a category, what does it look like. That split is what
// stops "which attributes does aws_ecs_service carry" and "what colour is it"
// from being answered in two files that drift apart.
package style

import "github.com/imohiyoko/oekaki/providers"

// Category is re-exported so renderers need only import this package.
type Category = providers.Category

// Palette is one category's colours. Fill is deliberately pale: the text and
// the edges are the content, and a saturated box fights them for attention.
type Palette struct {
	Fill   string
	Stroke string
	Text   string
}

var palettes = map[Category]Palette{
	providers.Network:  {Fill: "#e8f0fe", Stroke: "#3b6fd4", Text: "#16305e"},
	providers.Compute:  {Fill: "#e7f5ec", Stroke: "#3f9159", Text: "#1c4429"},
	providers.Database: {Fill: "#f0eafc", Stroke: "#7a52c7", Text: "#38215e"},
	providers.Security: {Fill: "#fdf0e3", Stroke: "#c97b1e", Text: "#5e3a0c"},
	providers.Storage:  {Fill: "#fdeef0", Stroke: "#c74f63", Text: "#5e1f2b"},
	providers.Generic:  {Fill: "#f2f3f5", Stroke: "#8a9099", Text: "#33383d"},
}

// CategoryOf classifies a resource type.
func CategoryOf(resourceType string) Category { return providers.CategoryOf(resourceType) }

// Of returns the palette for a resource type.
func Of(resourceType string) Palette { return ForCategory(providers.CategoryOf(resourceType)) }

// ForCategory returns a category's palette.
func ForCategory(c Category) Palette {
	if p, ok := palettes[c]; ok {
		return p
	}
	return palettes[providers.Generic]
}

// EdgeStyle describes how one kind of edge is drawn. The three kinds must stay
// visually distinct even in a printed, black-and-white diagram, which is why
// they differ in dash pattern as well as colour.
type EdgeStyle struct {
	Color  string
	Dashes string // "solid", "dashed", "bold"
	Label  string
}

var edges = map[string]EdgeStyle{
	"iac_ref":   {Color: "#8a9099", Dashes: "solid", Label: "references"},
	"reachable": {Color: "#c97b1e", Dashes: "dashed", Label: "can reach"},
	"observed":  {Color: "#3b6fd4", Dashes: "bold", Label: "observed traffic"},
}

// ForEdge returns the style for an edge kind.
func ForEdge(kind string) EdgeStyle {
	if s, ok := edges[kind]; ok {
		return s
	}
	return EdgeStyle{Color: "#8a9099", Dashes: "solid", Label: kind}
}

// groupPalettes styles container borders by group type. Types are shared
// vocabulary across providers — a "subnet" is a subnet whether AWS, Azure or
// GCP produced it — so one table covers them all.
var groupPalettes = map[string]Palette{
	"vpc":            {Fill: "#fbfcfe", Stroke: "#5b7fa6", Text: "#33506b"},
	"subnet":         {Fill: "#f6f8fa", Stroke: "#9aa7b4", Text: "#4a5763"},
	"provider":       {Fill: "#fcfbfe", Stroke: "#8a7fb0", Text: "#4a4163"},
	"module":         {Fill: "#fbfdfb", Stroke: "#7fa68a", Text: "#3f6b4f"},
	"datacenter":     {Fill: "#fdfbf8", Stroke: "#a68f5b", Text: "#6b5a33"},
	"cluster":        {Fill: "#f8fbfd", Stroke: "#6b93a6", Text: "#33566b"},
	"namespace":      {Fill: "#f8fafd", Stroke: "#7f8fb0", Text: "#414f6b"},
	"resource_group": {Fill: "#fdf9fb", Stroke: "#a67f93", Text: "#6b3f52"},
}

// ForGroup returns the styling for a container type.
func ForGroup(groupType string) Palette {
	if p, ok := groupPalettes[groupType]; ok {
		return p
	}
	return Palette{Fill: "#fafbfc", Stroke: "#a8b0b8", Text: "#4a5763"}
}

// ShortType trims the provider prefix so labels read "ecs_service" rather than
// "aws_ecs_service".
func ShortType(resourceType string) string { return providers.ShortType(resourceType) }
