## 1. Failing tests first

- [ ] 1.1 Add failing authenticator tests for expired, revoked, and idle-disabled tokens Check: accesstoken package test TestAuthenticateRefusesExpiredToken fails on main
- [ ] 1.2 Add failing API tests for 401 on Pulumi, TFE, and tool-neutral routes with expired, revoked, and disabled tokens Check: e2e test TestTokenExpiryDeniesPulumiAndTofu fails on main

## 2. Implementation

- [ ] 2.1 Implement authenticator checks for expiry, revocation, disable, maximum lifetime, and last-used update Check: `go test ./pkg/accesstoken/...` passes
- [ ] 2.2 Wire authenticator into Pulumi, TFE, and tool-neutral handlers with 401 mapping, fresh-step checks, and audit entries Check: `go test ./internal/api/...` passes with 401 mapping and audit entries
- [ ] 2.3 Implement 90-day inactivity disable job with break-glass exemption, settings, re-enable path, and 10-minute rotation overlap Check: `go test -run TestInactivityDisable ./pkg/accesstoken/...` passes

## 3. Definition of done

- [ ] 3.1 Check wiring, route with authorization actions, and audit entries Check: Route test lists token routes with authorization actions including principal:manage-tokens
- [ ] 3.2 Run e2e step against real CLIs over TLS Check: e2e step runs pulumi up and tofu apply with expired, revoked, and idle-disabled tokens and checks 401 plus audit entries on SQLite and PostgreSQL
- [ ] 3.3 Write docs page in the same pull request Check: Docs page for token life cycle documents expiry, lifetime, inactivity disable, rotation overlap, and re-enable
- [ ] 3.4 Run usability check for the persona path Check: Developer completes token create with fresh step, rotate, and CLI use from the docs page alone
- [ ] 3.5 Run full check with zero findings Check: `make check` reports zero findings
