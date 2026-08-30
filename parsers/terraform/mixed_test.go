package terraform

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// testdata/mixed-estate.json is the shape of a real enterprise: vSphere and
// Kubernetes on premises, plus AWS and Azure, all described by one Terraform
// run, with references crossing between them.

// A single reference across a provider boundary used to be enough to relocate a
// resource into another cloud. The on-premises ERP virtual machine reads from
// an RDS instance, and that one edge placed the machine inside an AWS subnet.
//
// Referencing something is not the same as living inside it, and across a
// provider boundary the two come apart completely.
func TestContainmentStopsAtProviderBoundaries(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	vm, ok := g.Node("vsphere_virtual_machine.legacy_erp")
	if !ok {
		t.Fatal("vsphere_virtual_machine.legacy_erp is missing")
	}
	if strings.Contains(vm.GroupOn(core.AxisNetwork), "aws_") {
		t.Errorf("an on-premises VM was placed inside AWS infrastructure: %q", vm.GroupOn(core.AxisNetwork))
	}
}

// The dependency itself must survive. Only containment is refused: losing the
// edge would hide that an on-premises system depends on a cloud database, which
// is exactly the kind of thing someone reads this diagram to find out.
func TestCrossProviderDependenciesAreStillDrawn(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	for _, e := range g.Edges {
		if e.From == "vsphere_virtual_machine.legacy_erp" && e.To == "aws_db_instance.orders" {
			return
		}
	}
	t.Error("the cross-provider dependency edge was dropped")
}

// The boundary rule is a stop sign, not a general ban on nesting.
func TestContainmentStillWorksWithinAProvider(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	db, ok := g.Node("aws_db_instance.orders")
	if !ok {
		t.Fatal("aws_db_instance.orders is missing")
	}
	if db.GroupOn(core.AxisNetwork) == "" {
		t.Error("the RDS instance lost its placement inside the VPC")
	}
}

func TestProviderOf(t *testing.T) {
	tests := map[string]string{
		"registry.terraform.io/hashicorp/aws":      "aws",
		"registry.terraform.io/hashicorp/vsphere":  "vsphere",
		"registry.terraform.io/-/aws":              "aws",
		"registry.example.com/acme/internal-cloud": "internal-cloud",
		"aws": "aws",
		"":    "",
	}

	for in, want := range tests {
		if got := providerOf(in); got != want {
			t.Errorf("providerOf(%q) = %q, want %q", in, got, want)
		}
	}
}
