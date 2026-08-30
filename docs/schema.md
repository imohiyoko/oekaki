# The graph format

The schema is the contract between parsers, enrichers and renderers. None of
them know about each other; they only agree on this. The machine-readable
version is [`schema/graph.schema.json`](../schema/graph.schema.json), which is
also embedded in the binary and printed by `oekaki schema`.

Current version: **0.5**. It will change again before v1.0 freezes it.

## Shape

```json
{
  "version": "0.5",
  "metadata": { "generator": "oekaki/0.2.0", "source": "terraform" },
  "axes":   [ … ],
  "nodes":  [ … ],
  "edges":  [ … ],
  "groups": [ … ]
}
```

## Axes

An axis is one way of grouping the same estate.

```json
[
  { "id": "network",  "label": "Network topology" },
  { "id": "provider", "label": "Provider" },
  { "id": "module",   "label": "Module" }
]
```

Infrastructure has no single correct hierarchy. The same database is inside a
subnet, inside an account, inside a module, and owned by a team, all at once.
Version 0.1 had one containment tree and therefore had to pick one of those and
discard the rest — which works while everything is one cloud with one network
model, and stops working the moment it is not.

An estate that mixes on-premises with several clouds has no shared network
topology to nest by at all: a vSphere datacenter and a VPC are not in the same
tree, and there is no honest parent for both. The provider axis answers that,
and the network axis still answers "what is in this subnet" within each one.

Renderers nest by one axis at a time (`--axis network|provider|module`) and are
free to use the others for colouring and filtering.

The Terraform parser emits three:

- **`network`** — containment the infrastructure imposes: subnets inside a VPC,
  namespaces inside a cluster. Derived from references.
- **`provider`** — one group per provider. Derived from `provider_name`.
- **`module`** — Terraform's module tree. A large estate is mostly modules, and
  "what does this module own" is a question network topology cannot answer,
  because one module's resources routinely span several subnets and several
  modules share one.

## Nodes

A resource drawn as a box.

```json
{
  "id": "aws_ecs_service.api",
  "type": "aws_ecs_service",
  "name": "api",
  "provider": "aws",
  "groups": {
    "network":  "aws_vpc.main/aws_subnet.private_a",
    "provider": "provider:aws",
    "module":   "module:module.platform"
  },
  "attrs":   { "desired_count": 3 },
  "metrics": { "cpu_p95": 0.34, "headroom": 0.66 },
  "source":  { "file": "main.tf", "line": 42 }
}
```

`id` is the Terraform resource address, used verbatim. It could have been a
hash, or a synthetic key, but an id you can paste into `grep` or `terraform
state show` is worth more than an id that is shorter. Instances keep their
index, so `aws_subnet.public[0]` is its own node.

**Ids are unique across nodes and groups together**, not within each list. An
edge may point at either, so an id has to identify exactly one thing.

`name` is for the label. The parser prefers the `Name` tag over the Terraform
resource name, because the `Name` tag is what the resource is called in the
console, and the diagram should agree with the console.

`provider` is what makes containment refuse to cross a boundary. See
[Where a node is placed](#where-a-node-is-placed).

`groups` maps an axis id to a path on that axis. An axis missing from the map
means the node sits at that axis's top level.

`attrs` is owned by parsers and is **deliberately curated**, not a dump of every
attribute. Copying everything would bury the signal and, worse, would make the
file churn: `tags_all` changes for reasons that have nothing to do with the
diagram, and this file is meant to be reviewed as a diff.

`metrics`, `coverage` and `claim` are owned by enrichers. **Parsers must not
write to them.** This is the one hard rule about who writes what, and it is what
allows an enricher and a parser to be written by different people who never
talk. The same rule covers `suppressed` on an edge and the top-level
`conflicts`.

`source` is optional and only appears when `--source-dir` is given, since a plan
file contains no source locations.

## Edges

```json
{ "from": "aws_ecs_service.api", "to": "aws_lb_target_group.api", "kind": "iac_ref", "attrs": { "attribute": "load_balancer" } }
```

`kind` is the reason this project exists:

- **`iac_ref`** — the source's configuration references the target. `A → B`
  means deleting B breaks A. `attrs.attribute` records which attribute did the
  referencing.
- **`reachable`** — the network permits traffic along this path, whether or not
  anything uses it. *Not implemented yet.*
- **`observed`** — traffic was actually measured. Produced today by an
  overlay; a metrics collector is the next source.

There are three, and adding a fourth should be resisted. Log coverage looked
like it needed one and did not: a log driver naming a log group *is* a
configuration reference, so a declaration is an `iac_ref`, and "traffic was
actually measured along this path" is already what `observed` means.

Either end may name a **node or a group**. Edges are sorted by
`(kind, from, to)` and de-duplicated.

### Containment is usually not an edge

A reference from a resource to its own VPC or subnet is dropped, because
containment already says it and drawing it again adds one arrow to every box in
the diagram.

That reasoning holds only while containment can express it. Across a provider
boundary containment is refused — an on-premises machine does not live in a VPC
— and then the edge is the only remaining way to record that the reference
happened, so it is kept. This is why edges are allowed to point at groups.

## Groups

Containers, each belonging to one axis.

```json
{ "id": "aws_subnet.private_a", "axis": "network", "type": "subnet", "label": "private-a", "parent": "aws_vpc.main" }
```

The `groups` array is the authority on nesting. A node's path is derived from
it, which is redundant on purpose: a renderer needs the path and should not have
to walk the parent chain to get it. Group ids therefore cannot contain a slash,
and the schema enforces that. A group's parent must be on the same axis.

**Containers are groups, never nodes.** A subnet that appeared both as a
container and as a box inside itself would be a confusing diagram and an
awkward graph.

### Where a node is placed

On the network axis, a node belongs to the containers it references **most
directly**. The parser walks references outward one hop at a time and stops at
the first distance where it finds any container:

- An EC2 instance names its subnet directly, so it lands in that subnet.
- An RDS instance names a DB subnet group, which names two subnets, so it is
  found one hop out and spans both.

When more than one container is found, the node is placed in their lowest common
ancestor. A load balancer in two subnets belongs to neither, so it is drawn in
the VPC that holds both.

Two things stop the walk.

**Security groups are never walked through.** Every security group points at the
VPC, so following one from an EC2 instance would lose the fact that the instance
is in one specific subnet and float it up to the VPC instead.

**The walk never leaves the node's provider.** Referencing something is not the
same as living inside it, and across a provider boundary the two come apart
completely. Before this rule existed, one reference from an on-premises vSphere
VM to an RDS instance was enough to draw that machine inside an AWS subnet.

## Scope

`metadata.scope` names the estate a document describes.

A Terraform resource address is only unique within one state file. An
organisation of any size has many, and `aws_vpc.main` in the platform team's
state is not the same VPC as `aws_vpc.main` in the data team's. Combining those
documents without qualifying the ids would silently merge unrelated resources —
the worst kind of failure, because the result still looks like a valid diagram.

`oekaki graph plan.json --scope platform-prod` qualifies every id:

```json
{ "id": "platform-prod:aws_vpc.main", … }
```

It is opt-in. A single-estate user pays nothing for it, and anyone combining
documents has the tool to do it correctly.

## Determinism

Identical input produces byte-identical output. Nodes and axes sort by id,
groups by `(axis, id)`, edges by `(kind, from, to)`, JSON object keys sort
alphabetically, and there is no timestamp anywhere — which is why `metadata` has
no `generated_at` field, tempting as it was.

This is not tidiness. It is what makes committing a generated graph and
reviewing it as a pull-request diff a reasonable thing to do.

## Validating

```console
$ oekaki graph plan.json | oekaki validate -
ok: 13 nodes, 13 edges, 9 groups
```

Two layers run, because they catch different things. JSON Schema covers shape:
required fields, types, the edge-kind enum. The decoder covers what JSON Schema
cannot express: ids unique across nodes and groups, edges landing on something
real, group parents that exist and share the child's axis, no cycles, and paths
that resolve. Both report every problem they find rather than stopping at the
first.

## Writing your own parser

Emit this format and every renderer works. There is no plugin API to implement
and no Go involved unless you want it — the conformance corpus in
`schema/testdata/` is the definition of correct.

Four things are easy to get wrong:

1. Sort and de-duplicate, or you lose determinism.
2. Do not write to `metrics`.
3. Declare every axis you use, and keep a group's parent on the same axis.
4. Do not invent edges you cannot justify. A wrong edge is worse than a missing
   one: a missing edge is an obvious gap, while a wrong edge is a confident lie.

## Changes since 0.1

- `axes` added; `node.group` (one string) became `node.groups` (a map per axis).
- `group.axis` added and required.
- `node.provider` added.
- `metadata.scope` added.
- Ids must now be unique across nodes and groups together.
- Edges may point at groups, not only nodes.

## Claims

Every node, edge and group may carry a `claim`:

```json
{ "origin": "ai", "author": "assistant", "confidence": 0.6, "note": "read off a service map" }
```

`origin` is `parser`, `human` or `ai`. **Absent means `parser`**, which is the
overwhelmingly common case and therefore costs no bytes: a graph produced with
no overlays is almost byte-identical to one from before claims existed.

The three are kept apart because a diagram that cannot tell them apart presents
somebody's guess with the authority of the code. `confidence` is absent unless
it was stated — an unstated confidence is not a confidence of zero.

Claims are written by enrichers, from overlay documents. See
[overlay.schema.json](../schema/overlay.schema.json), which is versioned
separately: this schema freezes at 1.0 and the overlay vocabulary has to keep
growing past that.

## Coverage

A node may carry what is known about its log collection:

```json
{
  "state": "silent",
  "reason": "a destination is declared and nothing was seen arriving there",
  "evidence": [
    { "kind": "declared", "sink": "logsink:app", "via": "access_logs block" },
    { "kind": "observed", "sink": "logsink:app", "records": 0 }
  ]
}
```

Five states, and the fifth is what makes the other four honest:

| State | Means |
| --- | --- |
| `flowing` | Declared somewhere, and something was seen arriving |
| `silent` | Declared, and nothing arrived |
| `blind` | Somebody looked and found no destination at all |
| `undeclared` | Logs arrive from something nothing declares |
| `unknown` | Nobody asserted anything, or the assertion was ambiguous |

Painting an unassessed resource as a blind spot is the same lie as painting a
blind spot as covered, so **absence of evidence never renders as a finding**. A
node nobody has mentioned carries no `coverage` at all.

Validation enforces that a state has a basis: `flowing`, `silent` and
`undeclared` require evidence, `blind` requires an evidence of kind `none`
because a blind spot has to be something somebody looked for, and `unknown`
carries none. `records` is absent when no count was available, which is not the
same as a count of zero.

## Conflicts

Where two claims disagree, the document shows one value and records the
disagreement rather than resolving it away:

```json
{
  "target_kind": "edge",
  "target": "edge:YXdzX3NlY3VyaXR5X2dyb3VwLmRi.YXdzX3NlY3VyaXR5X2dyb3VwLmJhc3Rpb24.aWFjX3JlZg.",
  "field": "suppressed",
  "claims": [
    { "value": "true",  "claim": { "origin": "human", "author": "operator" } },
    { "value": "false", "claim": { "origin": "parser" } }
  ]
}
```

`target_kind` is required and is either `entity` or `edge`; an entity target is
the node or group id verbatim. Edge targets use a reversible key because edges
have no id of their own. The key starts with `edge:` and contains four
dot-separated components in this fixed order: `from`, `to`, `kind`, `relation`.
Each component is the [RFC 4648 base64url](https://www.rfc-editor.org/rfc/rfc4648#section-5)
encoding, without `=` padding, of the component's UTF-8 bytes. The relation is
present even when empty, so `a` to `b`, kind `iac_ref`, with no relation is
`edge:YQ.Yg.aWFjX3JlZg.`. This keeps ids and relations containing `|`, `.`, or
any other separator collision-free for implementations in any language.

The displayed value is first. Ranking is human over ai over parser, and it is a
total order so that the choice does not depend on which overlay was read first.

Writers and `oekaki encode` emit only version 0.5. `Decode` first validates
the original bytes against the frozen version 0.4 schema, then migrates
unambiguous conflict targets. It rejects an old target that could name both an
entity and an edge (or more than one edge) instead of guessing.

## metadata.overlays

```json
{ "source": "overlay.json", "origin": "human", "author": "operator", "window": "last-7d" }
```

`window` is the period the overlay's author says their evidence covers, in their
own words, echoed verbatim. Nothing here is read from a clock — the metadata
block still carries no timestamp, and determinism holds because the window is
part of the input. It exists because a diagram that says "blind spot" without
saying over what period is a lie of omission.

## Observations

`observations` is an optional source-neutral collection for metrics, sensor
health, security assessments, and exposure checks. It identifies a graph entity
and metric, and may carry a value, unit, timestamp, window, threshold, state,
reason, and claim. Collectors keep credentials and vendor API calls outside
oekaki.

```json
{
	"kind": "oekaki.observations",
	"version": "1",
	"observations": [
		{
			"subject": "service:checkout",
			"metric": "p95_latency",
			"value": 820,
			"unit": "ms",
			"observed_at": "2026-08-28T10:00:00Z",
			"state": "abnormal",
			"reason": "exceeded 500ms threshold",
			"threshold": { "operator": ">", "value": 500 },
			"claim": { "origin": "parser", "note": "Prometheus exposition" }
		}
	]
}
```
