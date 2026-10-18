# 114. The policy port and its four default adapters

Status: accepted · 2026-10-17 · supersedes the "Policy engines" section of RFC 13

## Context

[RFC 13](../rfc/0013-policy-and-approvals.md) proposes a `Policy` port for
edit policy, and leaves the engines open. ADR 103 decides that
`job.Service.Start` asks the `job.Decider` port for a decision, and that a
policy that fails is not a denial.

The owner decided on 17 October 2026: policy and quotas are SDK ports with
default adapters. The policy adapters are the built-in Expr engine, OPA over
REST, Cedar and a signed webhook. Expr inside blueprints stays built in.

## Decision

1. **A tool-neutral package `pkg/policy`.** It holds the port
   `policy.Engine`, the input and decision types, the rule that combines
   decisions, and the contracts of the four adapters. It imports no transport
   and no store. depguard holds it to the rules of the domain packages.
2. **One port, one method.** `policy.Engine.Decide` takes a
   `policy.Input` and returns a `policy.Decision`:
   - The input has a kind, a schema version and a JSON document. Version 1
     has two kinds: `catalog.request` (a revision of a Catalog request at a
     recheck point) and `job.start` (the start of a job, ADR 103 decision 1).
     The document of each kind and version is a published JSON shape, because
     policies depend on it as a client depends on an API.
   - The decision has an effect (`allow`, `deny` or `approve`), the reasons,
     the approvals for `approve` (a number and the groups of the approvers),
     and the name and the version of the policy that decided.
   - An error is a failure of the policy, never a denial. It wraps
     `policy.ErrUnavailable` for an engine that does not answer in time and
     `policy.ErrInvalidDecision` for an answer that does not parse. The
     caller then changes nothing (ADR 113 decision 8).
   - A kind that an engine does not take gives `policy.ErrUnsupportedKind`.
     A namespace binds each engine to the kinds that it decides.
3. **The input of `catalog.request`.** The document holds the recheck point,
   the requester (subject, groups, agent and agent mode), the namespace, the
   blueprint name and version, the request name, the home, `ensure`, the
   operation (`create`, `update` or `destroy`), each answer with its old
   value, and the usage of each quota rule of the blueprint (ADR 115). It
   holds no secret value, because no answer is secret (ADR 111 decision 3).
4. **The strictest decision wins.** A namespace can bind more than one
   engine, and a blueprint has its own Expr rules. The Catalog asks each one,
   and `policy.Combine` joins the decisions:
   - One `deny` gives `deny`, with the reasons of each `deny`.
   - Else one `approve` gives `approve`, with the highest number of approvals
     and every group of each `approve`.
   - Else the result is `allow`.

   A table-driven test covers the rule.
5. **The built-in Expr adapter.** It runs the `policy` rules of a blueprint
   version (ADR 112 decision 7), and the rules of a namespace policy
   document in the same shape. The environment is the input document as
   `input`, with the aliases `inputs`, `request` and `usage` of ADR 112
   decision 6. A rule whose `when` gives true adds its effect. No rule that
   matches gives `allow`. The limits of ADR 112 decision 12 apply. The
   decision names the blueprint version or the namespace policy version.
6. **The OPA adapter.** It calls the Data API of an OPA server that the
   team runs:
   - `POST <url>/v1/data/<path>` with the body `{"input": <document>}`, over
     HTTPS, with a bearer token from a sensitive setting of the namespace,
     and no redirect.
   - The answer must be `{"result": {"effect": "...", "reasons": [...],
     "approvals": {"count": n, "groups": [...]}}}`. A missing `result` (an
     undefined decision) is `policy.ErrInvalidDecision`, not a `deny`, so a
     wrong path in the settings fails loud.
   - The `policyVersion` of the decision is the `revision` of the bundle when
     the server sends one with `?provenance`, else empty.

   The adapter needs only `net/http` in an adapter package, so it adds no
   dependency.
7. **The Cedar adapter.** It maps the input to one Cedar request:
   - principal `OpenDustpan::Principal::"<subject>"`, with the groups as
     parents `OpenDustpan::Group::"<name>"`,
   - action `OpenDustpan::Action::"<kind>"`, for example
     `"catalog.request"`,
   - resource `OpenDustpan::Blueprint::"<namespace>/<name>"`, or
     `OpenDustpan::Unit::"<unit reference>"` for `job.start`.
   - context: the input document.

   Cedar gives allow or deny only. So the adapter asks two requests: the
   action of the kind, and the same action with the suffix
   `.without-approval`. Deny on the first gives `deny`. Allow on the first
   and deny on the second gives `approve`, with the number of approvals and
   the groups of a setting of the namespace. Allow on both gives `allow`. The
   reasons are the IDs of the policies that decided. The adapter needs a
   Cedar library, `github.com/cedar-policy/cedar-go`, which is a new
   dependency. The PM decides on it before the adapter pull request.
8. **The signed webhook adapter.** It sends the input to a URL that the
   team runs:
   - `POST <url>` with the body `{"kind": ..., "schemaVersion": ...,
     "document": ...}`, over HTTPS, with no redirect.
   - The header `Dustpan-Signature: t=<unix seconds>,v1=<hex>` signs the
     body. `<hex>` is the HMAC-SHA256 of `<unix seconds>.<body>` with the
     shared secret of the hook, in lowercase hex. The header
     `Dustpan-Delivery` gives a new ID for each call.
   - The receiver refuses a signature that does not match and a time more
     than 5 minutes from its clock. `policy.Sign` and `policy.Verify` hold
     the rule, and a receiver written in Go can use them. A table-driven
     test covers both.
   - The answer must be the JSON of a decision: `{"effect": ..., "reasons":
     [...], "approvals": {...}, "policyVersion": "..."}`.
   - The secret is a sensitive setting of the namespace. Two secrets can be
     active during a rotation: the server signs with the newest one and
     sends one `v1` for each.
9. **Limits of every adapter.** A call has a time limit of 5 seconds. An
   answer is at most 64 KiB, with at most 32 reasons of at most 1 KiB each.
   A decision of `approve` needs from 1 to 10 approvals. An answer outside
   these limits is `policy.ErrInvalidDecision`.
10. **The job decision uses the same port.** The `job.Decider` adapter of the
    server asks the engines that the namespace binds to `job.start`, and
    joins the result with the decision that the Catalog recorded for the
    revision of the job (ADR 116 decision 6). So a team writes one policy for
    both, in the engine of its choice.
11. **Every decision is in the record.** A revision stores, for each engine
    that it asked at each recheck point: the engine, the policy version, the
    effect, the reasons and the SHA-256 of the input document. The audit
    entry of the submit and of a rejection holds the combined effect and the
    policy versions (ADR 117 decision 6). The input document is in the
    revision, so a person can read what the policy saw.
12. **A conformance suite.** `pkg/policy/policytest` gets a fixture that
    each adapter implements, with a small set of policies: allow all, deny a
    blueprint by name, ask for 2 approvals by the group `dba`, and a policy
    that times out. Each adapter runs the suite against a real engine in its
    own test: an OPA server, the Cedar library, or a webhook receiver in the
    test.

## Consequences

- A team uses the policy engine that it already runs. The server needs no
  embedded OPA.
- A wrong OPA path or a broken webhook fails the submit. It never lets a
  request through, and it never denies one by mistake.
- A team that writes Cedar sets the approvers in a namespace setting,
  because Cedar has no effect other than allow and deny.
- Two network calls can run at each recheck point. The time limit of each
  is 5 seconds, so a submit can take 10 seconds when two engines are slow.

## Open questions

- Does the PM accept `github.com/cedar-policy/cedar-go` as a dependency, or
  does the Cedar adapter call a Cedar service over HTTP that the team runs?
- Does a namespace bind engines to a collection or to a blueprint, or only
  to the namespace?
- Does the server cache an `allow` for the same input digest within one
  recheck point, so that a retry does not call the engine again?
- Which defaults does the job decision give with no policy installed
  (ADR 103 "Open questions")? This ADR gives none.
