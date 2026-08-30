package html

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/providers"
)

// categories are the glyph names the built-in sprite defines and the page
// falls back to.
var categories = []string{"compute", "database", "network", "security", "storage", "generic"}

// loadIcons builds a sprite from a directory of SVG files.
//
// Only the types this graph actually contains are read, so pointing at a set
// with a thousand icons in it costs the size of the dozen that get used. A
// resource type may have its own file; otherwise its category's file stands in;
// otherwise the built-in glyph does. Nothing is required to be present, because
// a set that happens not to cover somebody's estate should degrade rather than
// fail.
func loadIcons(dir string, g *core.Graph) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("icon directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("icon directory %q is not a directory", dir)
	}

	wanted := map[string]bool{}
	for _, n := range g.Nodes {
		wanted[n.Type] = true
		wanted[string(providers.CategoryOf(n.Type))] = true
	}
	for _, c := range categories {
		wanted[c] = true
	}

	names := make([]string, 0, len(wanted))
	for n := range wanted {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" class="icon-sprite">` + "\n")

	var found int
	for _, name := range names {
		// filepath.Base defends the join: a resource type is data from an
		// input file, and a type containing a path separator must not be able
		// to reach outside the directory the user pointed at.
		path := filepath.Join(dir, filepath.Base(name)+".svg")
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		body, ok := symbolBody(string(raw))
		if !ok {
			continue
		}
		found++
		fmt.Fprintf(&b, "  <symbol id=%q viewBox=%q>%s</symbol>\n",
			"icon-"+name, viewBoxOf(string(raw)), body)
	}

	if found == 0 {
		return "", fmt.Errorf("no usable .svg files in %q: expected files named after a resource type or a category, such as %s",
			dir, strings.Join([]string{"aws_ecs_service.svg", "compute.svg"}, " or "))
	}

	// Anything the directory did not supply keeps its built-in glyph, so a
	// partial set is usable rather than a wall of blanks.
	for _, c := range categories {
		if !hasSymbol(b.String(), "icon-"+c) {
			if body, ok := builtinSymbol(c); ok {
				b.WriteString("  " + body + "\n")
			}
		}
	}

	b.WriteString("</svg>\n")
	return b.String(), nil
}

var (
	svgOpen  = regexp.MustCompile(`(?is)<svg[^>]*>`)
	svgClose = regexp.MustCompile(`(?is)</svg\s*>`)
	viewBox  = regexp.MustCompile(`(?is)viewBox="([^"]+)"`)
	symbolRe = regexp.MustCompile(`(?is)<symbol\s+id="([^"]+)".*?</symbol>`)
)

// symbolBody strips an SVG file down to what can live inside a <symbol>.
func symbolBody(raw string) (string, bool) {
	open := svgOpen.FindStringIndex(raw)
	close := svgClose.FindStringIndex(raw)
	if open == nil || close == nil || close[0] < open[1] {
		return "", false
	}
	body := strings.TrimSpace(raw[open[1]:close[0]])
	return sanitizeSVGBody(body)
}

func viewBoxOf(raw string) string {
	if m := viewBox.FindStringSubmatch(raw); m != nil {
		candidate := strings.TrimSpace(m[1])
		if regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?(?:\s+-?[0-9]+(?:\.[0-9]+)?){3}$`).MatchString(candidate) {
			return candidate
		}
	}
	return "0 0 16 16"
}

// sanitizeSVGBody keeps only passive shape markup before it becomes template.HTML.
// User-provided icons are otherwise an HTML injection boundary: scripts, event
// handlers, styles and external references must never enter the self-contained page.
func sanitizeSVGBody(body string) (string, bool) {
	allowed := map[string]bool{"g": true, "path": true, "circle": true, "rect": true, "line": true, "polyline": true, "polygon": true, "ellipse": true}
	attrs := map[string]bool{"id": true, "d": true, "fill": true, "fill-rule": true, "clip-rule": true, "stroke": true, "stroke-width": true, "stroke-linecap": true, "stroke-linejoin": true, "stroke-dasharray": true, "points": true, "x": true, "y": true, "x1": true, "y1": true, "x2": true, "y2": true, "cx": true, "cy": true, "r": true, "rx": true, "ry": true, "width": true, "height": true, "transform": true, "opacity": true}
	dec := xml.NewDecoder(bytes.NewBufferString("<root>" + body + "</root>"))
	var out bytes.Buffer
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "root" {
				continue
			}
			if !allowed[t.Name.Local] {
				return "", false
			}
			out.WriteByte('<')
			out.WriteString(t.Name.Local)
			for _, a := range t.Attr {
				if !attrs[a.Name.Local] || strings.ContainsAny(a.Value, "<>\"") || strings.Contains(strings.ToLower(a.Value), "url(") {
					return "", false
				}
				fmt.Fprintf(&out, " %s=%q", a.Name.Local, a.Value)
			}
			out.WriteByte('>')
			depth++
		case xml.EndElement:
			if t.Name.Local == "root" {
				continue
			}
			out.WriteString("</")
			out.WriteString(t.Name.Local)
			out.WriteByte('>')
			depth--
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return "", false
			}
		case xml.Comment, xml.Directive, xml.ProcInst:
			return "", false
		}
	}
	return out.String(), out.Len() > 0 && depth == 0
}

func hasSymbol(sprite, id string) bool {
	return strings.Contains(sprite, `<symbol id="`+id+`"`)
}

func builtinSymbol(category string) (string, bool) {
	for _, m := range symbolRe.FindAllStringSubmatch(builtinIcons, -1) {
		if m[1] == "icon-"+category {
			return m[0], true
		}
	}
	return "", false
}
