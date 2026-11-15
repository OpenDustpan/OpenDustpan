## Context

Short-form security bug. The check of the approval of a change of the audit
chain to off becomes an allowlist of one kind, and each refusal is audited.

## Contract

- NEW method `auditChain.requirePersonApprover(ctx context.Context, subject string) error` in `pkg/dustpan/auditchain.go`, in place of `refuseServiceCaller`. It returns nil only for a stored principal of kind person. It returns `audit.ErrChainNeedsPerson` for every other kind, for a subject with no principal row and for a nil principal reader, and a read error joined with `audit.ErrChainNeedsPerson`.
- `auditChain.Approve`: on that refusal it writes one entry in the `@server` scope (action `server:set-audit-chain`, outcome denied, the reason), then returns the refusal.
- Endpoint `POST /api/server/audit/chain`: answers 403 with the text of `audit.ErrChainNeedsPerson` for a refused caller, as on main. Not changed.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | The route needs a console session with a fresh authentication. | the rows of `internal/api/server/audit_chain_test.go` |
| service | `Approve` runs the check for a change to off and audits the refusal. | `TestApproveOffRefusesEachOtherCaseAndAudits` |
| check | Person only; no row, read error and nil reader refuse. | `TestRequirePersonApproverRefusesEachOtherCase` |
