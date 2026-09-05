# Architecture

```
parsers/  →  IR (graph.json)  →  collectors/enrichers/  →  views/layout  →  renderers/
```

Every arrow is a JSON document conforming to
[`schema/graph.schema.json`](../schema/graph.schema.json). The central artifact
of this project is that schema, not the binary. Parsers and renderers are
interchangeable parts that agree on it, and that boundary is deliberately also
where contributions come in.

## Layout

| Directory | Contains |
| --- | --- |
| `schema/` | The IR schema, embedded in the binary, plus its conformance corpus |
| `core/` | IR types, normalization, validation, grouping |
| `providers/` | What is known about each provider: containment, attributes, categories |
| `parsers/terraform/` | `terraform show -json` → IR |
| `parsers/source/` | multi-language source tree → code dependency IR; extensible parser registry |
| `collectors/` | vendor-neutral adapters for logs, metrics, traces, and explicit reachability probes |
| `enrichers/` | What an enricher is, and what it may write |
| `enrichers/overlay/` | Assertions a human or a model wrote → IR annotations |
| `enrichers/ai/` | Validated optional model nodes/relationship candidates → IR |
| `internal/textmetrics/` | How wide text actually is, in points |
| `renderers/dot/` | IR → Graphviz DOT |
| `renderers/graphviz/` | DOT → SVG, via Graphviz compiled to WebAssembly |
| `renderers/mermaid/` | IR → Mermaid flowchart |
| `renderers/html/` | IR → one self-contained interactive page |
| `views/` | focused projections such as request paths and reachability, the atlas, and the path listing |
| `renderers/style/` | Colours and shapes shared by every renderer |
| `internal/cli/` | Command line |
| `cmd/oekaki/` | `main` |
| `examples/three-tier/` | A working plan and its generated output |
| `examples/log-coverage/` | The same, for the coverage map and overlays |

`parsers`, `renderers` and (later) `enrichers` are plural. A directory called
`enrichers/datadog` invites a `enrichers/newrelic` next to it; a directory
called `datadog/` tells a New Relic user to look elsewhere.

### Multiple repositories and AI context

`--repo` is repeatable on `graph` and `render`. A single input retains its
existing IDs; multiple inputs are qualified into repository namespaces and
merged after each input has independently validated. Each node carries the
repository namespace in parser-owned attributes, while `metadata.inputs`
records the selected local paths and input kinds. An AI adapter can therefore
distinguish an unresolved reference from a repository that was never supplied.
Its `needs` output is a structured request, not an implicit clone operation;
the operator supplies the additional local path and runs the same pipeline
again.

Source relationships also distinguish `library` imports from `application`
calls. The former are direct static dependencies; the latter are code-level
inferences and carry a `static_*` resolution marker. Runtime traces and log or
metric observations remain the evidence for whether a path was actually used.

## Design decisions

### The graph is a set of claims, and oekaki never makes one

A parser reading a file is one claim origin among three. A human reading an
operations console and a model reading one for them are the others, and what
is written in code is not automatically more true than either — state input
already recovers only the references that happened to leave an identifier
behind. All three write the same overlay document, and the IR records which of
them said what.

**oekaki consumes evidence documents and never invokes a model by default.**
The optional `enrichers/ai.Generate` boundary passes a graph to an explicitly
selected executable and accepts only validated candidate JSON. Non-determinism
lives at that opt-in boundary; the render path remains deterministic for the
same validated input.

This is the concrete form of the promise in the roadmap that LLMs are optional
and never required to get a diagram — a file format rather than an intention.

### Disagreement is recorded, not resolved

Where two claims conflict the document displays one value and says so, listing
every competing answer with its claimant. Ranking picks what is shown; it never
discards the alternatives. A diagram that silently picks a winner is a diagram
that lies quietly, and the reader cannot check what they cannot see. For the
same reason an edge somebody asserts is not real is flagged rather than
deleted: "a human said this is wrong" and "this never existed" are different
facts and only the first one is true.

### The default path is completely deterministic

Same input, same bytes — including layout, because layout is delegated to
Graphviz rather than randomised. This is what makes a generated diagram
committable and reviewable in a pull request, which in turn is the whole reason
the GitHub Action in the README is worth having.

### No layout engine of our own

Graphviz does layout. Later, ELK will do nested layout for the HTML renderer.
Hand-rolling a layout engine is a bottomless pit and a common way for a project
like this to stall before it is useful.

### Graphviz as WebAssembly

`github.com/goccy/go-graphviz` embeds Graphviz compiled to WebAssembly, run by
wazero. No cgo, no `dot` on `PATH`, cross-compiles like any pure Go program.

Its raster backend ignores `fill="none"` and fills every edge spline, so PNG
output is wrong and is not offered. SVG is correct and is the only image format
exposed; anyone needing a bitmap can pipe `-f dot` into a real Graphviz.

### Determinism covers the artifact, not the browser's layout of it

`-f svg` is deterministic all the way to the picture, because Graphviz does
layout at generate time. `-f html` is deterministic to the file: the same graph
produces the same bytes, and `make verify-example` checks that. But the
positions in the page are computed by ELK in the browser, and CI never runs a
browser. ELK's layered algorithm is itself deterministic for identical input,
so reopening the same file gives the same picture — that is a property of ELK,
not a promise this project tests.

**SVG remains the default output, and it is the one to commit into a pull
request.**

### DOT is a pivot format, not an implementation detail

`-f svg` renders the same DOT that `-f dot` prints. The DOT a user inspects when
something looks wrong is exactly the DOT that produced their picture.

### Read-only, credential-free graph path

The graph path reads files and caller-provided evidence. Nothing calls AWS and
no credential is serialized into the IR. Live collectors can use credentials in
their own process and write normalized evidence for the graph path.

### LLMs are optional and absent by default

The AI adapter is opt-in and local-executable based. It produces only
relationship candidates with provenance and confidence; the diagram renders
fully without it, and no third-party API is built into the project.

## How a render works

1. **Read.** The CLI sniffs the input: a `configuration`/`planned_values` block
   means Terraform, a `version`/`nodes` pair means an IR document already. This
   is what lets `oekaki graph plan.json | oekaki render -` work.
2. **Parse.** `planned_values` supplies resources; `configuration` supplies the
   reference expressions. Containers become groups, everything else becomes a
   node. Nested blocks are walked recursively, module calls are walked with
   their address prefix, and `count`/`for_each` instances are resolved so that
   a reference to `aws_subnet.public` reaches `aws_subnet.public[0]` and `[1]`.
3. **Place.** Containment is resolved by walking references outward, nearest hop
   first, refusing to walk through security groups. See
   [docs/schema.md](schema.md#where-a-node-is-placed).
4. **Normalize and validate.** Canonical ordering, de-duplication, and a
   referential-integrity check. A renderer can walk the result without
   defensive checks.
5. **Render.** DOT, then Graphviz for SVG; or Mermaid; or the IR itself.

### Runtime evidence

`collectors/loginventory` polls a backend through the `Store` interface and
persists only classified log metadata. `collectors/prometheus.Poller` does the
same for labelled sensor samples, retaining timestamped observations and
optional thresholds. `collectors/reachability.Probe` emits one normalized path
per explicitly supplied target. A probe is tied to its `from` node and must be
run from the network vantage point whose reachability is being described.

These collectors are outside the graph renderer. They may own credentials and
vendor SDKs in their process, but evidence documents contain no credentials,
raw log bodies, or HTTP response bodies.

## Extending it

**A resource type** is an entry in its provider's profile under `providers/`.
`Containers` decides whether it becomes a group, `Attachments` whether
containment may walk through it, `Attrs` which attributes are carried, and
`Categories` which drawing category it falls into. An unlisted type still
renders — as a generic grey box — so nothing breaks by omission.

**A provider** is a new file under `providers/` calling `Register`. Neither the
parser nor the renderers need to change: the parser asks the registry whether a
type is a container, and the renderers ask it for a category.

**A parser** is anything that emits valid IR, in any language. Validate it with
`oekaki validate`.

**A renderer** takes a `*core.Graph` and returns bytes. It can assume the graph
is normalized and valid.

**An enricher** reads a graph and writes to `metrics` on nodes, or adds
`observed` edges. It must not touch `attrs`, which belongs to parsers.
