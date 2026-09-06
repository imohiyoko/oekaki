# Rules: when to say something is wrong

A path listing answers "what do the declared and the observed routes say about
each other". A rule answers the next question — *and which of those do I want
to be woken for* — and it answers it as a **document**.

```console
$ oekaki alerts graph.json --rules rules.json --since 30d
2 declared routes derived by following references; nothing wrote them down
3 rules, 3 fired
page  a route fired that nothing declares
    gateway → ledger
    something walked this route and no declared route contains it in this order
cleanup  a declared route nothing walks any more
    gateway → reports
    path_requests last measured 2026-05-01T10:00:00Z
```

## Why a document and not an expression

An expression language is the obvious answer and the wrong one here. A document
can be read by somebody who did not write it, reviewed in a pull request, and
diffed between two versions of an estate. An expression needs an evaluator, an
evaluator needs a sandbox, and a sandbox is a thing to maintain forever in a
program whose point is somewhere else.

The cost is real and worth saying plainly: **a rule cannot say anything
`views/alerts.go` does not already know how to compute.** The escape hatch is
the boundary this project already has. A collector holds the credentials and
the vendor's own query language, works out whatever it likes — a seasonal
baseline, a percentile over a fortnight, a model — and writes its conclusion
back as an ordinary observation. A rule can then be about that.

## A rule

```json
{
  "kind": "oekaki.rules",
  "version": "0.1",
  "rules": [
    {
      "name": "a route fired that nothing declares",
      "severity": "page",
      "when": { "is": "unexpected" }
    },
    {
      "name": "a declared route nothing walks any more",
      "severity": "cleanup",
      "about": { "kind": "path" },
      "when": { "is": "quiet", "metric": "path_requests", "since": "2026-08-01T00:00:00Z" }
    },
    {
      "name": "the checkout service is over its limit",
      "severity": "page",
      "about": { "subject": "aws_ecs_service.checkout" },
      "when": { "is": "above", "metric": "cpu", "value": 90 }
    }
  ]
}
```

**`name` is a sentence, not an identifier.** It is what a person reads at three
in the morning, and "a route fired that nothing declares" is a better thing to
wake up to than `rule-7`.

**`severity` is the caller's vocabulary, not this program's.** An unused route
in a service being retired is the goal and one in a payment system is an
incident, and only the person who wrote the rule knows which estate they are
in. Nothing here ranks them.

## What a rule can be

| `is` | fires when |
| --- | --- |
| `above` | the newest reading of a metric is over a bound — the spike |
| `below` | …under one, which is the silence a collector reports as a zero |
| `quiet` | nothing has been measured since a moment — the silence a bound reads as healthy, because no number arrived at all |
| `unexpected` | something walked a route no declared route contains in that order |
| `unused` | a declared route nothing has ever been seen walking |
| `partial` | a declared route walked as far as some hop and never further |

The last three need no metric and no baseline: the finding *is* the comparison,
and it is the same comparison [`oekaki paths`](paths.md) makes — one reading of
it, wherever it is asked for.

A bound is about the **newest** reading. A service that was over its limit last
week and is not now is not something to wake somebody for.

## What a rule is about

| `about` | |
| --- | --- |
| `subject` | one node id, or one path key |
| `type` | every node of a resource type |
| `through` | every route that passes through a node |
| `kind` | `node` or `path`; absent means both |

`kind` earns its place on `quiet`. A rule asking what has stopped reporting a
*route's* metric, with nothing said about what it is about, is also asking about
every box in the estate — and every box has indeed never reported a metric that
was never about boxes. The answer is true and useless, which is the worst kind
of alert.

## Determinism

Nothing in `views` reads a clock. `--since 30d` is resolved by the command,
against the caller's clock, and fills in for any rule that did not name its own
moment; the resolved moment is written into the JSON output. A rule that names
its own keeps it: a document that says "since the first of August" means it
whoever runs it and whenever.

So the same document and the same graph produce the same alerts, which is what
makes an alert something you can commit, diff, and argue with.

## Where the declared side comes from

A rule about routes needs both sides of the comparison. When the graph carries
no declared routes, they are derived by following declared references — the
same thing `oekaki paths` does, and the run says so on stderr. Without that,
every observed route would arrive as a surprise, because the declared side was
empty rather than because anything was.

## In a pipeline

`--exit-code` makes the command exit 1 when anything fired. Without it the
command reports and carries on, because a listing is also something people read
while nothing is wrong.
