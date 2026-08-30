"""Build the plan.json for examples/log-coverage from main.tf's shape.

`terraform show -json` output is what oekaki consumes, and producing it
here rather than committing a capture from a real run keeps the example free
of anything that came off somebody's account. The shape is the documented one:
planned_values for what the resources are, configuration for what references
what.

Run: python3 examples/log-coverage/gen_plan.py
"""

import json
import pathlib

REF = "references"
CONST = "constant_value"


def ref(*targets):
    return {REF: list(targets)}


def const(v):
    return {CONST: v}


resources = [
    (
        "aws_vpc", "main",
        {"cidr_block": "10.20.0.0/16", "enable_dns_hostnames": True},
        {"cidr_block": const("10.20.0.0/16"), "enable_dns_hostnames": const(True)},
    ),
    (
        "aws_subnet", "private_a",
        {"cidr_block": "10.20.1.0/24", "availability_zone": "eu-west-1a", "map_public_ip_on_launch": False},
        {"vpc_id": ref("aws_vpc.main.id", "aws_vpc.main"),
         "cidr_block": const("10.20.1.0/24"),
         "availability_zone": const("eu-west-1a")},
    ),
    (
        "aws_subnet", "private_b",
        {"cidr_block": "10.20.2.0/24", "availability_zone": "eu-west-1b", "map_public_ip_on_launch": False},
        {"vpc_id": ref("aws_vpc.main.id", "aws_vpc.main"),
         "cidr_block": const("10.20.2.0/24"),
         "availability_zone": const("eu-west-1b")},
    ),
    (
        "aws_cloudwatch_log_group", "app",
        {"name": "/platform/app", "retention_in_days": 30},
        {"name": const("/platform/app"), "retention_in_days": const(30)},
    ),
    (
        "aws_cloudwatch_log_group", "batch",
        {"name": "/platform/batch", "retention_in_days": 7},
        {"name": const("/platform/batch"), "retention_in_days": const(7)},
    ),
    (
        "aws_lb", "public",
        {"name": "public", "internal": False, "load_balancer_type": "application"},
        {"name": const("public"), "internal": const(False),
         "load_balancer_type": const("application"),
         "subnets": ref("aws_subnet.private_a.id", "aws_subnet.private_a",
                        "aws_subnet.private_b.id", "aws_subnet.private_b")},
    ),
    (
        "aws_ecs_cluster", "main",
        {"name": "main"},
        {"name": const("main")},
    ),
    (
        "aws_ecs_service", "api",
        {"name": "api", "desired_count": 3, "launch_type": "FARGATE"},
        {"name": const("api"), "desired_count": const(3), "launch_type": const("FARGATE"),
         "cluster": ref("aws_ecs_cluster.main.id", "aws_ecs_cluster.main")},
    ),
    (
        "aws_ecs_service", "checkout",
        {"name": "checkout", "desired_count": 2, "launch_type": "FARGATE"},
        {"name": const("checkout"), "desired_count": const(2), "launch_type": const("FARGATE"),
         "cluster": ref("aws_ecs_cluster.main.id", "aws_ecs_cluster.main")},
    ),
    (
        "aws_ecs_service", "search",
        {"name": "search", "desired_count": 1, "launch_type": "FARGATE"},
        {"name": const("search"), "desired_count": const(1), "launch_type": const("FARGATE"),
         "cluster": ref("aws_ecs_cluster.main.id", "aws_ecs_cluster.main")},
    ),
    (
        "aws_db_subnet_group", "main",
        {"name": "main"},
        {"name": const("main"),
         "subnet_ids": ref("aws_subnet.private_a.id", "aws_subnet.private_a",
                           "aws_subnet.private_b.id", "aws_subnet.private_b")},
    ),
    (
        "aws_db_instance", "main",
        {"allocated_storage": 50, "engine": "postgres", "engine_version": "16.3",
         "instance_class": "db.t4g.small", "multi_az": False},
        {"allocated_storage": const(50), "engine": const("postgres"),
         "engine_version": const("16.3"), "instance_class": const("db.t4g.small"),
         "multi_az": const(False),
         "db_subnet_group_name": ref("aws_db_subnet_group.main.name", "aws_db_subnet_group.main")},
    ),
]

planned = []
configured = []
for typ, name, values, expressions in resources:
    address = f"{typ}.{name}"
    planned.append({
        "address": address,
        "mode": "managed",
        "type": typ,
        "name": name,
        "provider_name": "registry.terraform.io/hashicorp/aws",
        "schema_version": 0,
        "values": values,
    })
    configured.append({
        "address": address,
        "mode": "managed",
        "type": typ,
        "name": name,
        "provider_config_key": "aws",
        "schema_version": 0,
        "expressions": expressions,
    })

doc = {
    "format_version": "1.2",
    "terraform_version": "1.9.0",
    "planned_values": {"root_module": {"resources": planned}},
    "configuration": {
        "provider_config": {
            "aws": {
                "name": "aws",
                "full_name": "registry.terraform.io/hashicorp/aws",
                "expressions": {"region": {"constant_value": "eu-west-1"}},
            }
        },
        "root_module": {"resources": configured},
    },
}

out = pathlib.Path(__file__).with_name("plan.json")
out.write_text(json.dumps(doc, indent=2) + "\n")
print(f"wrote {out} ({len(planned)} resources)")
