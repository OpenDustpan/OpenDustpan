# Worker specs, 2026-11-07

Planner scratch. Not user documentation. Do not merge this branch.

Facts checked at main `9e96ae30c8c3ca08fa80feb8f016a8ff99a9a854`.

The item IDs (M4-31a and so on) are not in `docs/roadmap.md` or in an issue.
Each spec states the scope that the planner read from the roadmap, the persona
path tests of #2610, the in-flight PRs and the ADRs. If the plan of the lead
gives an ID another scope, correct the ID, not the spec.

| Order | ID | Class | Conflict group | Depends on | Ready |
| --- | --- | --- | --- | --- | --- |
| 1 | M4-31a | SECURITY | principal-groups | none | yes |
| 2 | M4-42a | SIZED | access-review | none (M4-31a for the rule link, later) | yes |
| 3 | M5-13a | SECURITY | job-approve | none | yes |
| 4 | M5-8b | CORE | job-start | #2619 (M5-2), #2611 (M5-8a) | no: wait for both merges |
| 5 | M6-8 | SECURITY | selfservice-render | none | yes |
| 6 | M6-9a | SECURITY | selfservice-svc | none | yes |
| 7 | M6-55 | SECURITY | jobproxy-core | none (pkg/jobproxy is on main) | yes |
| 8 | M5-60b | MECHANICAL (docs) | docs-runners | #2611, M5-8b, M5-13a | no: dependencies not in |

Model routing: MECHANICAL Haiku. SIZED Sonnet. CORE and SECURITY: Sonnet
author, Opus review.

Shared generated files: M4-42a, M5-13a, M5-8b and M6-9a change the API
description (`openapi.json`, `clients/typescript`). Resolve a conflict there
with a new run of the generator on the merged code (`make generate`,
`make clients-generate`), never by hand.

Shared wiring: M4-31a (`pkg/dustpan/area_signin.go`), M5-8b
(`pkg/dustpan/jobservice.go`) and M6-9a (a new `pkg/dustpan` file) each touch
`pkg/dustpan`, in different files. If one needs the route list of the server,
add one line and keep the lines of the other.

Every spec: `make check` passes with zero findings, each new test package has
`leak_test.go`, the PR pastes the output of its checks (ADR 76), and the commit
is signed off (`git commit -s`).
