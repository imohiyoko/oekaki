package terraform

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func parseDocument(t *testing.T, document map[string]any) *core.Graph {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	g, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return g
}

func TestSensitiveValuesNeverReachTheGraph(t *testing.T) {
	const secret = "secret-instance-class"
	document := map[string]any{
		"format_version": "1.2",
		"planned_values": map[string]any{"root_module": map[string]any{"resources": []any{
			map[string]any{
				"address":       "aws_instance.app",
				"mode":          "managed",
				"type":          "aws_instance",
				"name":          "app",
				"provider_name": "registry.terraform.io/hashicorp/aws",
				"values": map[string]any{
					"instance_type": secret,
					"tags":          map[string]any{"Name": "app"},
				},
				"sensitive_values": map[string]any{"instance_type": true},
			},
		}}},
	}

	g := parseDocument(t, document)
	node, ok := g.Node("aws_instance.app")
	if !ok {
		t.Fatal("aws_instance.app is missing")
	}
	if _, ok := node.Attrs["instance_type"]; ok {
		t.Fatal("sensitive instance_type was copied into node attrs")
	}
	raw, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("sensitive value appears in serialized graph")
	}
}

func TestExplicitResourceIndexResolvesOnlyThatInstance(t *testing.T) {
	raw, err := os.ReadFile("testdata/count.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	configuration := document["configuration"].(map[string]any)
	root := configuration["root_module"].(map[string]any)
	for _, item := range root["resources"].([]any) {
		resource := item.(map[string]any)
		if resource["address"] != "aws_instance.web" {
			continue
		}
		expressions := resource["expressions"].(map[string]any)
		subnetID := expressions["subnet_id"].(map[string]any)
		subnetID["references"] = []any{"aws_subnet.public[0].id"}
	}

	g := parseDocument(t, document)
	node, ok := g.Node("aws_instance.web")
	if !ok {
		t.Fatal("aws_instance.web is missing")
	}
	if got, want := node.GroupOn(core.AxisNetwork), "aws_vpc.main/aws_subnet.public[0]"; got != want {
		t.Fatalf("network group = %q, want %q", got, want)
	}
}

func moduleInstanceDocument(addresses []string, cardinalityKey string, cardinality any) map[string]any {
	stateResource := func(address, typ, name string, values map[string]any) map[string]any {
		return map[string]any{
			"address":          address,
			"mode":             "managed",
			"type":             typ,
			"name":             name,
			"provider_name":    "registry.terraform.io/hashicorp/aws",
			"values":           values,
			"sensitive_values": map[string]any{},
		}
	}
	module := func(address string) map[string]any {
		prefix := address + "."
		return map[string]any{
			"address": address,
			"resources": []any{
				stateResource(prefix+"aws_subnet.app", "aws_subnet", "app", map[string]any{"cidr_block": "10.0.0.0/24"}),
				stateResource(prefix+"aws_instance.app", "aws_instance", "app", map[string]any{"instance_type": "t3.micro"}),
			},
		}
	}
	childModules := make([]any, 0, len(addresses))
	for _, address := range addresses {
		childModules = append(childModules, module(address))
	}
	moduleCall := map[string]any{
		"source": "./workload",
		"module": map[string]any{"resources": []any{
			map[string]any{
				"address": "aws_subnet.app", "mode": "managed", "type": "aws_subnet", "name": "app",
			},
			map[string]any{
				"address": "aws_instance.app", "mode": "managed", "type": "aws_instance", "name": "app",
				"expressions": map[string]any{"subnet_id": map[string]any{"references": []any{"aws_subnet.app.id"}}},
			},
		}},
	}
	moduleCall[cardinalityKey] = cardinality
	return map[string]any{
		"format_version": "1.2",
		"planned_values": map[string]any{"root_module": map[string]any{"child_modules": childModules}},
		"configuration": map[string]any{"root_module": map[string]any{"module_calls": map[string]any{
			"workload": moduleCall,
		}}},
	}
}

func TestReferencesStayInsideModuleInstances(t *testing.T) {
	tests := []struct {
		name           string
		addresses      []string
		cardinalityKey string
		cardinality    any
	}{
		{
			name:           "count",
			addresses:      []string{"module.workload[0]", "module.workload[1]"},
			cardinalityKey: "count_expression",
			cardinality:    map[string]any{"constant_value": 2},
		},
		{
			name:           "for_each",
			addresses:      []string{`module.workload["blue/team.with.dot"]`, `module.workload["blue/team.with.other"]`},
			cardinalityKey: "for_each_expression",
			cardinality:    map[string]any{"constant_value": map[string]any{"blue/team.with.dot": true, "blue/team.with.other": true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := parseDocument(t, moduleInstanceDocument(tt.addresses, tt.cardinalityKey, tt.cardinality))
			seenModuleGroups := map[string]bool{}
			for _, address := range tt.addresses {
				prefix := escapeAddress(address) + "."
				nodeID := prefix + "aws_instance.app"
				want := prefix + "aws_subnet.app"
				moduleID := core.AxisModule + ":" + escapeAddress(address)
				node, ok := g.Node(nodeID)
				if !ok {
					t.Fatalf("%s is missing", nodeID)
				}
				if got := node.GroupOn(core.AxisNetwork); got != want {
					t.Errorf("%s network group = %q, want %q", nodeID, got, want)
				}
				if got := node.GroupOn(core.AxisModule); got != moduleID {
					t.Errorf("%s module group = %q, want %q", nodeID, got, moduleID)
				}
				if _, ok := g.Group(moduleID); !ok {
					t.Errorf("module group %q is missing", moduleID)
				}
				if seenModuleGroups[moduleID] {
					t.Errorf("module instances collided on group %q", moduleID)
				}
				seenModuleGroups[moduleID] = true
			}
		})
	}
}

func TestForEachKeysWithSlashProduceSafeDistinctGroupIDs(t *testing.T) {
	resource := func(address, key string) map[string]any {
		return map[string]any{
			"address":          address,
			"mode":             "managed",
			"type":             "aws_subnet",
			"name":             "items",
			"index":            key,
			"provider_name":    "registry.terraform.io/hashicorp/aws",
			"values":           map[string]any{"cidr_block": "10.0.0.0/24"},
			"sensitive_values": map[string]any{},
		}
	}
	document := map[string]any{
		"format_version": "1.2",
		"planned_values": map[string]any{"root_module": map[string]any{"resources": []any{
			resource(`aws_subnet.items["a/b"]`, "a/b"),
			resource(`aws_subnet.items["a%2Fb"]`, "a%2Fb"),
		}}},
	}

	g := parseDocument(t, document)
	for _, id := range []string{`aws_subnet.items["a%2Fb"]`, `aws_subnet.items["a%252Fb"]`} {
		if strings.Contains(id, core.GroupSeparator) {
			t.Fatalf("test expected a safe id, got %q", id)
		}
		if _, ok := g.Group(id); !ok {
			t.Errorf("safe group %q is missing", id)
		}
	}
}
