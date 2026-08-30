package providers

func init() { Register(aws) }

var aws = &Profile{
	Name:     "aws",
	Prefixes: []string{"aws_"},

	// A VPC contains subnets; subnets contain everything else. Both are drawn
	// as borders rather than boxes, so a subnet never appears twice.
	Containers: map[string]Container{
		"aws_vpc":    {Type: "vpc"},
		"aws_subnet": {Type: "subnet"},
	},

	// Every resource in a VPC points at a security group, and every security
	// group points at the VPC, so a security group is a shortcut from anywhere
	// to the top. Walking through one would move an instance out of its subnet.
	Attachments: map[string]bool{
		"aws_security_group": true,
	},

	Attrs: map[string][]string{
		"aws_vpc":                 {"cidr_block", "enable_dns_hostnames"},
		"aws_subnet":              {"availability_zone", "cidr_block", "map_public_ip_on_launch"},
		"aws_security_group":      {"description", "egress", "ingress", "name", "id"},
		"aws_security_group_rule": {"type", "egress", "protocol", "from_port", "to_port", "security_group_id", "source_security_group_id", "cidr_blocks", "ipv6_cidr_blocks", "description"},
		"aws_instance":            {"ami", "instance_type"},
		"aws_ecs_service":         {"desired_count", "launch_type", "name"},
		"aws_ecs_task_definition": {"cpu", "family", "memory", "network_mode"},
		"aws_ecs_cluster":         {"name"},
		"aws_db_instance":         {"allocated_storage", "engine", "engine_version", "instance_class", "multi_az"},
		"aws_db_subnet_group":     {"name"},
		"aws_rds_cluster":         {"engine", "engine_version"},
		"aws_lb":                  {"internal", "load_balancer_type", "name"},
		"aws_lb_target_group":     {"port", "protocol", "target_type"},
		"aws_lb_listener":         {"port", "protocol"},
		"aws_autoscaling_group":   {"desired_capacity", "max_size", "min_size"},
		"aws_elasticache_cluster": {"engine", "node_type", "num_cache_nodes"},
		"aws_lambda_function":     {"function_name", "handler", "memory_size", "runtime", "timeout"},
		"aws_nat_gateway":         {"connectivity_type"},
		"aws_s3_bucket":           {"bucket"},
		"aws_dynamodb_table":      {"billing_mode", "hash_key"},
		"aws_eks_cluster":         {"version"},

		// Carried so that an overlay can name a log destination the way the
		// platform does, rather than by its Terraform address.
		"aws_cloudwatch_log_group": {"name", "retention_in_days"},
	},

	// What these resources are called to a log system. A person reading an
	// operations console sees "/platform/app" and "api", not
	// "aws_cloudwatch_log_group.platform".
	Identities: map[string]map[string]string{
		"aws_cloudwatch_log_group": {"log_group": "name"},
		"aws_ecs_service":          {"service": "name"},
		"aws_lambda_function":      {"function": "function_name"},
		"aws_lb":                   {"load_balancer": "name"},
		"aws_s3_bucket":            {"bucket": "bucket"},
	},

	Categories: map[string]Category{
		"aws_lb":                      Network,
		"aws_alb":                     Network,
		"aws_elb":                     Network,
		"aws_lb_listener":             Network,
		"aws_lb_target_group":         Network,
		"aws_nat_gateway":             Network,
		"aws_internet_gateway":        Network,
		"aws_route_table":             Network,
		"aws_route53_record":          Network,
		"aws_cloudfront_distribution": Network,
		"aws_api_gateway_rest_api":    Network,

		"aws_instance":            Compute,
		"aws_ecs_service":         Compute,
		"aws_ecs_cluster":         Compute,
		"aws_ecs_task_definition": Compute,
		"aws_eks_cluster":         Compute,
		"aws_lambda_function":     Compute,
		"aws_autoscaling_group":   Compute,
		"aws_launch_template":     Compute,

		"aws_db_instance":         Database,
		"aws_rds_cluster":         Database,
		"aws_dynamodb_table":      Database,
		"aws_elasticache_cluster": Database,
		"aws_db_subnet_group":     Database,

		"aws_security_group":  Security,
		"aws_iam_role":        Security,
		"aws_iam_policy":      Security,
		"aws_kms_key":         Security,
		"aws_acm_certificate": Security,
		"aws_network_acl":     Security,

		"aws_cloudwatch_log_group": Storage,

		"aws_s3_bucket":       Storage,
		"aws_efs_file_system": Storage,
		"aws_ebs_volume":      Storage,
	},

	Highlights: []string{
		"aws_vpc",
		"aws_subnet",
		"aws_security_group",
		"aws_instance",
		"aws_ecs_service",
		"aws_db_instance",
		"aws_lb",
	},
}
