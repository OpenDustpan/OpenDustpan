# RFCs

An RFC holds a question that is not decided yet: the problem, the options,
the arguments and the open questions. It can be rough, incomplete or wrong.
An ADR in `docs/adr/` holds only a decision (ADR 15).

## Life of an RFC

1. Copy `0000-template.md` to the next free number, with a short slug:
   `0005-short-slug.md`. Set `status: draft`.
2. When it is ready for others to read, set `status: discussing`. Edit it as
   often as you like. The pull request and the file's history hold the
   discussion.
3. When the project owner accepts it, write a short ADR with the next ADR
   number. The ADR holds the decision and links the RFC. Then set
   `status: accepted` and `adr: <number>` in the RFC.
4. When an RFC is not taken, set `status: dropped` and say why in its last
   section. Keep the file, so that the same idea does not come back without
   its history.

An accepted or dropped RFC does not change after that. A new idea gets a new
RFC.

## Front matter

```yaml
rfc: 4                 # the number in the file name
title: What sits under a namespace
status: draft          # draft | discussing | accepted | dropped
created: 2026-10-10
adr: 16                # only when accepted
was: proposed ADR 9    # only for RFCs 1 to 3, which were proposed ADRs
```

The front matter and the first lines of prose give the same state. Change
both together.

## Rules

- An RFC never says what the code does today as if it were decided. Code that
  an RFC describes can exist, but it is not a decision until an ADR says so.
- `make docs-lint` does not check RFCs, so that writing one costs little. The
  ADR that comes from an RFC follows ADR 14.
- RFCs 1 to 3 were proposed ADRs 9 to 11. ADRs 12 and 13 cite those numbers.
  ADR 1 forbids edits to accepted ADRs, so read "ADR 9" there as RFC 1,
  "ADR 10" as RFC 2 and "ADR 11" as RFC 3.

## Index

| RFC | Title | Status |
| --- | --- | --- |
| [1](0001-opentofu-via-the-tfe-compatible-api.md) | OpenTofu support through the TFE-compatible remote API | accepted |
| [2](0002-organizations-are-namespaces.md) | Organizations are namespaces, in every client | accepted |
| [3](0003-domain-modules-for-updates-secrets-and-state.md) | Domain modules for updates, secrets and OpenTofu state | accepted |
| [4](0004-what-sits-under-a-namespace.md) | What sits under a namespace | discussing; collections and CLI placement accepted (ADR 51, ADR 61) |
| [5](0005-profiles.md) | Profiles, shared config and secrets for every tool | discussing; the core accepted (ADRs 200 to 209) |
| [6](0006-runners-and-remote-runs.md) | Runners and remote runs | draft |
| [7](0007-one-binary-many-roles.md) | One binary, many roles, removable features | accepted (ADRs 211, 249) |
| [8](0008-backups-and-restore.md) | Backups and restore, built in | accepted; the backup of one namespace (ADR 218) |
| [9](0009-themes-and-white-labeling.md) | Themes and white-labeling | draft; the branding preset setting accepted (ADR 212), operator branding accepted (ADR 235) |
| [10](0010-artifact-registry-and-curated-upstreams.md) | Artifact registry and curated upstreams | draft |
| [11](0011-languages-and-locales.md) | Languages and locales | draft |
| [12](0012-api-edits-and-write-back.md) | API edits, and write-back to git | draft; forge connection credentials and defaults accepted (ADR 213) |
| [13](0013-policy-and-approvals.md) | Policy and approvals | accepted (ADRs 222 to 230); the points of RFC 77 decided |
| [14](0014-blueprints.md) | Blueprints, self-service units with validated inputs | accepted |
| [15](0015-output-sharing.md) | Output sharing between units | draft; output reads of a job, on two paths, accepted (ADR 213) |
| [16](0016-egress-proxy.md) | Egress proxy for jobs, with request rules and credential injection | draft |
| [17](0017-unit-lifecycle.md) | Stack and workspace lifecycle: delete, rename, move, tags | draft |
| [18](0018-state-operations.md) | State operations: history, restore, locks and leases | accepted; the manual lock (ADR 213) |
| [19](0019-users-teams-roles.md) | Users, teams and roles | discussing; groups, grants and export accepted (ADR 52), the planner role (ADR 212) |
| [20](0020-access-tokens.md) | Access tokens and machine identity | discussing; token life cycle and workload identity accepted (ADR 53, ADR 54), SMTP settings (ADR 212) |
| [21](0021-directory-sync-and-sign-in.md) | Directory sync and sign-in hardening | discussing; MFA, sign-in methods, MFA freshness and break-glass accepted (ADR 46, ADR 55, ADR 56, ADR 57) |
| [22](0022-audit-log.md) | Audit log | discussing; port, entries, sinks and opt-in tamper evidence accepted (ADR 60), trusted proxies and the restore entries (ADR 212) |
| [23](0023-events-and-notifications.md) | Events, notifications and webhooks | accepted |
| [24](0024-run-triggers-and-orchestration.md) | Run triggers and orchestration between units | draft |
| [25](0025-drift-detection.md) | Drift detection and remediation | draft |
| [26](0026-unit-status.md) | Unit status in the inventory | draft; a lock is a flag, with manual locks (ADR 213) |
| [27](0027-pull-request-automation.md) | Pull request automation | draft |
| [28](0028-stored-plans.md) | Stored plans and plan-bound approvals | accepted |
| [29](0029-existing-ci.md) | Using an existing CI as the executor | accepted |
| [30](0030-opentofu-remote-execution.md) | CLI-driven remote execution for OpenTofu | draft |
| [31](0031-kubernetes.md) | Kubernetes: runner, operator, and blueprints as resources | draft |
| [32](0032-object-storage.md) | Object storage adapters | accepted; no bucket of the customer (ADR 221) |
| [33](0033-customer-managed-keys.md) | Customer-managed keys | accepted |
| [34](0034-server-observability.md) | Observing the OpenDustpan server | accepted |
| [35](0035-packaging.md) | Packaging and installation | draft; TLS, binaries and the image accepted (ADR 27), the Helm chart accepted (ADR 246) |
| [36](0036-high-availability.md) | Scale out to several instances on one database | accepted |
| [37](0037-federation.md) | Namespace placement and routing | draft; hosted multitenancy decided (ADR 215), opt-in routing and replication in RFC 79 |
| [38](0038-upgrade-checks.md) | Upgrade checks | accepted |
| [39](0039-reading-state.md) | Reading state contents | accepted |
| [40](0040-inventory-queries.md) | Inventory search, saved views and graph queries | draft |
| [41](0041-agent-interface.md) | An interface for AI agents | draft |
| [42](0042-migration-import.md) | Migration from other backends and operators | accepted (ADR 29); the import resource and the source adapters decided by ADRs 241 to 244 |
| [43](0043-ephemeral-units.md) | Ephemeral units and time to live | draft |
| [44](0044-bulk-operations.md) | Bulk operations | draft; bulk calls accepted (ADR 213) |
| [45](0045-cost-estimation.md) | Cost estimation | draft |
| [46](0046-insights.md) | Insights: dashboards and reports | draft |
| [47](0047-discovery-and-codification.md) | Discovery and codification of unmanaged resources | draft |
| [48](0048-management-as-code.md) | Managing OpenDustpan as code | draft |
| [49](0049-run-tasks.md) | Run tasks: external checks with callbacks | accepted (ADR 226) |
| [50](0050-policy-authoring.md) | Policy authoring, testing and compliance packs | draft; decision records, simulate and starter packs accepted (ADR 229) |
| [51](0051-change-management-integrations.md) | Change management and portal integrations | draft; Backstage plugin accepted (ADR 247) |
| [52](0052-console-core.md) | Console core views and navigation | draft; the console reads the capabilities document (ADR 211) |
| [53](0053-terraform-cli.md) | Terraform CLI support | draft |
| [54](0054-sources-beyond-git.md) | Sources other than git | draft |
| [55](0055-environment-promotion.md) | Environment promotion and comparison | draft |
| [56](0056-upgrade-pull-requests.md) | Upgrade pull requests for providers, modules and blueprints | draft |
| [57](0057-documentation.md) | Documentation, examples and agent skills as a product | draft; docs in the same pull request accepted (ADR 214) |
| [58](0058-large-request-limits.md) | Bound memory of large request bodies | accepted |
| [59](0059-installation-api.md) | Shape of the installation API served by api/server | accepted |
| [60](0060-maintenance-mode-visibility-wait.md) | Wait until every instance sees maintenance mode before a restore applies | accepted |
| [61](0061-docs-site-and-landing-page.md) | A docs site and a landing page | discussing |
| [62](0062-pulumi-journaling.md) | Pulumi journaling | accepted |
| [63](0063-workspace-lock-holder.md) | A workspace lock belongs to a principal, not to a client | accepted |
| [64](0064-pulumi-journaling-architecture.md) | Architecture of Pulumi journaling support | accepted |
| [65](0065-opentofu-http-backend.md) | OpenTofu through the http backend too | accepted |
| [66](0066-moving-units-between-namespaces-and-installations.md) | Tooling to move units between namespaces and installations | draft; the import tool and the move check of one unit, then of a namespace, accepted (ADRs 241 to 244) |
| [67](0067-spend-and-cost-tool-integration.md) | Integration with spend and cost tools (FinOps) | draft |
| [68](0068-installation-settings.md) | Installation settings: inventory, settings through the API, and live reload | draft; fresh MFA for weaker settings and one settings API accepted (ADR 212), the namespace as the tenant (ADR 215) and a lock that turns a feature off (ADR 219) |
| [69](0069-community-channels.md) | Community channels: announce list, Discussions, and a later chat | discussing |
| [70](0070-authorization-model-findings.md) | Authorization model findings: act-as reach, restores, trust rules and a model checker | accepted |
| [71](0071-signed-audit-log-segments.md) | Export signed audit log segments to the blob store for offline verification | draft |
| [72](0072-programmatic-runs-api-and-sdk.md) | Programmatic plans and applies through the API and an SDK | draft |
| [73](0073-ai-agent-for-infrastructure.md) | An AI agent for infrastructure across both tools | accepted |
| [74](0074-snapshots-of-files-with-no-git-commit.md) | Snapshots of files that have no git commit | draft |
| [75](0075-commands-by-intent.md) | Commands by intent: a dev server with no setup, and a serve command that needs decisions | accepted (ADRs 237 and 238) |
| [76](0076-hosted-multitenancy-open-questions.md) | Hosted multitenancy, the open questions after the namespace decisions | draft |
| [77](0077-open-points-of-the-gate-model.md) | Open points of the gate model | accepted (ADRs 222 to 230) |
| [78](0078-localization-open-questions.md) | Localization open questions: translated docs, the translation machine, the server formatter and the edges | discussing; translated docs, the translation agent, the formatter, the product name in chat, `resolvedLanguage` and reports of one string accepted (ADRs 232, 234 and 235) |
| [79](0079-opt-in-federation-routing-and-geo-replication.md) | Opt-in federation of installations, routing and geo-replication | dropped (superseded by ADR 248) |
| [80](0080-console-sessions-on-the-tfe-compatible-api.md) | Console sessions on the TFE-compatible API, and fresh authentication for force-unlock and the state download | draft |
| [81](0081-open-questions-of-the-import-tool.md) | Open questions of the import tool and the move check | accepted (ADRs 241 to 244) |
| [82](0082-a-namespace-in-the-blueprint-scope-key.md) | A namespace in the scope key of a blueprint version | discussing |
| [84](0084-backstage-plugin-open-questions.md) | Open questions of the Backstage plugin | discussing |
| [85](0085-gate-rows-of-a-unit-that-a-name-outlives.md) | Gate rows of a unit that its name outlives: key by unit ID, refuse the name, or move the rows to a tombstone key | draft |
| [86](0086-change-tickets-in-external-change-systems.md) | Change tickets in external change systems, and what the core needs for them | discussing |
| [87](0087-components-of-the-server-process.md) | Components of the server process, and a start flag that selects them | accepted (ADR 249) |
| [88](0088-federation-model.md) | Federation model: installations, request forwarding, asynchronous replication, manual failover | accepted (ADR 248) |
| [90](0090-scanner-and-linter-reports.md) | Reports of IaC scanners and linters on stacks and workspaces, SARIF first | draft |
| [91](0091-plan-review-at-scale.md) | Plan review at scale, with one view of repeated changes across stacks and workspaces, and noise rules that expire | draft |
| [92](0092-opentelemetry-for-jobs.md) | OpenTelemetry for jobs, with job traces, fleet metrics and OTLP export to the operator's collector | draft |
| [93](0093-vetted-supply-chain-for-providers-modules-and-plugins.md) | A vetted supply chain for OpenTofu providers and modules and Pulumi resource plugins | draft |
| [96](0096-follow-ups-after-an-apply.md) | Follow-ups after an apply, which start an AWX job template or send a signed webhook with selected outputs | draft |
