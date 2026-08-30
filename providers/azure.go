package providers

import "github.com/imohiyoko/oekaki/core"

func init() { Register(azure) }

// Azure is the provider that made containers need an axis.
//
// A resource group holds a virtual network, but is not part of one. It is an
// ownership and lifecycle boundary that cuts across network topology: one
// resource group routinely holds resources in several networks, and one network
// is routinely used by resources in several resource groups.
//
// Placed on the network axis it would quietly win. A virtual machine names its
// resource group directly and reaches its subnet only through a network
// interface, so the nearer container would take it and every machine would
// collapse into its resource group with the subnets left empty. On the account
// axis it answers "who owns this" while the network axis still answers "what is
// this attached to".
var azure = &Profile{
	Name:     "azurerm",
	Prefixes: []string{"azurerm_"},

	Containers: map[string]Container{
		"azurerm_resource_group":  {Type: "resource_group", Axis: core.AxisAccount},
		"azurerm_virtual_network": {Type: "vpc"},
		"azurerm_subnet":          {Type: "subnet"},
	},

	// Network security groups play the role AWS security groups do: attached
	// to everything, pointing back at the resource group, useless for working
	// out where a resource actually sits.
	Attachments: map[string]bool{
		"azurerm_network_security_group": true,
	},

	Attrs: map[string][]string{
		"azurerm_resource_group":             {"location"},
		"azurerm_virtual_network":            {"address_space", "location"},
		"azurerm_subnet":                     {"address_prefixes"},
		"azurerm_network_security_group":     {"location"},
		"azurerm_linux_virtual_machine":      {"location", "size"},
		"azurerm_windows_virtual_machine":    {"location", "size"},
		"azurerm_virtual_machine_scale_set":  {"location", "sku"},
		"azurerm_kubernetes_cluster":         {"kubernetes_version", "location", "sku_tier"},
		"azurerm_postgresql_flexible_server": {"sku_name", "version"},
		"azurerm_mssql_database":             {"sku_name"},
		"azurerm_storage_account":            {"account_replication_type", "account_tier"},
		"azurerm_lb":                         {"location", "sku"},
		"azurerm_application_gateway":        {"location"},
		"azurerm_redis_cache":                {"capacity", "family", "sku_name"},
		"azurerm_linux_function_app":         {"location"},
	},

	Categories: map[string]Category{
		"azurerm_lb":                  Network,
		"azurerm_application_gateway": Network,
		"azurerm_public_ip":           Network,
		"azurerm_network_interface":   Network,
		"azurerm_route_table":         Network,
		"azurerm_private_endpoint":    Network,

		"azurerm_linux_virtual_machine":     Compute,
		"azurerm_windows_virtual_machine":   Compute,
		"azurerm_virtual_machine_scale_set": Compute,
		"azurerm_kubernetes_cluster":        Compute,
		"azurerm_linux_function_app":        Compute,
		"azurerm_container_group":           Compute,

		"azurerm_postgresql_flexible_server": Database,
		"azurerm_mysql_flexible_server":      Database,
		"azurerm_mssql_database":             Database,
		"azurerm_mssql_server":               Database,
		"azurerm_cosmosdb_account":           Database,
		"azurerm_redis_cache":                Database,

		"azurerm_network_security_group": Security,
		"azurerm_key_vault":              Security,
		"azurerm_user_assigned_identity": Security,
		"azurerm_role_assignment":        Security,

		"azurerm_storage_account":   Storage,
		"azurerm_managed_disk":      Storage,
		"azurerm_storage_container": Storage,
	},

	Highlights: []string{
		"azurerm_resource_group",
		"azurerm_virtual_network",
		"azurerm_subnet",
		"azurerm_network_security_group",
		"azurerm_linux_virtual_machine",
		"azurerm_kubernetes_cluster",
		"azurerm_postgresql_flexible_server",
	},
}
