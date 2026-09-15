package terraform

import (
	"fmt"
	"testing"
)

// planWith wraps one resource's values in the smallest plan document that
// parses, so a case is the values it is about and nothing else.
func planWith(t *testing.T, typ, values string) string {
	t.Helper()
	return fmt.Sprintf(`{
      "format_version": "1.0",
      "planned_values": { "root_module": { "resources": [
        { "address": "%[1]s.api", "mode": "managed", "type": "%[1]s", "name": "api",
          "provider_name": "registry.terraform.io/hashicorp/aws", "values": %[2]s }
      ] } }
    }`, typ, values)
}

func imageOf(t *testing.T, doc string) (string, bool) {
	t.Helper()
	g, err := Parse([]byte(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("got %d nodes", len(g.Nodes))
	}
	image, ok := g.Nodes[0].Attrs["image"].(string)
	return image, ok
}

// The one field this reads, out of the document AWS uses to say what a task
// runs. It is the identifier a build record shares with what is running.
func TestATaskDefinitionSaysWhichImageItRuns(t *testing.T) {
	doc := planWith(t, "aws_ecs_task_definition", `{
      "family": "api",
      "container_definitions": "[{\"name\":\"api\",\"image\":\"registry.example/checkout:1.4.0\",\"cpu\":256}]"
    }`)
	image, ok := imageOf(t, doc)
	if !ok || image != "registry.example/checkout:1.4.0" {
		t.Fatalf("image = %q, %v", image, ok)
	}
}

// Nothing else is taken out of it. This is a join key, not a model of a
// container.
func TestNothingElseIsTakenOutOfTheContainerDefinition(t *testing.T) {
	doc := planWith(t, "aws_ecs_task_definition", `{
      "family": "api",
      "container_definitions": "[{\"name\":\"api\",\"image\":\"img:1\",\"cpu\":256,\"portMappings\":[{\"containerPort\":8080}]}]"
    }`)
	g, err := Parse([]byte(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"cpu_reservation", "portMappings", "ports", "containers", "container_definitions"} {
		if _, ok := g.Nodes[0].Attrs[unwanted]; ok {
			t.Errorf("%q was carried into the IR", unwanted)
		}
	}
}

// A task with a sidecar has a second answer to "which repository is this", and
// there is nowhere to put it yet. The first is the same reading the Kubernetes
// parser makes of a pod.
func TestTheFirstContainerIsTheOneRead(t *testing.T) {
	doc := planWith(t, "aws_ecs_task_definition", `{
      "container_definitions": "[{\"name\":\"app\",\"image\":\"img:1\"},{\"name\":\"log\",\"image\":\"sidecar:2\"}]"
    }`)
	if image, _ := imageOf(t, doc); image != "img:1" {
		t.Errorf("image = %q", image)
	}
}

// Everything that cannot be read is absent rather than guessed at. An absent
// image is a join that does not happen, which is where this started.
func TestAnUnreadableContainerDefinitionLeavesNoImage(t *testing.T) {
	for _, tc := range []struct{ name, values string }{
		{"absent", `{ "family": "api" }`},
		{"unknown in a plan", `{ "family": "api", "container_definitions": null }`},
		{"empty", `{ "container_definitions": "" }`},
		{"not JSON", `{ "container_definitions": "${jsonencode(local.containers)}" }`},
		{"not an array", `{ "container_definitions": "{\"image\":\"img:1\"}" }`},
		{"no containers", `{ "container_definitions": "[]" }`},
		{"no image in it", `{ "container_definitions": "[{\"name\":\"app\"}]" }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if image, ok := imageOf(t, planWith(t, "aws_ecs_task_definition", tc.values)); ok {
				t.Errorf("image = %q, want none", image)
			}
		})
	}
}

// A type whose profile says nothing about containers is not searched for one.
func TestATypeWithNoContainersIsLeftAlone(t *testing.T) {
	doc := planWith(t, "aws_instance", `{ "ami": "ami-1", "instance_type": "t3.micro",
      "container_definitions": "[{\"image\":\"img:1\"}]" }`)
	if image, ok := imageOf(t, doc); ok {
		t.Errorf("image = %q, want none", image)
	}
}
