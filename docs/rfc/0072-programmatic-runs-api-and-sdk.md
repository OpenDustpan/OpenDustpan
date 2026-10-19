---
rfc: 72
title: Programmatic plans and applies through the API and an SDK
status: draft
created: 2026-10-17
---

# RFC 72. Programmatic plans and applies through the API and an SDK

Status: draft. The question and the open questions only. This RFC proposes no design.

## Problem

A program may need to start a plan or an apply, wait for it, and read its result. Today the program starts the Pulumi CLI or the OpenTofu CLI as a child process and parses the output. The backend sees the result of the run, not the request that started it (RFC 6).

The Pulumi Automation API is a rival feature. It lets a program drive the Pulumi CLI from code. OpenDustpan is not affiliated with Pulumi. This draft has not checked whether OpenTofu offers an equivalent with a stable public contract.

Teams want the same control for both tools: start a run, follow it, get its result, and stop it when it is stuck. The control must work through the OpenDustpan API. A program that uses an SDK must not need the CLI on its host, unless the SDK is designed to run one.

## Related RFCs

- RFC 6 (runners and remote runs) defines the job that a runner claims. A programmatic run may be a job of that kind.
- RFC 29 (existing CI as the executor) covers CI jobs that report results to the backend. It does not cover a program that starts a run.
- RFC 30 (CLI-driven remote execution for OpenTofu) covers what the OpenTofu CLI needs for a remote run. This RFC may depend on it.

## Options

Each option is open. This RFC does not choose one.

- **REST API only.** Clients call the endpoints for runs. Each language writes its own client. This follows ADR 17, which requires a REST API for every feature.
- **REST API and a generated SDK.** The API stays the contract. An SDK wraps it for each language.
- **SDK that runs the CLI.** The SDK starts the Pulumi CLI or the OpenTofu CLI on the caller's host. This is close to the Pulumi Automation API. It needs the tools on the host, and it does not use the runner model.

## Open questions

- Which operations does the API start for each tool: plan or preview, apply or up, refresh, and destroy?
- Does a programmatic run start on a runner (RFC 6), or does it run in the caller's process? The answer changes who holds the cloud credentials.
- Can an SDK exist without a REST endpoint for the same action? ADR 17 says no for any feature.
- Which languages does the first SDK support?
- How does a program wait for an apply that needs an approval (RFC 13)?
- How does one API return a result for both tools, when ADR 12 keeps each tool's model apart?
- How does a program pass values and secrets to a run (RFC 5)?
- Which access token does a program use, and which actions may it take (RFC 20)?
- Does the run stream its log to the caller while it runs?
- Does this RFC wait for RFC 6 to be accepted?

## Outcome

None yet.
