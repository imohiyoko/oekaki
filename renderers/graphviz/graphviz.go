// Package graphviz lays the IR out with Graphviz and returns SVG.
//
// Graphviz runs in-process as WebAssembly, so `oekaki render` works on a
// machine with no `dot` binary and no cgo toolchain. That matters more than it
// sounds: the difference between "install Graphviz first" and "run this" is
// most of the reason a tool does or does not get tried.
//
// Only SVG is offered. The WebAssembly build can also emit PNG, but its raster
// backend ignores `fill="none"` and paints every edge spline as a solid blob,
// so the pictures come out wrong. Anyone who needs a bitmap can render the DOT
// with a system Graphviz, which the `-f dot` output exists for.
package graphviz

import (
	"bytes"
	"context"
	"fmt"

	gv "github.com/goccy/go-graphviz"

	"github.com/imohiyoko/oekaki/core"
	dotrender "github.com/imohiyoko/oekaki/renderers/dot"
)

// Options tunes the output. It mirrors the DOT renderer's options because the
// DOT is what actually gets laid out.
type Options struct {
	Title   string
	RankDir string
	Axis    string
	Kinds   []core.EdgeKind
	Legend  bool
}

// Render lays out the graph and returns SVG.
func Render(ctx context.Context, g *core.Graph, opts Options) ([]byte, error) {
	src, err := dotrender.Render(g, dotrender.Options{
		Title:   opts.Title,
		RankDir: opts.RankDir,
		Axis:    opts.Axis,
		Kinds:   opts.Kinds,
		Legend:  opts.Legend,
	})
	if err != nil {
		return nil, err
	}
	return RenderDOT(ctx, src)
}

// RenderDOT lays out DOT source produced elsewhere. Splitting this out keeps
// `oekaki render -f dot` and `-f svg` on exactly the same path, so the DOT
// a user inspects is the DOT that produced their picture.
func RenderDOT(ctx context.Context, src string) ([]byte, error) {
	engine, err := gv.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting graphviz: %w", err)
	}
	defer engine.Close()

	parsed, err := gv.ParseBytes([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("graphviz rejected the generated DOT: %w", err)
	}
	defer parsed.Close()

	var buf bytes.Buffer
	if err := engine.Render(ctx, parsed, gv.SVG, &buf); err != nil {
		return nil, fmt.Errorf("laying out the graph: %w", err)
	}
	return buf.Bytes(), nil
}
