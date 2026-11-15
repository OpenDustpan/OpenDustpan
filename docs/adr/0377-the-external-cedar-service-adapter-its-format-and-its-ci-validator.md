# 377. The external Cedar service adapter: its format, its CI validator and its outage rules

Status: accepted · 2026-11-14. Decided by the project owner

Changes:

- Extends ADR 224 decision 9: the external adapter speaks a small versioned format of OpenDustpan.
- Changes ADR 224 decision 8: CI runs the validator of the official Rust `cedar` CLI from a pinned image.
- Extends ADR 335 decision 7: this is the ADR of the external Cedar service adapter.

## Context

Gates are written in Cedar. The server links `cedar-go` as the default
adapter of the port `gate.Engine`. ADR 224 decision 9 (owner) also allows an
external adapter: "A second adapter calls an external Cedar service over
HTTP. An installation names one adapter for gates." It adds: "Each answer
echoes the request ID and the version of the policy set." ADR 335 decision 7
(owner, 2026-11-07) says: "A new adapter needs its own ADR, as ADR 224
decision 9 says."

The OpenSpec change `add-external-cedar-service-adapter` builds that adapter.
Its task 2.2 stopped on two points: which HTTP format the service speaks, and
which image runs the Rust Cedar validator in CI. ADR 224 decision 8 says: "CI
also runs the validator of the Rust Cedar implementation through the image of
the external Cedar service".

The facts, read on 14 November 2026:

- No standard Cedar service API exists. Cedar Agent
  (https://github.com/permitio/cedar-agent, Apache-2.0) has an HTTP API, but
  its answer does not echo a request ID or a policy-set version.
- AWS Verified Permissions has its own versioned `IsAuthorized` API, and a
  separate call for policy validation.
- The reference Rust Cedar tools, with the `cedar` CLI and its `validate`
  command, are in https://github.com/cedar-policy/cedar (Apache-2.0).

The project owner answered on 14 November 2026: "2.A". Option A is: "Own
format, official validator in CI", "with the outage rules exactly as ADR 224
decisions 8 and 9 say them".

## Decision

1. **The service speaks a format of OpenDustpan (owner, 2026-11-14).** The
   external adapter speaks a small, versioned JSON format over HTTPS that
   OpenDustpan defines. The version is part of the path. The format has two
   paths:
   - `evaluate`: the adapter sends the compiled policy set of a decision
     point, the entities and one request for each unit. The service answers
     with the matched policy IDs and the diagnostics for each unit.
   - `check`: the adapter sends a gate and the fact schema of its decision
     point at save. The service answers with the result of its validator and
     its diagnostics.

   Each request carries a request ID and the version of the policy set. Each
   answer echoes both. An `evaluate` answer names the matched policy IDs. The
   user documentation describes the format in `docs/reference`. A third-party
   service needs a small bridge to speak it.
2. **CI runs the official Rust validator from a pinned image (owner,
   2026-11-14).** CI runs the `validate` command of the official Rust `cedar`
   CLI of https://github.com/cedar-policy/cedar. The CLI comes from a pinned
   release, in an image that CI pins by digest. CI runs it on every fact
   schema and on every built-in gate and starter pack, as ADR 224 decision 8
   says. This image takes the place of "the image of the external Cedar
   service" in ADR 224 decision 8, because no standard service image exists.
3. **The outage rules stay as ADR 224 decisions 8 and 9 say them (owner,
   2026-11-14).** This ADR does not change them. The words of ADR 224
   decision 8 are: "When the external adapter is configured (decision 9), the
   save also calls the check operation of the service, and a refusal there
   refuses the save. When the service does not answer, a save that switches
   a gate to `evaluate`, removes it or sets `onError: skip` goes on with the
   linked validator only, and the audit entry says so. Every other save fails
   with `gate.ErrUnavailable`." The words of ADR 224 decision 9 are: "An
   answer that does not authenticate, does not echo both, or names an
   unknown policy ID gives `gate.ErrUnavailable`. Access limits (ADR 223)
   always use the linked adapter, so an outage of the service never refuses
   every API call."
4. **The adapter runs the conformance suite (owner, 2026-11-14).** The
   external adapter runs `pkg/gate/gatetest`, as ADR 335 decision 7 says. The
   test service of the suite speaks the format of decision 1.

## Consequences

- The change `add-external-cedar-service-adapter` gets its answers to open
  questions 1, 2 and 3. Its provisional answer to question 4 ("Store the gate
  after the local check and record the miss") does not agree with ADR 224
  decision 8. The spec follows decision 3 of this ADR instead.
- The CI step with the Rust validator is an edit of a workflow file. It
  follows the rules for workflow files. This ADR does not change them.
- The image of the `cedar` CLI is a new test tool in CI. A change of its
  pinned release or its digest follows ADR 7.
- The external adapter is security code. Its authentication of the service,
  its checks of the echo and its refusals each get their own hostile tests.
