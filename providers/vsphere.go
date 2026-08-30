package providers

func init() { Register(vsphere) }

// vSphere is on-premises, and its hierarchy is physical rather than logical:
// a datacenter holds compute clusters, which hold hosts, which run virtual
// machines. Datastores and networks hang off the datacenter alongside the
// clusters.
//
// This is the provider that shows why containment could not stay AWS-shaped.
// There is no VPC here and never will be, but there is a perfectly good
// three-level hierarchy that renders exactly like one once it is declared.
var vsphere = &Profile{
	Name:     "vsphere",
	Prefixes: []string{"vsphere_"},

	Containers: map[string]Container{
		"vsphere_datacenter":      {Type: "datacenter"},
		"vsphere_compute_cluster": {Type: "cluster"},
		"vsphere_resource_pool":   {Type: "resource_pool"},
		"vsphere_folder":          {Type: "folder"},
	},

	// A datastore is mounted by every machine in a cluster, so following one
	// would say more about storage than about where a machine runs.
	Attachments: map[string]bool{
		"vsphere_datastore":         true,
		"vsphere_datastore_cluster": true,
	},

	Attrs: map[string][]string{
		"vsphere_datacenter":                 {"name"},
		"vsphere_compute_cluster":            {"drs_enabled", "ha_enabled"},
		"vsphere_resource_pool":              {"name"},
		"vsphere_virtual_machine":            {"guest_id", "memory", "num_cpus"},
		"vsphere_host":                       {"hostname"},
		"vsphere_datastore":                  {"name"},
		"vsphere_distributed_virtual_switch": {"version"},
		"vsphere_distributed_port_group":     {"vlan_id"},
	},

	Categories: map[string]Category{
		"vsphere_virtual_machine": Compute,
		"vsphere_host":            Compute,
		"vsphere_resource_pool":   Compute,

		"vsphere_distributed_virtual_switch": Network,
		"vsphere_distributed_port_group":     Network,
		"vsphere_host_port_group":            Network,

		"vsphere_datastore":         Storage,
		"vsphere_datastore_cluster": Storage,
		"vsphere_virtual_disk":      Storage,

		"vsphere_entity_permissions": Security,
		"vsphere_role":               Security,
	},

	Highlights: []string{
		"vsphere_datacenter",
		"vsphere_compute_cluster",
		"vsphere_virtual_machine",
	},
}
