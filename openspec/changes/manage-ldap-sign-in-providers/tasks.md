## 1. Failing tests first

- [ ] 1.1 Write tests for validation, TLS refusal including loopback and missing setting, subject allowlist and uniqueness. Check: `go test ./pkg/session -run TestLDAPProviderConfig -count=1` fails on main
- [ ] 1.2 Write tests for group claims, unknown group, removal counting, protected groups, disabled user, referral, empty password, user-name bounds and limits. Check: `go test ./internal/signinsvc -run TestLDAP -count=1` fails on main
- [ ] 1.3 Write tests for the static binding, first start, lost record, rebind action, rebind audit entry and session revocation. Check: `go test ./internal/api/server -run TestStaticLDAPBinding -count=1` fails on main

## 2. Definition of done

- [ ] 2.1 Wire the routes with actions, audit entries and hostile tests per layer. Check: `go test ./... -run LDAPProvider -count=1`
- [ ] 2.2 Add an e2e step with a real LDAP directory and real CLI token use. Check: `make e2e` runs the LDAP steps
- [ ] 2.3 Write the guide page and run the usability check from the page alone, including the CLI sign-in step. Check: `make docs-lint` and `make check` report zero findings
