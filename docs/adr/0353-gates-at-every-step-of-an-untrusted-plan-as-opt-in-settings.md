# 353. Gates at every step of a plan that runs untrusted code: every control is an opt-in setting

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 213 decision 2: a trusted source needs more than a branch name in `protectedBranches`. A team can use `proxy-only` credentials for jobs of an untrusted source.
- Extends ADR 224 decision 2: a seventh decision point, `change.plan`, before each plan phase.
- Changes ADR 224 decision 14: a requirement of `change.plan` can block the plan phase, and a run signature counts only for `change.plan`.
- Changes ADR 226 decisions 2 and 4: each fact group names its origin, and the groups `checkSteps` and `ownership` are new.
- Changes ADR 226 decision 10: a check can also run as a check step on a runner.
- Extends ADR 226 decision 13: the paths come from two trees that the server read.
- Extends ADR 100 decision 4: a fifth phase kind, `check`.
- Extends ADR 102 decision 7: the two plan parts must agree. The plan binding holds the profile revisions and the digest of the proxy rule set.
- Extends ADR 203 decision 1: the server derives the consumer `job-proxy` from the proxy session.
- Extends ADR 330 decisions 2, 4 and 7: isolation, read-only checks, the header scan and the tunnel rule for an untrusted source.

## Context

[RFC 122](../rfc/0122-gates-at-every-step-of-an-untrusted-plan.md) describes a team that runs a plan of code from an untrusted source with a privileged credential. It gives a chain of seven steps with a gate at each step, 12 options with a recommendation, and 12 open questions for the owner. This ADR does not repeat the design. Read the RFC for the threat, the chain, the hardening items H1 to H10 and the residual risks.

On 7 November 2026 the project owner accepted all 12 recommendations of the "Open questions" of the RFC. The owner made two changes in the owner's words: "they're opt in settings not forced", and "Proxy only for applies can be allowed too".

## Decision

Decided by the project owner on 7 November 2026.

1. **Every control is a setting that a team turns on (owner, 2026-11-07).** The owner said: "they're opt in settings not forced". This covers `untrustedSourceCredentials`, the gates of `change.plan`, check steps, isolated pools, budgets of the job proxy and the other controls of the RFC. A control that the RFC calls a default is a recommended value that the team can choose. It is not forced on a team.
2. **The default keeps today's behavior (owner, 2026-11-07).** Until a team turns a setting on, a unit behaves as it does on `main`. The docs and the console make each setting visible. The API shows the effective value of each setting, with the place that set it (see "No magic" in `CLAUDE.md`). The console shows a warning for a unit with an untrusted source and the value `direct`.
3. **Proxy-only for applies (owner, 2026-11-07).** The owner said: "Proxy only for applies can be allowed too". A team may set `trustedSourceCredentials` to `proxy-only`, so that an apply of a trusted source also gets no real credential. The value `direct` stays the default. The docs state the residual risk of `direct`: the Pulumi CLI can store the credential in the state as a secret.
4. **Accepted questions (owner, 2026-11-07).** The owner accepted the recommendation of each of the 12 questions of the RFC, as the settings of decision 1:
   1. A trusted source has the repository ID and an exact name in `protectedBranches`. It has no merge around the gate and no force push since the last approved apply. It also has the branch rules of the Git host that the server read. When the Git host cannot report them, the server treats the source as untrusted.
   2. A middle credential level: `proxy-only` for each job of an untrusted source.
   3. No secrets and no unmasked state for a job of an untrusted source under `proxy-only`, whoever starts the job.
   4. A trusted apply keeps `direct` as the default (decision 3 lets a team choose `proxy-only`).
   5. A new decision point `change.plan` before each plan phase, with a run signature that never counts as the approval of a plan.
   6. The save refuses an approve-and-run gate that reads an unverified fact. It also refuses `onError: skip` at `change.plan`. For a job of an untrusted source, an approval gate that reads an unverified fact always asks for its approval. This rule also covers the `unless` of an approval default (a note from the research for RFC 91). An `unless` condition that reads an unverified fact never removes an approval requirement.
   7. Ownership facts from the stored ownership rules, `false` when a changer is unknown. The code owners file of the Git host comes later.
   8. Scanners as check steps in a check phase of their own, or as external checks. A check step runs in a fresh sandbox with no credential, and its result is an unverified fact that only the server judges.
   9. A pool for untrusted sources, with `firecracker`, `gvisor` or a sandboxed Kubernetes runtime class, that only untrusted sources use.
   10. A check that the plan people read and the plan the apply follows show the same changes. It runs in Go for the Pulumi CLI. It runs as a render step in a fresh sandbox for OpenTofu.
   11. Body matching of a proxy rule and a tester that replays past requests against a changed rule set.
   12. A budget for a plan phase of the job proxy. The values are 10,000 requests and 1 GiB for a plan phase. They are 5 minutes and 64 MiB for one stream. The API shows the values. Each unit can change them.
5. **The rest of the proposal (owner, 2026-11-07).** The rest of the proposal of the RFC applies as settings of decision 1. It has these items:
   - the facts of the job proxy.
   - the check at the job start for a credential that also reaches the job.
   - the starter pack for an owner review of the program.
   - the binding of profile revisions and the digest of the proxy rule set.
   - a read route for the chain.
6. **Security rules stay (owner, 2026-11-07).** A control that a team turns on is never weaker than the rules of ADR 250 in `CLAUDE.md`. A setting can turn a control off only by an explicit stored value that has a record of who set it and when.

## Consequences

- RFC 122 has the status accepted, with a pointer to this ADR.
- The work of each item is a slice with its own pull request and its own e2e step. The tool-compatible APIs do not change.
- The GLOSSARY gets the terms of "Proposed glossary lines" of the RFC when the first slice lands.
- ADR 347 decision 3 (the strict installation setting) can turn the warning of decision 2 into a block. Decision 4 of ADR 347 is not changed.
