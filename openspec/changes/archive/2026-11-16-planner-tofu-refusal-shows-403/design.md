## Context

Short-form bug. The change adds no port, type, table or route. It fixes what
the e2e test checks in the CLI output, and, only if that test fails on main,
the scope of one discovery route that ADR 374 decision 1 names.

## Contract

- Endpoint `GET /api/v2/organizations/{organization}/entitlement-set`: for a
  principal with a grant on a collection or a unit in the namespace, it
  answers 200 with the entitlement set, as on main. Not changed.
- Endpoint `POST /api/v2/workspaces/{id}/actions/lock`: for a planner, it
  answers 403 with the JSON:API error code `access.denied`, as on main. Not
  changed.
- CLI output of `tofu apply` and `tofu destroy` as planner: the exit code is
  not 0, the output holds the server refusal, and it does not hold
  `organization "<namespace>"` together with `not found`.
