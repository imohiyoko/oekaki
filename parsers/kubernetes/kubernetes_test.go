package kubernetes

import (
	"os"
	"testing"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/schema"
)

func parseFile(t *testing.T, path string) *Result {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(raw, Options{File: path})
	if err != nil {
		t.Fatalf("Parse(%s): %v", path, err)
	}
	return res
}

func hasEdge(g *core.Graph, from, to, relation string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Relation == relation {
			return true
		}
	}
	return false
}

// The parser's output has to satisfy the published schema, not just the Go
// struct. This is the check that keeps the two from drifting apart.
func TestParsedGraphMatchesTheSchema(t *testing.T) {
	doc, err := parseFile(t, "testdata/shop.yaml").Graph.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("parser output does not match the published schema: %v", err)
	}
}

// A Service points at pods by label, never by name. Reading the selector is
// the only way to recover which workload answers a Service, and getting it
// wrong draws an arrow at the wrong box with full confidence.
func TestServiceSelectsWorkloadByLabels(t *testing.T) {
	g := parseFile(t, "testdata/shop.yaml").Graph

	if !hasEdge(g, "service/shop/checkout", "deployment/shop/checkout", "selects") {
		t.Error("the Service did not reach the Deployment its selector matches")
	}
	// The CronJob's pods carry app=reconcile, so the same selector must not
	// pick them up.
	if hasEdge(g, "service/shop/checkout", "cronjob/shop/nightly-reconcile", "selects") {
		t.Error("the selector matched a workload whose labels differ")
	}
}

// A Service with no selector is not a Service that failed to match: it is an
// ExternalName or hand-managed Endpoints. Drawing nothing and saying nothing
// leaves the reader unable to tell the two apart.
func TestServiceWithoutSelectorSaysWhy(t *testing.T) {
	g := parseFile(t, "testdata/shop.yaml").Graph

	svc, ok := g.Node("service/shop/warehouse")
	if !ok {
		t.Fatal("the selector-less Service is missing")
	}
	if svc.Attrs["selects"] == nil {
		t.Error("the Service does not record that it selects nothing")
	}
}

// An Ingress names its backend Service directly, in one of two shapes
// depending on how old the manifest is. A repository still on the older shape
// is exactly the one that needs to see its routing drawn.
func TestIngressRoutesToServiceInBothShapes(t *testing.T) {
	current := parseFile(t, "testdata/shop.yaml").Graph
	if !hasEdge(current, "ingress/shop/storefront", "service/shop/checkout", "routes") {
		t.Error("the v1 Ingress did not reach its backend Service")
	}

	legacy := parseFile(t, "testdata/legacy.yaml").Graph
	if !hasEdge(legacy, "ingress/shop/old-storefront", "service/shop/checkout", "routes") {
		t.Error("the v1beta1 Ingress did not reach its backend Service")
	}
}

// Everything a pod needs to start is named in its template: config, secrets,
// storage, identity. Each of these is a way for a deletion elsewhere to break
// this workload, which is what an iac_ref edge is for.
func TestPodTemplateReferencesWhatItNeeds(t *testing.T) {
	g := parseFile(t, "testdata/shop.yaml").Graph
	const from = "deployment/shop/checkout"

	for _, want := range []struct{ to, relation string }{
		{"configmap/shop/checkout-config", "reads"}, // envFrom
		{"configmap/shop/flags", "reads"},           // env valueFrom
		{"secret/shop/checkout-db", "reads"},        // env valueFrom
		{"secret/shop/checkout-tls", "reads"},       // volume
		{"secret/shop/registry", "reads"},           // imagePullSecrets
		{"persistentvolumeclaim/shop/uploads", "mounts"},
		{"serviceaccount/shop/checkout", "runs-as"},
	} {
		if !hasEdge(g, from, want.to, want.relation) {
			t.Errorf("missing %s -> %s (%s)", from, want.to, want.relation)
		}
	}
}

// Every pod runs as some ServiceAccount, so drawing the implicit default one
// would add an arrow per workload that says nothing about that workload.
func TestImplicitDefaultServiceAccountIsLeftOut(t *testing.T) {
	g := parseFile(t, "testdata/shop.yaml").Graph

	if hasEdge(g, "cronjob/shop/nightly-reconcile", "serviceaccount/shop/default", "runs-as") {
		t.Error("the implicit default ServiceAccount was drawn")
	}
}

// A CronJob keeps its pod template two levels deeper than every other
// workload. Missing that path loses the references of every scheduled job in
// the input, silently, because the CronJob itself still becomes a box.
func TestCronJobTemplateIsRead(t *testing.T) {
	g := parseFile(t, "testdata/shop.yaml").Graph

	if !hasEdge(g, "cronjob/shop/nightly-reconcile", "secret/shop/checkout-db", "reads") {
		t.Error("the CronJob's nested pod template was not read")
	}
}

// A Deployment that mounts a Secret nobody committed is the most useful thing
// this graph can show. It can only show it if the missing end has a box to
// point at, marked as something the input only declared.
func TestMissingTargetBecomesADeclaredOnlyNode(t *testing.T) {
	g := parseFile(t, "testdata/shop.yaml").Graph

	n, ok := g.Node("secret/shop/checkout-db")
	if !ok {
		t.Fatal("the referenced but absent Secret has no node")
	}
	if n.Attrs["declared_only"] != true {
		t.Error("an absent object is not marked as declared only")
	}
	if present, _ := g.Node("configmap/shop/checkout-config"); present != nil {
		if present.Attrs["declared_only"] == true {
			t.Error("an object that is in the input was marked as absent")
		}
	}
}

// A Namespace is a container, and containers are not nodes. Emitting both
// would put the same namespace on the diagram twice, once as a box inside
// itself.
func TestNamespaceBecomesAGroupNotANode(t *testing.T) {
	g := parseFile(t, "testdata/shop.yaml").Graph

	if _, ok := g.Group("namespace:shop"); !ok {
		t.Fatal("the Namespace did not become a container")
	}
	if _, ok := g.Node("namespace/shop"); ok {
		t.Error("the Namespace became a node as well as a container")
	}
	dep, _ := g.Node("deployment/shop/checkout")
	if got := dep.GroupOn(core.AxisNetwork); got != "namespace:shop" {
		t.Errorf("workload placement = %q, want the namespace", got)
	}
}

// An apiVersion no current cluster serves is a finding. Dropping the object
// turns that finding into an absence, which reads as "there is nothing here".
func TestRemovedAPIVersionIsDrawnAndFlagged(t *testing.T) {
	res := parseFile(t, "testdata/legacy.yaml")

	n, ok := res.Graph.Node("cronjob/shop/sweep")
	if !ok {
		t.Fatal("an object on a removed apiVersion was dropped")
	}
	if n.Attrs["api_removed_in"] != "1.25" {
		t.Errorf("api_removed_in = %v, want the release that stopped serving it", n.Attrs["api_removed_in"])
	}
	if len(res.Removed) != 2 {
		t.Errorf("Removed = %v, want both legacy objects", res.Removed)
	}
}

// A kind this parser has never heard of still belongs on the diagram. A
// cluster full of custom resources would otherwise render as the handful of
// objects this table happens to know.
func TestUnknownAPIVersionStillDraws(t *testing.T) {
	res := parseFile(t, "testdata/legacy.yaml")

	n, ok := res.Graph.Node("widget/shop/sprocket")
	if !ok {
		t.Fatal("an unrecognised kind was dropped")
	}
	if n.Attrs["api_unknown"] != true {
		t.Error("an unrecognised kind is not marked as unrecognised")
	}
	if len(res.Unknown) != 1 {
		t.Errorf("Unknown = %v, want the one custom resource", res.Unknown)
	}
}

// The oldest cluster that can accept a document set is decided by its newest
// apiVersion. Reporting anything else tells somebody a manifest will apply to
// a cluster that will refuse it.
func TestMinimumReleaseIsTheNewestAPIVersion(t *testing.T) {
	res := parseFile(t, "testdata/shop.yaml")

	// autoscaling/v2 arrived in 1.23, later than everything else here.
	if res.MinimumRelease != "1.23" {
		t.Errorf("MinimumRelease = %q, want 1.23", res.MinimumRelease)
	}
	if res.Graph.Metadata.SourceVersion != "1.23" {
		t.Errorf("metadata source version = %q", res.Graph.Metadata.SourceVersion)
	}
}

// A ClusterRole, a PersistentVolume and a CustomResourceDefinition do not live
// in a namespace. Giving them the default one files them under a namespace
// that never contained them, and the diagram shows it as fact.
func TestClusterScopedObjectsGetNoNamespace(t *testing.T) {
	raw, err := os.ReadFile("testdata/cluster-scoped.yaml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(raw, Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := res.Graph

	// PersistentVolume is in the table as cluster-scoped; ClusterRole is not
	// in the table at all, and an unknown kind with no namespace of its own
	// must not be assumed into one either.
	for _, id := range []string{"persistentvolume/archive", "clusterrole/reader"} {
		n, ok := g.Node(id)
		if !ok {
			t.Fatalf("%s is missing: a cluster-scoped object was filed under a namespace", id)
		}
		if n.Groups[core.AxisNetwork] != "" {
			t.Errorf("%s was placed in %q", id, n.Groups[core.AxisNetwork])
		}
		if n.Attrs["namespace"] != nil {
			t.Errorf("%s carries a namespace it does not have", id)
		}
	}
	if _, ok := g.Group("namespace:default"); ok {
		t.Error("a default namespace was invented for objects that have none")
	}
}

// A projected volume holds the same references one level further in, and names
// its Secret `name` rather than `secretName`. A workload that mounts its config
// this way depends on it exactly as much as one that does not.
func TestProjectedVolumeReferencesAreRead(t *testing.T) {
	raw, err := os.ReadFile("testdata/cluster-scoped.yaml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(raw, Options{})
	if err != nil {
		t.Fatal(err)
	}
	const from = "deployment/shop/checkout"
	if !hasEdge(res.Graph, from, "configmap/shop/trust-bundle", "reads") {
		t.Error("a ConfigMap projected into a volume was not read")
	}
	if !hasEdge(res.Graph, from, "secret/shop/signing-key", "reads") {
		t.Error("a Secret projected into a volume was not read")
	}
}

// The releases that accept a document set are [floor, ceiling): the newest
// apiVersion sets the floor, the earliest removal sets the ceiling. When they
// cross, no cluster runs all of it, and naming the floor anyway would promise
// a release that will refuse half the input.
func TestNoReleaseServesEveryAPIVersion(t *testing.T) {
	res := parseFile(t, "testdata/mixed-eras.yaml")

	if !res.Incompatible {
		t.Fatal("a set no release serves was reported as compatible")
	}
	if res.MinimumRelease != "" || res.Graph.Metadata.SourceVersion != "" {
		t.Errorf("a minimum release was reported for a set that has none: %q", res.MinimumRelease)
	}
	if res.Floor != "1.23" || res.Ceiling != "1.22" {
		t.Errorf("floor/ceiling = %q/%q, want 1.23/1.22", res.Floor, res.Ceiling)
	}
}

// `kubectl get -o yaml` returns a List when it is asked for more than one
// object, and ownerReferences are the only thing that puts a live ReplicaSet
// under the Deployment that made it.
func TestListIsFlattenedAndOwnersAreRead(t *testing.T) {
	g := parseFile(t, "testdata/list.yaml").Graph

	if _, ok := g.Node("replicaset/shop/checkout-7d9f"); !ok {
		t.Fatal("the List was not unwrapped")
	}
	if !hasEdge(g, "replicaset/shop/checkout-7d9f", "deployment/shop/checkout", "owned-by") {
		t.Error("the ownerReference did not become an edge")
	}
}

// A stream with nothing Kubernetes-shaped in it must say so. Returning an
// empty graph would let a wrong path render as an estate with nothing in it.
func TestNonManifestInputIsRefused(t *testing.T) {
	if _, err := Parse([]byte("just: a map\n"), Options{}); err == nil {
		t.Error("a document with no apiVersion parsed as manifests")
	}
}
