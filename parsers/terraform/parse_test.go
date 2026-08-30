package terraform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/providers"
	"github.com/imohiyoko/oekaki/schema"
)

const examplePlan = "../../examples/three-tier/plan.json"

func parseFile(t *testing.T, path string, opts Options) *core.Graph {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	g, err := Parse(raw, opts)
	if err != nil {
		t.Fatalf("Parse(%s): %v", path, err)
	}
	return g
}

// The parser's output has to satisfy the published schema, not just the Go
// struct. This is the check that keeps the two from drifting apart.
func TestParsedGraphMatchesTheSchema(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	doc, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("parser output does not match the published schema: %v", err)
	}
}

func TestContainersBecomeGroupsNotNodes(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	for _, n := range g.Nodes {
		if providers.IsContainer(n.Type) {
			t.Errorf("%s is a container but was emitted as a node", n.ID)
		}
	}

	want := []string{
		"aws_subnet.private_a",
		"aws_subnet.private_c",
		"aws_subnet.public_a",
		"aws_subnet.public_c",
		"aws_vpc.main",
	}
	var got []string
	for _, grp := range g.Groups {
		// Only the network axis: the provider and module axes are derived
		// rather than read out of the configuration, and are checked separately.
		if grp.Axis == core.AxisNetwork {
			got = append(got, grp.ID)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

func TestSubnetsNestUnderTheirVPC(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	for _, grp := range g.Groups {
		if grp.Type != "subnet" {
			continue
		}
		if grp.Parent == nil {
			t.Errorf("%s has no parent VPC", grp.ID)
			continue
		}
		if *grp.Parent != "aws_vpc.main" {
			t.Errorf("%s parent = %q, want aws_vpc.main", grp.ID, *grp.Parent)
		}
	}
}

// Placement is the parser's most opinionated behaviour, so it is pinned here
// rather than left to be discovered from a picture.
func TestNodePlacement(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	want := map[string]string{
		// Names one subnet directly, so it lands in that subnet.
		"aws_instance.bastion": "aws_vpc.main/aws_subnet.public_a",
		// Spans two subnets, so it lands in the VPC that holds both.
		"aws_lb.public":       "aws_vpc.main",
		"aws_ecs_service.api": "aws_vpc.main",
		// Reaches subnets one hop out, through the DB subnet group.
		"aws_db_instance.main": "aws_vpc.main",
		// Regional resources genuinely live outside the VPC.
		"aws_ecs_cluster.main":        "",
		"aws_ecs_task_definition.api": "",
	}

	for id, wantGroup := range want {
		n, ok := g.Node(id)
		if !ok {
			t.Errorf("%s is missing from the graph", id)
			continue
		}
		if n.GroupOn(core.AxisNetwork) != wantGroup {
			t.Errorf("%s group = %q, want %q", id, n.GroupOn(core.AxisNetwork), wantGroup)
		}
	}
}

func TestContainmentIsNotAlsoDrawnAsAnEdge(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	groups := map[string]bool{}
	for _, grp := range g.Groups {
		groups[grp.ID] = true
	}
	for _, e := range g.Edges {
		if groups[e.To] {
			t.Errorf("edge %s -> %s duplicates containment", e.From, e.To)
		}
	}
}

func TestReferencesBecomeEdges(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	want := map[string]string{
		"aws_ecs_service.api|aws_ecs_cluster.main":         "cluster",
		"aws_ecs_service.api|aws_security_group.app":       "network_configuration",
		"aws_ecs_service.api|aws_lb_target_group.api":      "load_balancer",
		"aws_db_instance.main|aws_db_subnet_group.main":    "db_subnet_group_name",
		"aws_lb_listener.http|aws_lb.public":               "load_balancer_arn",
		"aws_security_group.db|aws_security_group.bastion": "ingress",
	}

	got := map[string]string{}
	for _, e := range g.Edges {
		if e.Kind != core.EdgeIACRef {
			t.Errorf("unexpected edge kind %q in a v0.1 graph", e.Kind)
		}
		attr, _ := e.Attrs["attribute"].(string)
		got[e.From+"|"+e.To] = attr
	}

	for key, wantAttr := range want {
		attr, ok := got[key]
		if !ok {
			t.Errorf("missing edge %s", key)
			continue
		}
		if attr != wantAttr {
			t.Errorf("edge %s attribute = %q, want %q", key, attr, wantAttr)
		}
	}
}

// Nested blocks are where a naive expression walker stops early: an ECS
// service's subnets and security groups live inside network_configuration.
func TestReferencesInsideNestedBlocksAreFound(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	n, ok := g.Node("aws_ecs_service.api")
	if !ok {
		t.Fatal("aws_ecs_service.api is missing")
	}
	// Its subnets are only reachable through a nested block, so a service
	// placed in the VPC proves the walker descended into one.
	if n.GroupOn(core.AxisNetwork) != "aws_vpc.main" {
		t.Errorf("group = %q; nested-block references were not followed", n.GroupOn(core.AxisNetwork))
	}
}

func TestSourceLocationsAreRecorded(t *testing.T) {
	g := parseFile(t, examplePlan, Options{SourceDir: "../../examples/three-tier"})

	n, ok := g.Node("aws_ecs_service.api")
	if !ok {
		t.Fatal("aws_ecs_service.api is missing")
	}
	if n.Source == nil {
		t.Fatal("no source location recorded")
	}
	if n.Source.File != "main.tf" || n.Source.Line <= 0 {
		t.Errorf("source = %+v, want main.tf with a positive line", n.Source)
	}

	// The recorded line should genuinely hold the declaration.
	raw, err := os.ReadFile(filepath.Join("../../examples/three-tier", n.Source.File))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	decl := lines[n.Source.Line-1]
	if !strings.Contains(decl, `resource "aws_ecs_service" "api"`) {
		t.Errorf("line %d is %q, which is not the declaration", n.Source.Line, decl)
	}
}

func TestSourceLocationsAreOptional(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	for _, n := range g.Nodes {
		if n.Source != nil {
			t.Errorf("%s has a source location but none was asked for", n.ID)
		}
	}
}

// Determinism is a stated design requirement: users are meant to commit
// generated graphs and review them as diffs.
func TestParsingIsDeterministic(t *testing.T) {
	raw, err := os.ReadFile(examplePlan)
	if err != nil {
		t.Fatal(err)
	}

	first, err := Parse(raw, Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := first.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}

	// Ten runs, because the failure mode is Go's randomised map iteration
	// order and a single repeat can easily miss it.
	for i := range 10 {
		next, err := Parse(raw, Options{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := next.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Fatalf("run %d produced different bytes than run 0", i+1)
		}
	}
}

func TestCountInstancesResolve(t *testing.T) {
	g := parseFile(t, "testdata/count.json", Options{})

	// Both instances of the counted subnet must become groups.
	for _, id := range []string{"aws_subnet.public[0]", "aws_subnet.public[1]"} {
		if _, ok := g.Group(id); !ok {
			t.Errorf("group %s is missing", id)
		}
	}

	// `aws_subnet.public` in a reference names both instances, so the
	// instance that points at it belongs to their common parent.
	n, ok := g.Node("aws_instance.web")
	if !ok {
		t.Fatal("aws_instance.web is missing")
	}
	if n.GroupOn(core.AxisNetwork) != "aws_vpc.main" {
		t.Errorf("group = %q, want aws_vpc.main", n.GroupOn(core.AxisNetwork))
	}
}

func TestStateInputIsAccepted(t *testing.T) {
	g := parseFile(t, "testdata/state.json", Options{})

	if _, ok := g.Group("aws_vpc.main"); !ok {
		t.Error("the VPC did not become a group")
	}
	if _, ok := g.Node("aws_instance.bastion"); !ok {
		t.Error("the bastion did not become a node")
	}
}

// A state file has no configuration block, so references have to come out of
// the attribute values instead.
func TestStateDependenciesAreInferredFromValues(t *testing.T) {
	g := parseFile(t, "testdata/state.json", Options{})

	n, ok := g.Node("aws_instance.bastion")
	if !ok {
		t.Fatal("aws_instance.bastion is missing")
	}
	if want := "aws_vpc.main/aws_subnet.private_a"; n.GroupOn(core.AxisNetwork) != want {
		t.Errorf("group = %q, want %q; subnet_id was not matched to the subnet", n.GroupOn(core.AxisNetwork), want)
	}

	var found bool
	for _, e := range g.Edges {
		if e.From == "aws_instance.bastion" && e.To == "aws_security_group.db" {
			found = true
		}
	}
	if !found {
		t.Error("vpc_security_group_ids did not produce an edge to the security group")
	}

	// The subnet group is named "main-private" and the database refers to it
	// by that name, which is the only thread connecting them in a state file.
	found = false
	for _, e := range g.Edges {
		if e.From == "aws_db_instance.main" && e.To == "aws_db_subnet_group.main" {
			found = true
		}
	}
	if !found {
		t.Error("db_subnet_group_name did not produce an edge to the subnet group")
	}
}

// An instance tagged Name="bastion" must not appear to depend on every
// resource that happens to be called "bastion".
func TestTagsDoNotProduceEdges(t *testing.T) {
	resources := []resource{
		{address: "aws_instance.a", typ: "aws_instance", name: "a", values: map[string]any{
			"id":   "i-0123456789abcdef0",
			"tags": map[string]any{"Name": "shared-name-value"},
		}},
		{address: "aws_security_group.b", typ: "aws_security_group", name: "b", values: map[string]any{
			"id":   "sg-0123456789abcdef0",
			"name": "shared-name-value",
		}},
	}

	if deps := inferDependencies(resources); len(deps["aws_instance.a"]) != 0 {
		t.Errorf("a tag produced dependencies: %+v", deps["aws_instance.a"])
	}
}

// If two resources claim the same identifier, neither is a safe target.
func TestAmbiguousIdentifiersAreDropped(t *testing.T) {
	resources := []resource{
		{address: "aws_s3_bucket.a", typ: "aws_s3_bucket", name: "a", values: map[string]any{"name": "duplicated-name"}},
		{address: "aws_iam_role.b", typ: "aws_iam_role", name: "b", values: map[string]any{"name": "duplicated-name"}},
		{address: "aws_instance.c", typ: "aws_instance", name: "c", values: map[string]any{"user_data": "duplicated-name"}},
	}

	if deps := inferDependencies(resources); len(deps["aws_instance.c"]) != 0 {
		t.Errorf("an ambiguous identifier produced dependencies: %+v", deps["aws_instance.c"])
	}
}

func TestDataSourcesAreExcludedByDefault(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	for _, n := range g.Nodes {
		if strings.HasPrefix(n.ID, "data.") {
			t.Errorf("%s is a data source and should not be drawn by default", n.ID)
		}
	}
}

func TestAttributesAreCurated(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	n, ok := g.Node("aws_db_instance.main")
	if !ok {
		t.Fatal("aws_db_instance.main is missing")
	}
	if n.Attrs["instance_class"] != "db.t4g.medium" {
		t.Errorf("instance_class = %v, want db.t4g.medium", n.Attrs["instance_class"])
	}
	// tags_all churns for reasons unrelated to the diagram, so it is dropped.
	if _, present := n.Attrs["tags_all"]; present {
		t.Error("tags_all leaked into attrs")
	}
}

func TestLabelPrefersTheNameTag(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	grp, ok := g.Group("aws_subnet.private_a")
	if !ok {
		t.Fatal("aws_subnet.private_a is missing")
	}
	if grp.Label != "private-a" {
		t.Errorf("label = %q, want %q (the Name tag, not the Terraform name)", grp.Label, "private-a")
	}
}

func TestUnusableInputIsReportedClearly(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantSub string
	}{
		{"not json", "hello", "does not look like"},
		{"json but not terraform", `{"hello":"world"}`, "planned_values"},
		{"empty plan", `{"format_version":"1.2","planned_values":{"root_module":{}}}`, "no managed resources"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input), Options{})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not mention %q", err, tt.wantSub)
			}
		})
	}
}
