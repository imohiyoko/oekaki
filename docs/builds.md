# What built this: the record a pipeline leaves

The code graph says what the source declares. The Terraform and Kubernetes
graphs say what runs. Between them is an image tag, and nothing here read
where it came from.

In an enterprise that join is already automated and already written down: a
pipeline builds an image from a commit, and a pull request writes that image
into the IaC. So the record of *this container is that repository at that
commit* lives in the CI system, and that is the only place it exists.

```console
$ oekaki graph cluster.yaml --builds builds.json -o estate.json
builds: 1 file, 1 assertions applied
```

## The document

One file, written by whatever has the credentials. oekaki never fetches it:
the vendor API and the token stay outside, exactly as they do for metrics and
for traces.

```json
{
  "kind": "oekaki.builds",
  "version": "0.1",
  "builds": [
    {
      "repository": "acme/checkout",
      "commit": "9f1c0f2e6a0b",
      "ref": "refs/heads/main",
      "run": {
        "id": "17243",
        "workflow": "release",
        "url": "https://github.com/acme/checkout/actions/runs/17243",
        "completed_at": "2026-09-14T10:02:11Z"
      },
      "images": [
        { "reference": "registry.example/checkout:1.4.0", "digest": "sha256:…" }
      ]
    }
  ]
}
```

`repository`, `run.id` and at least one image are required. The rest is
optional because a pipeline that records less still answers the question the
whole thing is for.

It is not an overlay. An overlay is an assertion somebody makes about a graph
and carries an author; this is a record a pipeline already wrote about itself
and carries a run. The difference is exactly where trust is decided, so the two
are separate documents with separate schemas
([schema/builds.schema.json](../schema/builds.schema.json)).

## What it adds

| node | what it is |
| --- | --- |
| `repository` | one repository the record names, when something here runs what it built |

| relation | between | what it says |
| --- | --- | --- |
| `built_from` | element → repository | a named run built the image this element runs |

The edge is `observed`: a build is something that happened, not something the
configuration permits. Its claim names the run — `release run 17243` — and it
carries the `commit`, the `ref`, the `image` it matched on, the `digest` and
the `run_url`, so "which system is this container" has an answer somebody can
go and check.

A repository the estate does not already draw becomes a node here, and the run
summary says so — `adopted` in the JSON report — because an edge to a box that
was invented a moment ago and an edge to parsed infrastructure look identical
in a drawing.

A repository nothing here runs gets no node at all. It is reported instead:

```
reported: {image=registry.example/unused:1.0.0} — nothing here runs it (built by acme/unused)
```

which is the difference between an estate that does not run something and a
join that quietly found nothing. The two look identical in a drawing.

## Why the image reference, and only the image reference

The reference is the one identifier both halves actually share. The IaC says
which image a workload runs; the record says which repository produced that
image. Matching them is reading two documents, not guessing.

So nothing is stripped, completed or normalised. `checkout:1.4.1` is not
`checkout:1.4.0`, a bare `checkout` is not `registry.example/checkout`, and a
repository called `checkout` that happens to build an image called `checkout`
is **not** evidence of anything — that is precisely the invention this project
refuses, and the estate where it is wrong is the one where it looks most
obviously right.

A digest matches too, both ways round: an estate that pins `name@sha256:…`
never writes the tag the record was pushed with, and a record that pins one
never writes the tag the estate runs. A record that says a digest twice and says it differently is refused on the
spot — whether that is a reference pinned to one digest carrying another in its
`digest` field, or two entries in one build giving one reference two digests.
Whichever of the two is true, the other joins a container to a build that did
not produce it.

## Two records of one image

Rebuilding a tag is ordinary, so when one repository claims an image twice the
later run wins — by `run.completed_at`, then by `run.id`, so every machine
resolves it the same way. Both are compared as what they are rather than as
text: `19:00+09:00` is an hour *before* `11:00Z` however the two sort as
strings, and run 9 is before run 10 however they sort as text. A
`completed_at` nothing can read is refused rather than quietly ignored. Determinism is what `oekaki diff` is built on, and a
tie broken by map order would take it away.

Two *different* repositories claiming one image is not something to pick a
winner for. One of them did not build it and nothing here knows which, so no
edge is drawn and both are named:

```
ambiguous, not applied: {image=registry.example/checkout:1.4.0} -> acme/checkout, acme/legacy-checkout
```

## Which element is the repository

By default the repository becomes a node of its own. The record says it exists
and says it built this, and that much is known without anybody deciding where
it sits in the estate.

When a code graph is loaded as well, whoever knows the estate can say which
**input** that repository is. The repository node then records it as
`attrs.code_input`, and in an atlas the box opens as that repository's code map
— the descent from a running container to one class, by clicking. See
[atlas.md](atlas.md).

What this run is told is what the repository ends up saying, whether or not
anything here happens to be running an image the records name — a mapping is a
sentence about a repository, not about what is deployed today. A repository
this run says nothing about keeps what it was told before: not repeating a flag
is not a retraction.

Its own key, not `attrs.repository`: that one already says which input a node
*came from*, which every node of a combined graph carries and which a
repository node arriving inside a previous output carries too. One key holding
two answers is decided by whichever was written last.

```console
$ oekaki graph cluster.yaml --repo ../checkout \
    --builds builds.json --build-repo acme/checkout=repo-2-checkout
```

The id on the right is the input's, which `--repo` derives from the directory
name and the graph records in its metadata. One element of the graph is
accepted there too, for an estate that would rather point the edge at something
it already draws:

```console
$ oekaki graph cluster.yaml --repo ../checkout \
    --builds builds.json --build-repo acme/checkout=repo-2-checkout:file:main.go
```

An id that names nothing is an error rather than a silent drop — the same
refusal [`--api`](api.md) makes from the other side. So is a repository no
record mentions: that mapping would never be consulted, the run would join to
an invented node under the very name somebody was overriding, and nothing
would have said so. The join was the question the flag was answering.

## Refusing to read it

```console
$ oekaki graph cluster.yaml --builds builds.json --no-builds
builds: --no-builds, so the build records were not read
```

Whether a CI system belongs in the picture at all is the estate's decision, not
this program's default. The reading is already opt-in, but the command is
often assembled by a wrapper — an `action.yml`, a Makefile — that passes
`--builds` unconditionally, and refusing has to be sayable downstream of
whoever wrote the wrapper.

## What is not read

**Anything that is not `attrs.image`.** Two parsers record one:
[parsers/kubernetes](kubernetes.md) takes the image of a pod's first container,
and the Terraform parser takes it out of an `aws_ecs_task_definition`'s
`container_definitions` — so the join lands on the task definition, which is
the thing that declares what runs, one `iac_ref` away from the service.

A Lambda built from an image (`image_uri`), Cloud Run and Azure container
instances are not read. Their images are written down in shapes of their own,
and each is a small addition to the same place rather than something guessed at
here.

**Everything else in a container definition.** The image is read and no other
field is. The ports, limits and environment in the same document stay unread,
because this is the identifier two documents share rather than a model of a
container.

**A second container.** One workload, one image, in both parsers. A sidecar
built from another repository is a second answer to "which system is this", and
there is nowhere to put it yet.

**The CI system itself.** No poller ships here. `gh api`, a step at the end of
the pipeline, or anything else that can write JSON is the producer, and the
document is deliberately vendor-neutral: nothing in it is particular to GitHub
Actions beyond the shape a run has everywhere.
