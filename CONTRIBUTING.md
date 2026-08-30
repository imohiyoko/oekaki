# Contributing

The architecture exists so that the interesting contributions do not require
touching the core. If you find yourself editing `core/` to add a resource type
or an output format, something has gone wrong — please say so in an issue.

## Getting set up

```console
$ git clone https://github.com/imohiyoko/oekaki && cd draw
$ go test ./...
$ go run ./cmd/oekaki render examples/three-tier/plan.json -o /tmp/a.svg
```

Go 1.24 or newer. Graphviz is compiled in as WebAssembly, so there is no `dot`
to install and no cgo.

## Adding a resource type

Everything about a resource type lives in one place: its provider's file under
`providers/`. Add entries to that profile:

- `Attrs` — which attributes to carry into the IR. Keep it short. The IR is
  meant to be read as a diff, and `tags_all` churning on every plan would make
  that worthless.
- `Categories` — which drawing category it belongs to.
- `Containers` — only if the resource holds other resources, like a VPC or a
  Kubernetes namespace.
- `Attachments` — only if every resource in the network points at it, like a
  security group. Containment resolution refuses to walk through these.

An unlisted type still renders as a generic box, so nothing breaks if you skip
any of it.

These used to be two tables in two packages, and they had already drifted apart
— some types had a colour but no attributes, others the reverse, and nothing
noticed. `providers/providers_test.go` now fails on that, so a half-finished
entry is caught rather than shipped.

Please add the type to the example under `examples/three-tier/` if it fits
naturally, or a new fixture under `parsers/terraform/testdata/` if it does not.

## Adding a parser

A parser turns something into IR. Emit JSON that validates against
[`schema/graph.schema.json`](schema/graph.schema.json) and every existing
renderer works, in whatever language you like:

```console
$ your-parser input | oekaki render - -o architecture.svg
```

If it is written in Go, it belongs in `parsers/<name>/` and should expose a
`Parse([]byte, Options) (*core.Graph, error)`.

Three rules, in order of how easy they are to get wrong:

1. **Do not write to `metrics`.** That field belongs to enrichers. It is the one
   ownership rule that lets a metrics enricher and a parser be written by people
   who never speak.
2. **Sort and de-duplicate.** `core.Graph.Normalize()` does it for you. Without
   determinism, committed graphs churn and the whole diff-review story dies.
3. **Do not invent edges.** A missing edge is a visible gap. A wrong edge is a
   confident lie, and someone will act on it.

## Adding a renderer

Take a `*core.Graph`, return bytes. It arrives normalized and validated, so no
defensive checks are needed.

Use `renderers/style` rather than choosing your own colours — a database should
look the same whichever renderer drew it. If you need a new visual distinction,
add it there.

## Tests

Run `go test ./...` and `make lint`. The latter requires `golangci-lint`; CI
installs the pinned version automatically. What matters most:

- **Determinism.** New behaviour that iterates a map needs a repeat-render test.
  Go randomises map order, so a single comparison often passes by luck; the
  existing tests repeat ten times.
- **Schema conformance.** Parser output is checked against the published schema,
  not just the Go structs, so the two cannot drift.
- **The corpus.** `schema/testdata/valid` and `invalid` define what the format
  actually is. Adding a file there is often better than adding a test function.

## How changes land

`main` is protected. Changes arrive by pull request, and a PR needs the test
matrix, the race detector and the determinism check to pass before it can
merge. Force pushes and deletions are refused for everyone.

The ruleset requires a single check, `gate`, rather than each matrix leg by
name. `gate` fails if any upstream job failed, so the protection is the same —
but the list of what must pass lives in the workflow's `needs:` instead of
being duplicated in repository settings, where the two could quietly drift
apart when a matrix leg is renamed.

The determinism check is the one that surprises people: it regenerates
`examples/three-tier/` and fails if anything moved. If you changed rendering or
parsing on purpose, run `make example` and commit the result in the same PR. If
you did not, a diff there means something became non-deterministic — usually a
map iterated without sorting.

`CODEOWNERS` requests a review automatically. It exists mostly to flag the
paths that are expensive to get wrong: the schema, the workflows, and the
licence notices.

## Releasing

Maintainers only. Two routes, and neither publishes without a human.

**From the Actions tab** (the normal one). Run the **Release** workflow against
`main`, pick `patch` / `minor` / `major`, and optionally a prerelease
identifier like `rc1`. It works out the next version from the newest `v*` tag,
creates a `release/vX.Y.Z` branch pinning the exact commit, tags it, and then
publishes.

**From a terminal** (the direct route). `git tag -a vX.Y.Z -m "…" && git push
origin vX.Y.Z`. No release branch is created.

Either way:

1. `verify` runs the tests and a full GoReleaser dry run.
2. `publish` then **waits for a repository admin to approve it** in the
   `release` environment. Nothing reaches the Releases page until someone does.

The split is the point. The dry run means the approver is only ever asked about
a build that has already succeeded, rather than being asked to vouch for a tag
that then falls over.

Release tags and `release/*` branches are protected: they cannot be moved or
deleted once pushed. A mistake is fixed with a new version, not by rewriting
the old one. Version numbers are single-use.

If you add or remove a dependency, update `THIRD_PARTY_NOTICES.md` in the same
PR. The binaries embed Graphviz under EPL-2.0, so shipping an archive whose
notices are out of date is a licensing problem, not a tidiness one.

## Style

Ordinary Go, `gofmt`ed. Comments should explain why something is the way it is,
particularly when the obvious approach was tried and rejected — several
decisions here look arbitrary until you know what they avoid, and those are the
comments worth writing.

## Scope

Some things are deliberately out, and a PR adding them will be turned down
however good it is:

- Calls to cloud provider APIs, or anything needing credentials
- A hand-written layout engine
- Anything that requires an LLM to produce a diagram
- Chasing complete coverage of every provider resource

[docs/roadmap.md](docs/roadmap.md) has the reasoning. If you disagree with a
boundary, an issue arguing the case is genuinely welcome — better to move the
line deliberately than to discover it in code review.
