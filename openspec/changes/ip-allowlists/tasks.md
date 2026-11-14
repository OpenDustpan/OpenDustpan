## 1. failing tests

- [ ] 1.1 Add failing e2e and service tests for denied console sign-in, denied CLI request, and pass for non opted-in scope. Check: TestDenyConsoleSignInOutsideList fails on main.
- [ ] 1.2 Add failing API test for invalid CIDR entry refusal with 400. Check: TestInvalidCidrRefused fails on main.
- [ ] 1.3 Add failing service test for address inside one list but outside another. Check: TestDenyWhenAnyListDenies fails on main.

## 2. implementation

- [ ] 2.1 Add installation flag and list settings with CIDR parsing and audit entries. Check: PUT settings with a CIDR list returns 200 and GET shows the effective value with audit entry.
- [ ] 2.2 Wire the checker into console sign-in before credential use and into access token authentication after. Check: Denied address gets 403 with error code ip-allowlist.denied through the real binary over TLS.
- [ ] 2.3 Write docs page with enable, opt-in, deny and allow examples. Check: Page ip-allowlists runs each command as written and warns about lockout of the own address.

## 3. definition of done

- [ ] 3.1 Check wiring, routes and authorization actions. Check: Route test lists each changed route with its authorization actions.
- [ ] 3.2 Run e2e step against the real CLI and the real directory. Check: e2e step with real Pulumi CLI and OpenTofu from denied and allowed addresses.
- [ ] 3.3 Do usability check of the full path. Check: Usability run from the docs page alone by the site administrator.
- [ ] 3.4 Run full check suite. Check: `make check` with zero findings.
