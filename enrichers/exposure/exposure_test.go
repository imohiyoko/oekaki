package exposure

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func TestPublicFindingCreatesObservationAndExposureEdge(t *testing.T) {
	d, err := Parse([]byte(`{"kind":"oekaki.exposure","version":"1","findings":[{"subject":"service:a","endpoint":"api.example.com","public":true,"reason":"public load balancer"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	g := core.New()
	g.Nodes = []core.Node{{ID: "service:a", Type: "service", Name: "a"}}
	r, err := (Enricher{Docs: []*Document{d}}).Enrich(g)
	if err != nil || r.Applied != 1 || len(g.Observations) != 1 || len(g.Edges) != 1 {
		t.Fatalf("report=%+v err=%v", r, err)
	}
	if g.Edges[0].From != "external:internet" || g.Edges[0].To != "service:a" {
		t.Fatalf("exposure edge has wrong direction: %#v", g.Edges[0])
	}
}

func TestParseRejectsUnknownAndMissingPublicFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unknown top-level field",
			raw:  `{"kind":"oekaki.exposure","version":"1","findings":[],"extra":true}`,
			want: "unknown field",
		},
		{
			name: "misspelled finding field",
			raw:  `{"kind":"oekaki.exposure","version":"1","findings":[{"subject":"service:a","publci":true}]}`,
			want: "unknown field",
		},
		{
			name: "missing public",
			raw:  `{"kind":"oekaki.exposure","version":"1","findings":[{"subject":"service:a"}]}`,
			want: "public is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want text %q", err, tc.want)
			}
		})
	}
}

func TestParseAcceptsExplicitNonPublicFinding(t *testing.T) {
	d, err := Parse([]byte(`{"kind":"oekaki.exposure","version":"1","findings":[{"subject":"service:a","public":false}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Findings) != 1 || d.Findings[0].Public {
		t.Fatalf("finding = %#v", d.Findings)
	}
}
