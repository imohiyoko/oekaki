package graphviz

import (
	"context"
	"html"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/internal/textmetrics"
)

// fitFixture uses the long type names that actually occur in real estates.
// Short labels fit almost any box; the defect only shows on the long ones.
func fitFixture() *core.Graph {
	g := &core.Graph{
		Version: core.Version,
		Axes:    []core.Axis{{ID: core.AxisNetwork}},
		Nodes: []core.Node{
			{ID: "aws_ecs_task_definition.app", Type: "aws_ecs_task_definition", Name: "app"},
			{ID: "aws_db_subnet_group.main", Type: "aws_db_subnet_group", Name: "main"},
			{ID: "aws_lb_target_group.api", Type: "aws_lb_target_group", Name: "api"},
			{ID: "aws_security_group.database", Type: "aws_security_group", Name: "database"},
			{ID: "aws_cloudwatch_log_group.platform", Type: "aws_cloudwatch_log_group", Name: "platform-application"},
			// A Name tag is free text, so it is routinely not ASCII. Graphviz
			// has no metrics for these either, and full-width characters are
			// where its estimate is furthest out: it assumes about 0.83em
			// against a true 1em, so a full-width label is under-measured by
			// roughly a sixth.
			//
			// The node margin absorbs that up to about seventeen full-width
			// characters, which is where this check starts failing, and real
			// spill begins around twenty-five. Closing that gap would mean
			// emitting an explicit width per node rather than letting Graphviz
			// size it, which is a larger change than this one and belongs in
			// its own. What matters here is that the measurement no longer
			// hides it.
			{ID: "aws_instance.jp", Type: "aws_instance", Name: "本番データベース"},
			// The length that broke the first attempt at this fix. Correct
			// metrics alone were not enough: Graphviz pads a box by a fixed
			// margin, while a substituted face is a *percentage* wider, so the
			// padding covers a short label and runs out on a long one. Names
			// this long are ordinary on a real estate.
			{ID: "aws_s3_bucket_server_side_encryption_configuration.main",
				Type: "aws_s3_bucket_server_side_encryption_configuration", Name: "main"},
		},
	}
	g.Normalize()
	return g
}

var (
	nodeGroupRe = regexp.MustCompile(`(?s)<g id="node\d+" class="node">(.*?)\n</g>`)
	pathRe      = regexp.MustCompile(`\sd="([^"]+)"`)
	polygonRe   = regexp.MustCompile(`\spoints="([^"]+)"`)
	numberRe    = regexp.MustCompile(`-?\d+(?:\.\d+)?`)
	textRe      = regexp.MustCompile(`font-size="([\d.]+)"[^>]*>([^<]*)</text>`)
)

// boxWidth is the horizontal extent of a node's shape, in points. Graphviz
// emits rounded boxes as a path and everything else as a polygon; in both the
// coordinates alternate x and y.
func boxWidth(shape string) (float64, bool) {
	m := pathRe.FindStringSubmatch(shape)
	if m == nil {
		m = polygonRe.FindStringSubmatch(shape)
	}
	if m == nil {
		return 0, false
	}

	nums := numberRe.FindAllString(m[1], -1)
	lo, hi := 0.0, 0.0
	for i := 0; i < len(nums); i += 2 {
		x, err := strconv.ParseFloat(nums[i], 64)
		if err != nil {
			continue
		}
		if i == 0 || x < lo {
			lo = x
		}
		if i == 0 || x > hi {
			hi = x
		}
	}
	return hi - lo, hi > lo
}

// A label wider than its box is the most visible way this tool can look
// broken, and the mechanism is invisible from the DOT: Graphviz sizes boxes
// from a metric table it looks up by font name, and the browser draws them
// with whatever font it resolves font-family to. So the assertion has to be on
// the rendered geometry, measured independently.
//
// minSlackPt is per side, on top of the substitution tolerance. A box that
// clears the tolerance by nothing is one hinting decision away from spilling.
const minSlackPt = 6.0

func TestNodeLabelsFitTheirBoxes(t *testing.T) {
	svg, err := Render(context.Background(), fitFixture(), Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	groups := nodeGroupRe.FindAllStringSubmatch(string(svg), -1)
	if len(groups) == 0 {
		t.Fatal("no nodes in the SVG; the fixture or the node markup changed")
	}

	for _, g := range groups {
		body := g[1]

		width, ok := boxWidth(body)
		if !ok {
			continue // a shape with no geometry, such as a plaintext legend row
		}

		for _, m := range textRe.FindAllStringSubmatch(body, -1) {
			size, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				t.Fatalf("unparseable font-size %q", m[1])
			}
			label := html.UnescapeString(m[2])
			if strings.TrimSpace(label) == "" {
				continue
			}

			need := textmetrics.FitWidth(label, size) + 2*minSlackPt
			if need > width {
				t.Errorf("label %q needs %.1fpt but its box is %.1fpt: it will spill out",
					label, need, width)
			}
		}
	}
}
