# Diff: what did this change do to the picture

A diagram lives in a repository and is regenerated on every change. The
question a reviewer has is not "what does the estate look like" — it is "what
did this pull request do to it". A picture answers that badly: two pictures
side by side is a spot-the-difference puzzle, and the difference that matters
is usually one box.

```console
$ oekaki diff before.json after.json
5 changes: 1 added, 3 removed, 1 changed
removed  edge   http → public (iac_ref)
removed  edge   http → api (iac_ref)
changed  node   jump-host  aws_instance.bastion
         attr:instance_type: "t3.micro" → "t3.large"
         name: bastion → jump-host
removed  node   http  aws_lb_listener.http
added    node   assets  aws_s3_bucket.assets
```

`-f json` writes the same answer for something else to read, and `--exit-code`
makes the command exit 1 when anything changed — which is what turns it into a
check: *this pull request changes the estate, and here is how.*

```console
$ oekaki graph plan.json -o after.json
$ oekaki diff main.json after.json --exit-code
```

## Why this is the payoff of determinism

Identical input produces byte-identical output. That has been a requirement
since v0.1 — nodes sort by id, JSON keys sort alphabetically, and there is no
timestamp anywhere, which is why `metadata` has no `generated_at` field however
tempting it was.

Without it, every regeneration would differ from the last one and a comparison
would be noise. With it, a comparison is a fact.

## An id is identity

**A resource whose id changed is a removal and an addition, not a rename.**

The ids are what say two things are the same thing, and here they disagree.
Pairing them by similar names or similar attributes would be a claim neither
document makes, and it is worse than the usual invention: a wrongly paired
rename hides a deletion *and* a creation behind a field change, which is the
one shape a reviewer must not miss.

Whoever knows it was a rename can say so. Nothing here has to guess.

## What is compared

What is drawn, and what somebody would act on.

| | compared | identity |
| --- | --- | --- |
| `node` | type, name, description, provider, its group on each axis, coverage state, claim, and every attribute | its id |
| `edge` | suppressed, claim, attributes | its two ends, kind and relation |
| `group` | type, label, axis, parent, claim, attributes | its id |
| `path` | label, claim, attributes | its kind and its participants in order |
| `note` | — | everything it is |

Attributes are compared because they are usually where the change is: an
instance type, a port, an image tag. Each is encoded the way the document
encodes it, so `8080` and `"8080"` are told apart.

A note is keyed by everything it is, because a note is not a field somebody
edits: `Normalize` folds only notes that match exactly, so a note with one word
changed is a note somebody wrote, beside the one they wrote before. It is
reported added and removed rather than changed, and that is the truth of it.

## What is not compared

**Measurements.** Observations, metrics and log records change on every
collection *by design* — that is what a measurement is. A diff that reported
them would bury the box that moved under a thousand numbers that were always
going to move. Use `oekaki alerts` for the question "is a reading wrong", which
is a different question with its own answer.

What a *conclusion* drawn from measurements says is compared. Coverage is a
state rather than a reading, and a service that went blind is a change worth
being told about.

## Two estates are not a diff

Comparing documents whose `metadata.scope` differs reports every element of
both as added and removed, which is not information. The command refuses
instead, and says which two estates it was handed.

Documents with no scope are compared: a single-estate user pays nothing for a
guard against a mistake they cannot make.

## What is not here yet

**Nothing says how the two documents were made.** A plan compared against a
state, or a graph from one parser against a graph from another, will differ in
ways that are about the parsers rather than about the estate. `metadata.source`
records which parser wrote each one, and a warning when they disagree would
cost little — it is not there because no one has yet been surprised by it.

**A diff of two atlases.** Pages, and the ways between them, change too. The
comparison would be a different one — a page is a projection, so a change to a
page is usually a change to the graph it was projected from, already reported
here.
