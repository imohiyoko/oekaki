package providers

func init() { Register(kubernetes) }

// Kubernetes has exactly one containment axis and it is the namespace. There
// is no network hierarchy to speak of — every pod is routable to every other
// by default — so the namespace is both the boundary people organise by and
// the only one worth drawing.
//
// Terraform's kubernetes provider nests the namespace inside a `metadata`
// block rather than naming it in a top-level attribute, which the expression
// walker already handles: it descends into nested blocks and reports the
// block name as the referencing attribute.
var kubernetes = &Profile{
	Name:     "kubernetes",
	Prefixes: []string{"kubernetes_"},

	Containers: map[string]Container{
		"kubernetes_namespace": {Type: "namespace"},
	},

	// A service account is mounted into most workloads and says nothing about
	// where they run.
	Attachments: map[string]bool{
		"kubernetes_service_account": true,
	},

	Attrs: map[string][]string{
		"kubernetes_deployment":   {"metadata"},
		"kubernetes_stateful_set": {"metadata"},
		"kubernetes_daemon_set":   {"metadata"},
		"kubernetes_service":      {"spec"},
		"kubernetes_cron_job_v1":  {"spec"},
	},

	// A workload is known to a log system by its name and namespace, never by
	// its Terraform address. Both keys are required together on a selector,
	// because the same workload name in two namespaces is two workloads.
	//
	// The dotted paths reach into the `metadata` block, which is where
	// Terraform's Kubernetes provider puts both.
	Identities: map[string]map[string]string{
		"kubernetes_deployment":   {"workload": "metadata.name", "namespace": "metadata.namespace"},
		"kubernetes_stateful_set": {"workload": "metadata.name", "namespace": "metadata.namespace"},
		"kubernetes_daemon_set":   {"workload": "metadata.name", "namespace": "metadata.namespace"},
	},

	Categories: map[string]Category{
		"kubernetes_deployment":   Compute,
		"kubernetes_stateful_set": Compute,
		"kubernetes_daemon_set":   Compute,
		"kubernetes_job":          Compute,
		"kubernetes_cron_job_v1":  Compute,
		"kubernetes_pod":          Compute,

		"kubernetes_service":        Network,
		"kubernetes_ingress_v1":     Network,
		"kubernetes_network_policy": Network,

		"kubernetes_secret":               Security,
		"kubernetes_service_account":      Security,
		"kubernetes_role_binding":         Security,
		"kubernetes_cluster_role_binding": Security,

		"kubernetes_persistent_volume":       Storage,
		"kubernetes_persistent_volume_claim": Storage,
		"kubernetes_config_map":              Storage,
	},

	Highlights: []string{
		"kubernetes_namespace",
		"kubernetes_deployment",
		"kubernetes_service",
	},
}
