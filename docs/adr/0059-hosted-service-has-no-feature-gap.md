# 59. The hosted service has no feature that the open-source server lacks

Status: accepted · 2026-10-13. Decision 6 superseded in part by ADR 215: the hosted service is multitenant, with the namespace as the tenant

## Context

On 2026-10-13 the project owner chose a hosted service on the model of
Temporal. The product manager relayed the owner direction:

1. The open-source server stays feature complete.
2. The hosted service sells only operations: the same software, run by the
   project, at a flat price.
3. No feature is gated to the hosted service. SSO, MFA, audit and access
   control stay in the open-source server.
4. Hosted limits and defaults are installation settings (RFC 68), not code
   paths. Sign-up and billing stay outside the server binary.

The owner also decided on one hosted tier at a flat price for each
organization, with no free tier, no trial and no dedicated tier. Fair-use
limits on storage, history and request rate are installation settings
(RFC 68).

ADR 22 makes the project Apache-2.0 with no contributor license agreement,
so the project cannot relicense the code. ADR 35 makes the server a library:
`pkg/dustpan` builds a server from ports, adapters and options.

The project lead asked the owner to approve an ADR with the title of this
one. The owner answered: "Yes but I need to clarify one point for the ADR.
Please sync with the product lead about this. We will need some shims to
handle billing, onboarding, and `namespaces` would probably need to be
re-worded as accounts in the UI language and the settings would need to be
cut down a bit for a hosted version. I am not proposing to cut features but
we might need to keep a small fork or something. The application is already
consumable as a library so it might not need to be a fork per-se but still.
On that note, the frontend should probably be just as pluggable as the
backend."

On the word "account", the owner chose "Console relabel only", and asked how
names stay apart between customers. On the source of the hosted
distribution, the owner chose public source under Apache-2.0: "Public,
Apache-2.0 is absolutely WILD for a hosted product. I kinda like the
craziness of it." The owner then said yes.

The project lead proposed that the infrastructure code of the hosted service
is public too. The owner answered: "That's a pretty bold advertisement for
the product. Yes, secrets and such can live in Dustpan and we can use
Dustpan runners instead of relying on GitHub Actions runners for IaC
changes."

The product manager then relayed three more owner answers on 2026-10-13:
the longest embargo is 14 days, the hosted distribution runs the same e2e
suite and exit tests as the open-source server, and "The private security
release process needs to be in the open."

## Decision

### Features

1. **No feature gap.** The hosted service never offers a feature that the
   open-source server lacks. It never turns a feature off for its
   customers.
2. **The test.** If an administrator of a namespace on a self-hosted
   installation can do something, an administrator of a hosted customer can
   do it too. A pull request that breaks this rule needs a new ADR.
3. **No entitlement checks.** The server has no plan, edition or license
   check, and no feature flag that depends on a plan. The billing shim reads
   only the fair-use limits, which are installation settings (RFC 68). It
   never switches a feature on or off.

### The hosted distribution

4. **A distribution, not a fork.** The hosted service is a separate
   distribution. It builds the server with the public SDK (`pkg/dustpan`,
   ADR 35) and the console with its public extension points (decision 7).
   It is not a long-lived fork of either repository.
5. **What the distribution holds.** It holds only these:
   - the shims for sign-up, onboarding and billing, outside the server
     binary,
   - the hosted defaults and fair-use limits, as installation settings
     (RFC 68),
   - a static settings layer that locks the operator settings: the
     database, blob store, TLS, listen address, backup target, key files,
     installation limits and fair-use limits,
   - the console labels of decision 6.

   A hosted customer can change every setting that a namespace
   administrator can change on a self-hosted installation.
6. **"Account" is a console label only.** The hosted console shows the word
   "account" where the open-source console shows "namespace". The API, the
   CLI and every error text keep "namespace", and the Pulumi CLI keeps
   "organization" (ADR 18). There is no account object in the server. Each
   hosted customer has its own installation and its own host name, and a
   namespace name is unique in its installation, so two customers cannot
   have the same name in one place.
7. **The console is as pluggable as the server.** The console gets public
   extension points for labels and wording, navigation entries, settings
   sections that a distribution hides, and slots for extra screens, such as
   sign-up and billing. The theme of RFC 9 uses the same extension points,
   so a self-hosted installation can use each one too. A console RFC, under
   ADR 58, designs them.
8. **Public source.** The hosted distribution is public source under
   Apache-2.0. Only deployment secrets and the configuration of the
   project's own accounts stay private.

### The infrastructure of the hosted service

9. **Public infrastructure code.** The infrastructure code of the hosted
   service is public under Apache-2.0. It follows the reference
   architectures that the owner chose.
10. **What stays private.** These never go into the public repository:
    - secrets,
    - account, project and subscription identifiers,
    - resource names that someone else can claim, such as bucket and DNS
      names,
    - network ranges and allowlists,
    - state.

    The public code takes them as inputs.
    Their values live in OpenDustpan stack and workspace configuration and
    secrets, or in a small private configuration repository.
11. **OpenDustpan deploys it.** The state, configuration and secrets of the
    hosted infrastructure live in OpenDustpan. A separate operations
    installation holds them, not the installations that it deploys, so a
    failed installation never holds the state that is needed to fix it.
    The operations installation keeps backups and tested restores (ADR 30).
12. **No deploy credentials in public CI.** A pull request from a fork runs
    checks only, never a plan or an apply against real accounts. Plans and
    applies run from protected branches with workload identity (ADR 54).
    They run on OpenDustpan runners (RFC 6) once those exist. Until then
    they run on CI runners that the project controls. A plan output that
    holds an identifier never goes to a public log.

### Releases

13. **Only merged code.** The hosted service runs only code that is merged
    to the main branch of the public repositories. It can run ahead of the
    latest tagged release, for example for an urgent fix. The owner said on
    2026-10-13, as the product manager relayed: "The hosted service might
    run ahead of the tagged release at times if e.g. it needs an urgent
    hotfix. It should in principal never run code that isn't MERGED
    upstream."

14. **Embargoed security fixes.** During an embargo, the hosted service
    can run a security fix from a private security advisory fork. The fix
    merges upstream and is released when the problem is disclosed. The
    owner accepted this exception on 2026-10-13: "the embargoed security fix
    situation is a necessary evil since we're forced to patch before
    disclosure."

    The fix merges upstream and is released no more than 14 days after the
    hosted service starts to run it. Only the fix is private during the
    embargo. The process is public: `SECURITY.md` tells how to report, and
    describes the private advisory fork, the 14-day limit, the hosted
    service that runs the fix, and the merge and release at disclosure.

15. **The same tests.** The hosted distribution runs the same e2e suite and
    the same exit tests as the open-source server.

### Experiments

16. **No A/B tests.** The hosted service runs no experiment that gives some
    customers a different behavior from others. It has too few users for a
    valid result, it makes a feature gap, it needs behavior telemetry, and
    it changes behavior that a regulated customer did not ask for.
17. **Early access is an opt-in setting.** An experimental feature merges
    upstream behind a setting that is off by default. Self-hosted
    installations get the same setting. A hosted customer turns it on on
    purpose. Feedback comes from the people who turned it on, not from
    telemetry. The hosted service is still the first to run merged code,
    before the tagged release (decision 13).

## Not decided here

- The names and places of the hosted distribution repository and the
  infrastructure repository.

## Consequences

- RFC 68 gets the rule of decision 3 as a check on each new setting.
- GLOSSARY.md gets "hosted distribution", and notes that "account" is a
  label of the hosted console, not a term of the API.
- The extension points of decision 7 need a console RFC.
- `SECURITY.md` describes the embargo process of decision 14.
- The infrastructure repository runs the same secret scans as this
  repository (`make trufflehog`, `make semgrep`).
