## 1. Failing tests first

- [ ] 1.1 Write failing tests for trust, its refusals, concurrent changes, and full sync on and off. Check: `go test ./pkg/principal/... -count=1` fails on main.

## 2. Definition of done

- [ ] 2.1 Wire routes with actions, edit the baselines and the backup table list, add the e2e step and the docs page. Check: `make e2e` step and `sqlc diff` clean.
- [ ] 2.2 Run the usability check and the full gate. Check: `make check` with zero findings.
