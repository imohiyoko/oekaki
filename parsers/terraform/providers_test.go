package terraform

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// Before these profiles existed, only AWS nested. Every other provider's
// hierarchy degraded into arrows, so the same concept — a container holding
// resources — was drawn two different ways depending on who made the cloud.

func TestVSphereNestsDatacenterClusterMachine(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	dc, ok := g.Group("vsphere_datacenter.dc1")
	if !ok {
		t.Fatal("the vSphere datacenter did not become a container")
	}
	if dc.Type != "datacenter" {
		t.Errorf("datacenter group type = %q", dc.Type)
	}

	cluster, ok := g.Group("vsphere_compute_cluster.prod")
	if !ok {
		t.Fatal("the compute cluster did not become a container")
	}
	if cluster.Parent == nil || *cluster.Parent != dc.ID {
		t.Errorf("cluster parent = %v, want %q", cluster.Parent, dc.ID)
	}

	vm, ok := g.Node("vsphere_virtual_machine.legacy_erp")
	if !ok {
		t.Fatal("the virtual machine is missing")
	}
	want := "vsphere_datacenter.dc1/vsphere_compute_cluster.prod"
	if got := vm.GroupOn(core.AxisNetwork); got != want {
		t.Errorf("VM placement = %q, want %q", got, want)
	}
}

// Kubernetes names its namespace inside a metadata block rather than in a
// top-level attribute, so this also checks the expression walker descends.
func TestKubernetesNestsByNamespace(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	ns, ok := g.Group("kubernetes_namespace.payments")
	if !ok {
		t.Fatal("the namespace did not become a container")
	}
	if ns.Type != "namespace" {
		t.Errorf("namespace group type = %q", ns.Type)
	}

	dep, ok := g.Node("kubernetes_deployment.api")
	if !ok {
		t.Fatal("the deployment is missing")
	}
	if got := dep.GroupOn(core.AxisNetwork); got != ns.ID {
		t.Errorf("deployment placement = %q, want %q", got, ns.ID)
	}
}

// The case that made containers need an axis at all.
//
// An Azure virtual machine names its resource group directly and reaches its
// subnet only through a network interface. With resource groups on the network
// axis the nearer container would win, every machine would collapse into its
// resource group, and the subnets would be drawn empty.
func TestAzureVirtualMachineIsInBothItsSubnetAndItsResourceGroup(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	vm, ok := g.Node("azurerm_linux_virtual_machine.app")
	if !ok {
		t.Fatal("the Azure VM is missing")
	}

	wantNetwork := "azurerm_virtual_network.hub/azurerm_subnet.gw"
	if got := vm.GroupOn(core.AxisNetwork); got != wantNetwork {
		t.Errorf("network placement = %q, want %q", got, wantNetwork)
	}

	wantAccount := "azurerm_resource_group.shared"
	if got := vm.GroupOn(core.AxisAccount); got != wantAccount {
		t.Errorf("account placement = %q, want %q", got, wantAccount)
	}
}

// A resource group is an ownership boundary, not a network one, so it must not
// become the virtual network's parent even though the network references it.
func TestAzureResourceGroupIsNotANetworkParent(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	rg, ok := g.Group("azurerm_resource_group.shared")
	if !ok {
		t.Fatal("the resource group did not become a container")
	}
	if rg.Axis != core.AxisAccount {
		t.Errorf("resource group axis = %q, want %q", rg.Axis, core.AxisAccount)
	}

	vnet, ok := g.Group("azurerm_virtual_network.hub")
	if !ok {
		t.Fatal("the virtual network did not become a container")
	}
	if vnet.Axis != core.AxisNetwork {
		t.Errorf("virtual network axis = %q, want %q", vnet.Axis, core.AxisNetwork)
	}
	if vnet.Parent != nil {
		t.Errorf("virtual network parent = %q; a resource group is not a network parent", *vnet.Parent)
	}

	subnet, ok := g.Group("azurerm_subnet.gw")
	if !ok {
		t.Fatal("the subnet did not become a container")
	}
	if subnet.Parent == nil || *subnet.Parent != vnet.ID {
		t.Errorf("subnet parent = %v, want %q", subnet.Parent, vnet.ID)
	}
}

// The account axis is only declared when something is actually on it, so a
// pure-AWS graph does not advertise a view with nothing in it.
func TestAccountAxisAppearsOnlyWhenUsed(t *testing.T) {
	if g := parseFile(t, examplePlan, Options{}); g.HasAxis(core.AxisAccount) {
		t.Error("an AWS-only graph declared an account axis with nothing on it")
	}
	if g := parseFile(t, "testdata/mixed-estate.json", Options{}); !g.HasAxis(core.AxisAccount) {
		t.Error("an estate with Azure resource groups has no account axis")
	}
}

func TestGCPNestsSubnetworkInsideNetwork(t *testing.T) {
	g := parseFile(t, "testdata/gcp.json", Options{})

	network, ok := g.Group("google_compute_network.main")
	if !ok {
		t.Fatal("the GCP network did not become a container")
	}
	subnet, ok := g.Group("google_compute_subnetwork.app")
	if !ok {
		t.Fatal("the subnetwork did not become a container")
	}
	if subnet.Parent == nil || *subnet.Parent != network.ID {
		t.Errorf("subnetwork parent = %v, want %q", subnet.Parent, network.ID)
	}

	vm, ok := g.Node("google_compute_instance.api")
	if !ok {
		t.Fatal("the GCP instance is missing")
	}
	want := "google_compute_network.main/google_compute_subnetwork.app"
	if got := vm.GroupOn(core.AxisNetwork); got != want {
		t.Errorf("instance placement = %q, want %q", got, want)
	}
}

// A firewall rule is attached to the network, so walking through one would
// pull instances out of their subnetwork — the same trap AWS security groups
// set.
func TestGCPFirewallIsNotWalkedThrough(t *testing.T) {
	g := parseFile(t, "testdata/gcp.json", Options{})

	// The SQL instance reaches the network only via its private_network
	// reference, so it belongs at network level, not inside a subnetwork.
	db, ok := g.Node("google_sql_database_instance.main")
	if !ok {
		t.Fatal("the SQL instance is missing")
	}
	if got := db.GroupOn(core.AxisNetwork); got != "google_compute_network.main" {
		t.Errorf("SQL instance placement = %q, want the network", got)
	}
}
