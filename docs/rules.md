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

## Determinism, and where a moment comes from

Nothing in `views` reads a clock. `--since 30d` is resolved by the command,
against the caller's clock, and **is handed in with the document** — a rule that
named no moment is completed with it before it is judged, which is why the flag
can answer what `is: quiet` needs. A rule that names its own keeps it: a
document that says "since the first of August" means it whoever runs it and
whenever.

**A rule takes a moment, never a span.** A document has no clock, so `"since":
"30d"` in a file is refused where it is written. Left alone it would reach a
comparison that falls back to comparing the text — where every timestamp sorts
before the letter `d`, so every subject fires, and under `--exit-code` a
pipeline stops for nothing.

`oekaki validate` checks a rules document on its own: no run, no clock, no
moment to lend it. A rule that needs one has to carry it, and hears so there
rather than the first time somebody runs it.

So the same document, the same graph and the same moment produce the same
alerts, which is what makes an alert something you can commit, diff, and argue
with.

## A window is about readings that have a time

A reading with no `observed_at` is not old — it is undated. It stays in every
bound, because dropping it would take every reading from a collector that
records no time out of every rule, quietly and for a reason nobody could see.
Where two readings compete for "newest", one with a time wins.

`quiet` is the exception, and for the same reason rather than against it.
Silence is the claim that *nothing arrived*, and an undated reading says
something arrived and says nothing about when. It is not silence, so the rule
does not fire; it cannot be placed inside the window either, so the rule does
not pass. The rule is simply not answering the question for that subject, and
the run says so on stderr:

```console
$ oekaki alerts graph.json --rules rules.json --since 30d
stopped: ledger measured with no time on the reading, so there is no telling
whether it went quiet; not reported
1 rule, nothing fired
```

Which is the thing worth saying out loud, because the alternative was worse in
both directions: treating an undated reading as older than every moment fired
every subject of such a collector on every run, and dropping it silently would
have left a `quiet` rule that can never fire and never says why.

## Where the declared side comes from

A rule about routes needs both sides of the comparison. When the graph carries
no declared routes, they are derived by following declared references — the
same thing `oekaki paths` does, and the run says so on stderr.

When *none* can be derived either, the run says which of the two happened,
because they are different situations with different things to do about them:

| | |
| --- | --- |
| this graph records no declared calls to follow | a graph built from traces alone. Nothing is wrong with it; the declared side simply is not there yet, and belongs in an overlay |
| everything that calls something is also called by something | there is nowhere a route starts. An estate whose entry point sits inside a cycle looks like this |

Silence there reads as "everything observed is a surprise", which is exactly
what a rule about unexpected routes then reports: one alert per route, none of
them about anything that happened.

## In a pipeline

`--exit-code` makes the command exit 1 when anything fired. Without it the
command reports and carries on, because a listing is also something people read
while nothing is wrong.

`-f json` writes the same run for something else to read:

```json
{
  "since": "2026-08-07T00:00:00Z",
  "unanswered": [
    "stopped: ledger measured with no time on the reading, so there is no telling whether it went quiet; not reported"
  ],
  "alerts": [
    { "rule": "too busy", "subject": "ledger", "is": "above",
      "metric": "request_rate", "value": 4000, "last_seen": "2026-09-05T00:00:00Z",
      "reason": "request_rate is 4000, above 1000" }
  ]
}
```

`unanswered` is there for the same reason the stderr line is: an empty `alerts`
is what a rule that was applied and found nothing produces, and it is also what
a rule that could not be applied at all produces. Something reading only
`alerts` cannot tell those apart, and one of them is not good news.

`is` says which condition fired, and it is what the rest of the alert is read
in the light of. The `reason` is a sentence written for that condition — a
bound names its value in it, a silence names its moment — so anything deciding
what to do with `value` and `last_seen` asks `is` rather than searching the
sentence for the digits. The table does exactly this, and it is not a nicety:
a quiet reason contains the moment, a moment contains `0` and `1` and `2026`,
and a heartbeat of `0` under a rule called "stopped" is the reading somebody
most wants to see.
