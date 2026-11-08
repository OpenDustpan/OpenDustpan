# QA round 14 summary

Range: e942f91b8..ce2265d3. Local only: `dev` server on SQLite. The Pulumi CLI and OpenTofu were not installed, so the tests used the REST and TFE-compatible APIs with curl.

## Findings
- #2921: the blueprint routes in `docs/guides/publish-a-blueprint.md` and `docs/reference/self-service-api.md` answer 404 in the binary. No non-test code calls `WithSelfServiceBlueprints`.

## Checked and correct
- Two placement rules (Pulumi and OpenTofu) made by `dev`; `--demo` rules in each namespace.
- New OpenTofu workspace goes in the collection; `placement.no-match` 422 in a namespace with no rule.
- `protected` field on groups; `--soft-delete-retention` and `--ip-allowlists` flags, defaults and error text.
- Error codes named in the guides exist in `internal/errcode`.
- Postgres keepalive values, 10000 unverified-key bound, `DELETE /api/server/instances/{id}`, Slack zero-width spaces, the purge read-error metric.

## Not tested
- Planner grant refusal (grant writes need a recent console sign-in; the dev token gets `mfa.fresh-step-required`).
- LDAP, passkeys, access review, restart during an operation, full disk, `unit.no-collection` end to end.
