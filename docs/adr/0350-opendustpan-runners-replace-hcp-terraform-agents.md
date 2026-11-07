# 350. OpenDustpan runners replace HCP Terraform agents, and a gap in security is a documented gap

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 254 decision 4: no `tfc-agent` against OpenDustpan. The TFE agent token routes refuse creation. A later RFC covers the agent pool routes.
- Extends ADR 133 decision 2: the join token is the only way to add a runner to a pool. This holds also for a user who comes from TFE agents.

## Context

ADR 254 decision 4 says that the TFE agent pool API is not served. RFC 30 asks if the server should serve it over runner pools. The question of the agent token is not answered.

HashiCorp's `tfc-agent` is the program that runs a plan for HCP Terraform and Terraform Enterprise. A TFE agent token is long-lived. A token can join many agents. The owner decided two points on 7 November 2026.

## Decision

Decided by the project owner on 7 November 2026.

1. **No `tfc-agent` (owner, 2026-11-07).** OpenDustpan does not run `tfc-agent`, and it does not serve the protocol of `tfc-agent`. The license of the agent forbids this use. The owner wrote: "If something is impossible because of licensing, it's impossible because of licensing." OpenDustpan runners replace HCP Terraform agents. The documentation gives no way to run `tfc-agent` against OpenDustpan.
2. **A TFE agent token is refused (owner, 2026-11-07).** A TFE agent token is long-lived and has many uses. A runner join token has one use and a short life (ADR 133 decision 2). When the server serves the routes of the TFE agent tokens, a request to create a token fails. The answer is a JSON:API error with a clear message. The message names the route that creates a join token: `POST /pools/{pool}/join-tokens` of the runner API. The documentation says why the token is refused.
3. **A rule for each tool-compatible API (owner, 2026-11-07).** When compatibility with a tool would weaken our security, we document the gap. We support only our own stronger mechanism. The owner wrote: "If supporting it would weaken our security, document that". The text goes on: "the decision is that we should only support our own stronger version." The rule holds for each tool-compatible API. The documentation of each gap says what the tool expects, what the server does and why.
4. **The agent pool routes are not decided (owner, 2026-11-07).** The pool management routes (level 1a) stay undecided. A later RFC covers them. Until the RFC is accepted, ADR 254 decision 4 holds: the server does not serve the TFE agent pool API.

## Consequences

- A team that uses TFE agents moves to runners. The migration guide for OpenTofu says so. It names the join token. It does not name `tfc-agent` as an option.
- A refusal is a fail-closed answer. The server never creates a TFE agent token as a long-lived credential.
- This ADR does not decide the CLI command that creates a join token. No CLI command exists today, and the error names the route. A later change can add the command name to the message.
- This ADR does not change the answer for `agent-pool-id` on a workspace (ADR 254 decision 4).
- The user documentation needs a page on runners for users of TFE agents, and a list of the gaps of the tool-compatible APIs.
