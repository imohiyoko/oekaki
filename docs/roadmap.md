# Roadmap

The point of the project is three evidence edge kinds overlaid. The prototype
now has the core of all three, plus collectors and focused views. Everything
below is ordered so that each release remains useful on its own.

This file is what a release contains. [notebook.md](notebook.md) is the working
note behind it: what is worth building before it has earned a version number,
and the decisions already argued out.

## v0.1 — a diagram comes out ✅

Seven first-class resource types, `iac_ref` edges, VPC and subnet nesting,
SVG / DOT / Mermaid / JSON output, plan and state input, a published schema with
a conformance corpus, and a working example that runs straight after a clone.

The success condition was "I tried it on my own Terraform and got a diagram in a
few seconds". A short list of supported types is forgivable. Not working is not.

## v0.2 — the second edge kind ✅

This is the release that makes the project make sense.

- `reachable` edges computed from security group rules
- Highlighting for paths that are permitted but never observed
- A Mermaid renderer good enough to embed in a pull request comment

Current implementation covers inline and standalone AWS security-group rules,
and accepts normalized effective paths from other network-policy collectors.
NACLs, route tables, peering and Transit Gateway remain explicit follow-up
inputs because their effective policy requires additional network context.

One thing already known: security group rules that reference another security
group are *unknown* in a plan for resources that do not exist yet, so
reachability from a plan is necessarily partial. From a state file it is
complete. That difference needs to be visible in the output, not buried here.

## v0.3 — claims, and the log coverage map ✅

The current IR schema is v0.6; v0.3 describes the historical claims release.

The release that admits code is not the whole truth.

- Schema 0.3: every node, edge and group may carry a claim — parser, human or
  model — and a node may carry a coverage state
- `enrichers/overlay`, applying assertions somebody wrote against an
  operations console, joined to the graph by a documented resolution ladder
- `renderers/html`, pulled forward from v0.4: a self-contained page with ELK
  layout, drill-down, click for detail, filtering by coverage state — **and
  the ability to author an overlay by drawing in it**
- Icons, in the HTML renderer

The unglamorous work here was identity resolution, exactly as the capacity
table below is the unglamorous work there. Matching "the workload called
checkout in namespace shop" to a Terraform address is what the whole feature
stands on, and getting it subtly wrong produces a map that is confidently
attached to the wrong resources.

## v0.4 — capacity ✅ (collector boundary)

- A **collector** — a separate program — reading p95 and max from a metrics
  API and writing an overlay. See the note under "Deliberately not planned":
  credentials live in the collector, never in oekaki
- A table defining where each resource type's ceiling comes from — ECS from the
  task definition, RDS from the instance class, ALB from LCUs
- `headroom = observed / ceiling`, mapped to node colour
- `observed` edges from real service-to-service traffic

The table is the actual work here. Headroom does not fall out of a metrics API;
it requires knowing what the limit *is* for each resource type, and that is
patient, unglamorous research rather than code.

`enrichers/` is plural from the first commit so that a New Relic or Prometheus
enricher is an obvious next step rather than a fork.

## v0.5 — exploration (prototype work in progress)

- `oekaki diff` between two graphs
- More assertion kinds: per-API call frequency, access paths by principal,
  volume against a declared baseline. All of them are additional `assert`
  values resolved by the same ladder, which is the payoff of unifying the
  overlay format rather than shipping one file format per question

`diff` is the one that benefits most from determinism being a v0.1 requirement:
comparing two graphs is only meaningful if identical infrastructure produces
identical output.

`oekaki serve` grew a state directory, a roles model and a management page in
v0.5. The authorization it can express is deliberately small — three permission
names, one line of descent, roles and holders written down rather than derived
— because the thing it is waiting on is an identity provider, and building a
richer model before anything can say who is asking would be guessing at which
parts of it matter. Until then every mode that wants authentication refuses to
start.

## v1.0 — a frozen boundary

- Schema v1, with the parser and renderer boundaries frozen
- `parsers/cloudformation`
- An optional LLM enricher for naming summaries and prose, disabled by default

Freezing the schema is the point of 1.0. Everything before it is an invitation
to say the shape is wrong while changing it is still cheap.

## Deliberately not planned

- **Covering every provider resource.** Unknown types render as generic boxes.
  Chasing completeness is unbounded and never finishes.
- **Calling cloud APIs by default.** Credentials and live discovery belong in
  caller-owned collectors. The graph process accepts their evidence files and
  never stores credentials.
- **Implicit LLM calls.** An explicitly selected local executable may be used
  through the AI adapter, but the default CLI path never invokes a model.
- **A layout engine.** Graphviz and ELK are better at it than this project will
  ever be.
- **LLMs on the default path.** Optional, opt-in, and never required to get a
  diagram.

## Known limitations

- Cross-module references are not resolved. Resources inside modules appear
  with their full addresses and their within-module references are followed,
  but values plumbed through `var` and `output` are not traced across the
  boundary.
- `--source-dir` does not recurse, because a file in a subdirectory usually
  belongs to a module whose `module.<name>.` prefix cannot be recovered from the
  path, and guessing would attach source locations to the wrong resources.
- State-file input recovers references by matching identifiers in attribute
  values, which cannot see a dependency that left no concrete identifier behind.
- Containment is understood for AWS, Google Cloud, Azure, vSphere and
  Kubernetes. Other providers render, and their references are extracted
  identically, but their containers are drawn as ordinary boxes rather than as
  nesting until a profile is added under `providers/`.
- A container belongs to one axis, so containers do not appear on the other
  axes. Viewing by provider shows the resources grouped by provider but not the
  subnets they sit in.
