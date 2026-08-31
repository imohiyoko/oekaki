package kubernetes

import (
	"strings"
	"testing"
)

func parseString(t *testing.T, body string) *Result {
	t.Helper()
	res, err := Parse([]byte(body), Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return res
}

// A reference has to be resolved to the id the target actually has. A
// cluster-scoped object has no namespace segment in its id, so composing one
// from the referring object's namespace names something that is not there:
// the real object sits unlinked while a phantom copy of it collects the edge.
func TestClusterScopedReferenceReachesTheRealObject(t *testing.T) {
	res := parseString(t, `
apiVersion: example.com/v1
kind: ClusterWidget
metadata:
  name: global
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: checkout
  namespace: shop
  ownerReferences:
  - apiVersion: example.com/v1
    kind: ClusterWidget
    name: global
spec:
  template:
    spec:
      containers:
      - name: checkout
        image: registry.example/checkout:1.4.0
`)
	g := res.Graph

	if !hasEdge(g, "deployment/shop/checkout", "clusterwidget/global", "owned-by") {
		t.Error("the owner reference did not reach the cluster-scoped object in the input")
	}
	if _, ok := g.Node("clusterwidget/shop/global"); ok {
		t.Error("a phantom copy was created in the referring object's namespace")
	}
}

// The same composition produces an id with an empty middle segment when the
// referring object is itself cluster-scoped. It validates, renders, and is
// wrong.
func TestAClusterScopedOwnerReferenceMakesNoEmptySegment(t *testing.T) {
	res := parseString(t, `
apiVersion: example.com/v1
kind: ClusterWidget
metadata:
  name: global
  ownerReferences:
  - kind: ClusterOwner
    name: root
`)
	for _, n := range res.Graph.Nodes {
		if strings.Contains(n.ID, "//") {
			t.Errorf("node id %q has an empty segment", n.ID)
		}
	}
	if !hasEdge(res.Graph, "clusterwidget/global", "clusterowner/root", "owned-by") {
		t.Error("the owner reference did not reach a cluster-scoped placeholder")
	}
}

// A selector pair that cannot be read must not be dropped. Dropping it widens
// the match, and the workloads it then reaches arrive looking exactly like the
// ones that were meant.
func TestSelectorWithAnUnreadableValueMatchesNothing(t *testing.T) {
	res := parseString(t, `
apiVersion: v1
kind: Service
metadata:
  name: api
  namespace: shop
spec:
  selector:
    app: api
    version: 2
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api-v1
  namespace: shop
spec:
  template:
    metadata:
      labels:
        app: api
        version: "1"
    spec:
      containers:
      - name: api
        image: registry.example/api:1
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api-v2
  namespace: shop
spec:
  template:
    metadata:
      labels:
        app: api
        version: "2"
    spec:
      containers:
      - name: api
        image: registry.example/api:2
`)
	g := res.Graph

	for _, to := range []string{"deployment/shop/api-v1", "deployment/shop/api-v2"} {
		if hasEdge(g, "service/shop/api", to, "selects") {
			t.Errorf("a partially readable selector matched %s", to)
		}
	}
	svc, _ := g.Node("service/shop/api")
	if svc.Attrs["selects"] == nil {
		t.Error("the Service does not say why it matched nothing")
	}
}

// A List is recognised by holding items. A custom resource whose kind merely
// ends in List is an object, and unwrapping it loses the whole document while
// reporting that there was nothing in it.
func TestAKindEndingInListIsStillAnObject(t *testing.T) {
	res := parseString(t, `
apiVersion: example.com/v1
kind: AllowList
metadata:
  name: partners
  namespace: shop
`)
	if _, ok := res.Graph.Node("allowlist/shop/partners"); !ok {
		t.Error("a custom resource was unwrapped as a List and lost")
	}
}

// Concatenating a base and an overlay defines some objects twice. That is a
// fact about the input, and reporting it as an IR error about a duplicate id
// tells the reader about this tool instead.
func TestADuplicateObjectIsCountedRatherThanFatal(t *testing.T) {
	res := parseString(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: shop
data:
  a: "1"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: shop
data:
  a: "2"
`)
	if err := res.Graph.Validate(); err != nil {
		t.Fatalf("a duplicated object produced an invalid graph: %v", err)
	}
	count := 0
	for _, n := range res.Graph.Nodes {
		if n.ID == "configmap/shop/settings" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the object appears %d times", count)
	}
	if len(res.Duplicates) != 1 || res.Duplicates[0] != "configmap/shop/settings" {
		t.Errorf("Duplicates = %v, want the repeated object", res.Duplicates)
	}
}

// A duplicate definition must not go on deciding anything. It is not drawn,
// but if it stays in the list its labels still answer selectors, and the edge
// that results points at the node the first definition made: an object nobody
// drew deciding what a drawn object connects to.
func TestADuplicateDoesNotAnswerSelectors(t *testing.T) {
	res := parseString(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  template:
    metadata:
      labels: {app: other}
    spec:
      containers: [{name: api, image: registry.example/api:1}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  template:
    metadata:
      labels: {app: api}
    spec:
      containers: [{name: api, image: registry.example/api:1}]
---
apiVersion: v1
kind: Service
metadata: {name: api, namespace: shop}
spec:
  selector: {app: api}
`)
	// Only the first definition was drawn, and it carries app=other.
	if hasEdge(res.Graph, "service/shop/api", "deployment/shop/api", "selects") {
		t.Error("the ignored second definition answered the Service's selector")
	}
	if len(res.Duplicates) != 1 {
		t.Errorf("Duplicates = %v", res.Duplicates)
	}
}

// A selector that is present but is not a map is malformed, not empty. An
// empty pod selector selects every pod in the namespace, so reading a typo as
// one restricts a whole namespace on the strength of it.
func TestAMalformedSelectorIsNotAnEmptyOne(t *testing.T) {
	res := parseString(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: shop}
spec:
  template:
    metadata: {labels: {app: web}}
    spec:
      containers: [{name: web, image: registry.example/web:1}]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: broken, namespace: shop}
spec:
  podSelector: whatever
  ingress: []
`)
	if hasEdge(res.Graph, "networkpolicy/shop/broken", "deployment/shop/web", "restricts") {
		t.Error("a malformed pod selector restricted the namespace as if it were empty")
	}
	got, _ := nodeAttrString(res, "networkpolicy/shop/broken", "restricts_unresolved")
	if got == "" {
		t.Error("the policy does not say that its selector could not be read")
	}
}

// The same shape on a Service: a selector that cannot be read must not become
// a selector that matches everything.
func TestAMalformedServiceSelectorMatchesNothing(t *testing.T) {
	res := parseString(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: shop}
spec:
  template:
    metadata: {labels: {app: web}}
    spec:
      containers: [{name: web, image: registry.example/web:1}]
---
apiVersion: v1
kind: Service
metadata: {name: web, namespace: shop}
spec:
  selector: whatever
`)
	if hasEdge(res.Graph, "service/shop/web", "deployment/shop/web", "selects") {
		t.Error("a malformed selector selected a workload")
	}
}

func nodeAttrString(res *Result, id, key string) (string, bool) {
	n, ok := res.Graph.Node(id)
	if !ok {
		return "", false
	}
	s, _ := n.Attrs[key].(string)
	return s, true
}
