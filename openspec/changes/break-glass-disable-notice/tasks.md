## 0. Confirm against the code

- [ ] 0.1 Confirm `internal/removalalertsvc/removalalertsvc.go` has the `Config` ports Outbox, Audit, Logger and Now, and the `alert` function.
- [ ] 0.2 Confirm `pkg/notify/notify.go` has `MembershipMassRemoval` and `Types()`.
- [ ] 0.3 Confirm `internal/api/server/principals.go` `disablePrincipal` calls `svc.DisablePrincipal`.
- [ ] 0.4 Confirm the change keep-two-working-break-glass is on main.

## 1. Failing tests first

- [ ] 1.1 `pkg/notify/notify_test.go`: `Types()` holds `break-glass.disabled`.
- [ ] 1.2 NEW `internal/removalalertsvc/breakglass_disable_test.go`: table of hostile inputs (empty actor, account that is not break-glass, invalid attribute), the notice content, the audit entry, a failing outbox.
- [ ] 1.3 NEW `internal/api/server/break_glass_disable_notice_test.go`: a disable sends one notice to the administrators, a refused disable sends none, a failed notice keeps the disable.

## 2. Build

- [ ] 2.1 Add `BreakGlassDisabled` to `pkg/notify/notify.go` and to `Types()`.
- [ ] 2.2 Add `ActionBreakGlassDisableNotice` to `pkg/principal`.
- [ ] 2.3 NEW `internal/removalalertsvc/breakglass_disable.go`.
- [ ] 2.4 Wire it in `pkg/dustpan/localusers.go` and call it in `internal/api/server/principals.go`.

## 3. Side effects

- [ ] 3.1 `docs/guides/break-glass.md`: name the notice, its recipients and where to read it.
- [ ] 3.2 The API description and the clients need no change: no route changes. Run `make generate` and confirm a clean diff.

## 4. Definition of done

- [ ] 4.1 `make check` passes with zero findings.
- [ ] 4.2 `go test -race ./internal/removalalertsvc/... ./internal/api/server/... ./pkg/notify/...` passes.
- [ ] 4.3 The pull request body holds the output of the checks.
