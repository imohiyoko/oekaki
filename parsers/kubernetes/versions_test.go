package kubernetes

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

const doc = "../../docs/kubernetes.md"

// podTemplate writes the path a kind keeps its pod template at. It is stated
// here independently of the parser on purpose: a test that asks the parser
// where to look would agree with any answer the parser gave.
var podTemplate = map[string]string{
	"Pod":         "spec",
	"Deployment":  "spec.template.spec",
	"StatefulSet": "spec.template.spec",
	"DaemonSet":   "spec.template.spec",
	"ReplicaSet":  "spec.template.spec",
	"Job":         "spec.template.spec",
	"CronJob":     "spec.jobTemplate.spec.template.spec",
}

// manifest builds the smallest object of a kind that the table claims to
// recognise, mounting a ConfigMap when the row says config is recovered.
func manifest(a API, withConfig bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: %s\nkind: %s\nmetadata:\n  name: probe\n  namespace: shop\n", a.APIVersion(), a.Kind)
	path, ok := podTemplate[a.Kind]
	if !ok || !withConfig {
		return b.String()
	}
	indent := ""
	for _, segment := range strings.Split(path, ".") {
		fmt.Fprintf(&b, "%s%s:\n", indent, segment)
		indent += "  "
	}
	fmt.Fprintf(&b, "%scontainers:\n%s- name: probe\n%s  envFrom:\n%s  - configMapRef:\n%s      name: settings\n",
		indent, indent, indent, indent, indent)
	return b.String()
}

// Every row is a promise that an apiVersion is recognised. A row the parser
// does not actually accept is worse than a missing one: it is a documented
// capability that silently is not there.
func TestEveryRowIsRecognised(t *testing.T) {
	for _, a := range Table() {
		t.Run(a.APIVersion()+"/"+a.Kind, func(t *testing.T) {
			res, err := Parse([]byte(manifest(a, false)), Options{})
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if a.Kind == "Namespace" {
				if _, ok := res.Graph.Group("namespace:probe"); !ok {
					t.Fatal("the Namespace did not become a container")
				}
				return
			}
			n, ok := res.Graph.Node(strings.ToLower(a.Kind) + "/shop/probe")
			if !ok {
				t.Fatal("the object did not become a node")
			}
			if n.Attrs["api_unknown"] == true {
				t.Error("a listed apiVersion was not recognised")
			}
			if res.MinimumRelease != a.Since {
				t.Errorf("MinimumRelease = %q, want the row's Since %q", res.MinimumRelease, a.Since)
			}
		})
	}
}

// A row that claims to recover config has to recover it from that kind's own
// template path. The paths differ per kind, and a kind added to the table
// without being added to the parser's switch would draw a box with no arrows.
func TestRowsThatClaimConfigRecoverIt(t *testing.T) {
	for _, a := range Table() {
		if !claims(a, "configmap") {
			continue
		}
		t.Run(a.APIVersion()+"/"+a.Kind, func(t *testing.T) {
			res, err := Parse([]byte(manifest(a, true)), Options{})
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			from := strings.ToLower(a.Kind) + "/shop/probe"
			if !hasEdge(res.Graph, from, "configmap/shop/settings", "reads") {
				t.Errorf("%s claims to recover config but no edge was read from it", a.Kind)
			}
		})
	}
}

// The table is the answer to "how far does this go?", so it has to be
// readable without opening the source. A generated document drifts the moment
// nobody regenerates it, which is what this compares.
func TestDocumentMatchesTheTable(t *testing.T) {
	raw, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	// Compared exactly rather than by containment: a stale row left below the
	// generated block would still be found by a search, and the document
	// would go on advertising an apiVersion nothing recognises.
	if got, want := tableBlock(t, string(raw)), Markdown(); got != want {
		t.Errorf("%s is out of date.\n\nhas:\n%s\nwants:\n%s", doc, got, want)
	}
	if !strings.Contains(string(raw), SupportedThrough) {
		t.Errorf("%s does not name the release the table was checked against", doc)
	}
}

// Releases are compared as numbers, not as text: 1.9 came before 1.16, and
// string order says the opposite.
func TestReleasesCompareNumerically(t *testing.T) {
	if compare("1.9", "1.16") >= 0 {
		t.Error("1.9 did not sort before 1.16")
	}
	if compare("1.25", "1.25") != 0 {
		t.Error("a release did not equal itself")
	}
	api := API{Removed: "1.25"}
	if api.Served("1.25") || !api.Served("1.24") {
		t.Error("a removed apiVersion is served in the wrong releases")
	}
}

// tableBlock returns the run of table rows the document carries, from the
// header to the first line that is not one.
func tableBlock(t *testing.T, raw string) string {
	t.Helper()
	start := strings.Index(raw, "| apiVersion |")
	if start < 0 {
		t.Fatalf("%s has no generated table", doc)
	}
	var b strings.Builder
	for _, line := range strings.Split(raw[start:], "\n") {
		if !strings.HasPrefix(line, "|") {
			break
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func claims(a API, what string) bool {
	for _, r := range a.Recovers {
		if r == what {
			return true
		}
	}
	return false
}
