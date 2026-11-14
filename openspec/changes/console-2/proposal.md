---
version: 2
---

Roadmap: `docs/roadmap.md:323` (Console).

## Why

**Persona: developer.** A developer runs plans and applies on runners and needs to follow one run, read its masked diff and approve it without use of raw API output.

The user path this change must make work:

1. **Console.** Open the unit and select the job from its history to reach the run page.
2. **Console.** Follow the timeline and live log while the job runs.
3. **Console.** Read the masked plan diff for the plan phase.
4. **Console.** Approve the waiting apply where policy needs it.
5. **Docs page.** Read the run page docs that name the API routes behind each panel.
6. **E2e.** Run the browser tests of the console repository against the server of the suite.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Job unknown: no job with this ID in this namespace. | The run page shows job not found and links back to the unit list. | `GET /api/dustpan/v1/namespaces/{}/jobs/{}` |
| Approval refused: the principal lacks the approve action on the unit. | The run page shows approval refused and keeps the approval wait state. | `GET /api/dustpan/v1/namespaces/{}/jobs/{}` |
| Log page unavailable: the log poll failed, retry from the last cursor. | The live log panel shows log unavailable and offers retry from the last cursor. | `GET /api/dustpan/v1/namespaces/{}/jobs/{}/logs/{}` |

Evidence for the claims above:

- The context pack names no ADR for this change. Proof: Pack field adrs is an empty list.
- The roadmap row cites RFC 52 console core views as draft. Proof: Roadmap row: RFC 52 console core views, run page with timeline and diff, draft.
- The roadmap step requires the run page with timeline, live log, masked plan diff and approval. Proof: Roadmap step: The run page shows the timeline, the live log, the masked plan diff and the approval of step 6.

## What Changes

- Run page shows timeline, live log, masked plan diff and approval for one job.
- Browser tests of the console repository run against the suite server.
- Docs page describes the run page path and its API routes.

Out of scope:

- Job start, cancel, approval rules, policy evaluation and server masking rules.
- Runner management and pool views.
- Pull request automation views.
- Console views other than the run page.

Depends on:

- add-job-service-routes-start-1
- policy-approvals
- runners
- `policy-approvals` (order of the milestone review)
- `runners` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A large plan diff still slows first render; the page paginates resources.
- Live log polling lags a few seconds behind the runner; the page states the refresh time.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Server has a console control test that covers console control paths. (`pkg/dustpan/console_control_test.go:14 (TestConsoleControlPaths)`)
- Stack package has a masked-paths test that covers secret masking in state reads. (`pkg/stack/reveal_test.go:27 (TestMaskedPathsHoldsSecret)`)
- Access package has a planner-role test that covers viewer actions of the planner role. (`pkg/access/planner_role_test.go:95 (TestPlannerHoldsEveryViewerAction)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change touch any accepted ADR decision? No. The context pack lists adrs as empty, so no ADR decision applies.
- Does the live log use polling or server-sent events? Polling every 5 seconds with a manual refresh control and a last-polled time. No event channel ships in this slice.
- Does this change cover other console views? No. The roadmap step names only the run page.
- Does this change define job behavior or policy decisions? No. The job service and policy items own them. This change only reads and renders their records.
- Is RFC 52 accepted as the design basis? RFC 52 stays draft. This change lands the run page slice now against the suite server and aligns with RFC 52 later, because the milestone exit test needs the slice.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
