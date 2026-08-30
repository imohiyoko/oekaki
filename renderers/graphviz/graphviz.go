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
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"

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

	// CSS is a stylesheet placed inside the SVG, for a caller who wants the
	// picture to match the rest of what they publish.
	//
	// The selectors are not the ones an HTML page takes: Graphviz writes an
	// edge as <g class="edge"><path/><polygon/>, so the line is a descendant
	// of the class rather than carrying it. A sheet written for one format
	// therefore does nothing in the other, which is a property of the markup
	// and not something this flag can paper over.
	CSS []byte
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
	out, err := RenderDOT(ctx, src)
	if err != nil {
		return nil, err
	}
	return withStyle(out, opts.CSS)
}

// svgRoot matches the document's own <svg> open tag. Graphviz breaks it over
// two lines, and a DOCTYPE naming the SVG DTD stands before it, so this looks
// for the element rather than for the letters.
var svgRoot = regexp.MustCompile(`<svg\b[^>]*>`)

// withStyle puts a caller's stylesheet inside the SVG root.
//
// It is written into the document rather than asked of Graphviz because
// Graphviz cannot emit one: its `stylesheet` attribute produces an
// xml-stylesheet instruction, which is a reference to a second file, and an
// SVG that only looks right next to its stylesheet is no longer a thing you
// can attach to a ticket.
//
// The sheet goes in a CDATA section because an SVG is XML, where a bare & or
// < is a parse error rather than a style that fails to apply — and both occur
// in ordinary CSS now that nesting uses & and container queries use <.
func withStyle(svg, css []byte) ([]byte, error) {
	if len(css) == 0 {
		return svg, nil
	}
	if at := bytes.Index(css, []byte("]]>")); at >= 0 {
		return nil, fmt.Errorf("this stylesheet writes \"]]>\" at byte %d, which would end the SVG's CDATA section early", at)
	}
	if err := xmlText(css); err != nil {
		return nil, err
	}
	root := svgRoot.FindIndex(svg)
	if root == nil {
		return nil, errors.New("graphviz produced no <svg> element to put a stylesheet in")
	}
	var out bytes.Buffer
	out.Write(svg[:root[1]])
	out.WriteString("\n<style type=\"text/css\"><![CDATA[\n")
	out.Write(bytes.TrimRight(css, "\n"))
	out.WriteString("\n]]></style>")
	out.Write(svg[root[1]:])
	return out.Bytes(), nil
}

// xmlText reports whether the bytes can appear in an XML document at all.
//
// CDATA settles what the characters mean, not whether they are characters.
// XML 1.0 admits no control character but tab, newline and carriage return,
// and no byte sequence that is not UTF-8 — so a stylesheet saved in Latin-1,
// or one carrying a stray NUL, produces a picture that every parser refuses
// to open rather than a rule that fails to apply.
//
// An HTML page takes both without complaint: its parser substitutes the
// replacement character and carries on. This check therefore belongs here and
// not in the HTML renderer, where it would refuse files that work.
func xmlText(css []byte) error {
	for at := 0; at < len(css); {
		r, size := utf8.DecodeRune(css[at:])
		if r == utf8.RuneError && size == 1 {
			return fmt.Errorf("this stylesheet is not UTF-8 at byte %d, and an SVG carrying it will not open", at)
		}
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0xFFFE || r == 0xFFFF {
			return fmt.Errorf("this stylesheet has the character %U at byte %d, which XML does not allow anywhere", r, at)
		}
		at += size
	}
	return nil
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
