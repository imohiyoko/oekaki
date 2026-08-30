package providers

func init() { Register(google) }

// Google Cloud's network model differs from AWS in a way that does not affect
// the drawing: a VPC network is global rather than regional, and subnetworks
// are regional. The nesting is the same shape either way — subnetworks inside
// a network — so the same "vpc"/"subnet" vocabulary is reused rather than
// inventing provider-specific words a reader would have to learn.
var google = &Profile{
	Name:     "google",
	Prefixes: []string{"google_"},

	Containers: map[string]Container{
		"google_compute_network":    {Type: "vpc"},
		"google_compute_subnetwork": {Type: "subnet"},
	},

	// A firewall rule is attached to the network, so every resource that
	// references one has a path straight to the top. Walking it would move
	// instances out of their subnetworks, exactly as a security group does
	// on AWS.
	Attachments: map[string]bool{
		"google_compute_firewall": true,
	},

	Attrs: map[string][]string{
		"google_compute_network":         {"auto_create_subnetworks", "routing_mode"},
		"google_compute_subnetwork":      {"ip_cidr_range", "region"},
		"google_compute_firewall":        {"description", "direction", "priority"},
		"google_compute_instance":        {"machine_type", "zone"},
		"google_compute_instance_group":  {"zone"},
		"google_container_cluster":       {"location", "node_version"},
		"google_container_node_pool":     {"node_count"},
		"google_sql_database_instance":   {"database_version", "region"},
		"google_redis_instance":          {"memory_size_gb", "tier"},
		"google_cloud_run_service":       {"location"},
		"google_cloudfunctions_function": {"available_memory_mb", "runtime"},
		"google_storage_bucket":          {"location", "storage_class"},
		"google_compute_forwarding_rule": {"ip_protocol", "port_range"},
		"google_compute_backend_service": {"protocol"},
	},

	Categories: map[string]Category{
		"google_compute_forwarding_rule": Network,
		"google_compute_backend_service": Network,
		"google_compute_url_map":         Network,
		"google_compute_router":          Network,
		"google_compute_address":         Network,
		"google_dns_record_set":          Network,

		"google_compute_instance":        Compute,
		"google_compute_instance_group":  Compute,
		"google_container_cluster":       Compute,
		"google_container_node_pool":     Compute,
		"google_cloud_run_service":       Compute,
		"google_cloudfunctions_function": Compute,

		"google_sql_database_instance": Database,
		"google_bigtable_instance":     Database,
		"google_redis_instance":        Database,
		"google_spanner_instance":      Database,

		"google_compute_firewall":    Security,
		"google_service_account":     Security,
		"google_kms_crypto_key":      Security,
		"google_project_iam_binding": Security,

		"google_storage_bucket":     Storage,
		"google_filestore_instance": Storage,
		"google_compute_disk":       Storage,
	},

	Highlights: []string{
		"google_compute_network",
		"google_compute_subnetwork",
		"google_compute_firewall",
		"google_compute_instance",
		"google_container_cluster",
		"google_sql_database_instance",
	},
}
