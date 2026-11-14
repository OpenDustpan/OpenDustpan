## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add cases to testGroupProtected in pkg/access/accesstest/reach.go: each of namespace:manage-access, namespace:manage-principals and namespace:manage-principal-tokens on the installation gives protected true. A read-only data role on the installation and the same actions on one namespace give false. A grant that ends returns the flag to false. Run both dialects. Check: testGroupProtected fails on main for the three actions.
- [ ] 1.2 Add cases to TestProtectedReadsTheObjectOfEachAction in pkg/access/protected_internal_test.go for the object of each action. Check: TestProtectedReadsTheObjectOfEachAction fails on main for the new cases.
- [ ] 1.3 Add a test in pkg/access/access_test.go that the guide table equals SensitiveActions. Check: The test fails on main while the guide has its own list.
- [ ] 1.4 Add a test in pkg/access/access_test.go that a group with 30 causes returns 20 causes and a hidden count of 10 for a caller who reads 20 cause objects. Check: The new test fails on main.
- [ ] 1.5 Add a hostile test in the NEW file internal/api/server/principals_causes_test.go: a caller who can read the group but not the object of a cause gets the count and no grant ID, action or object of that cause. Check: The hostile test fails on main.

## 2. Implementation

- [ ] 2.1 Make Reach.protected in pkg/access/reach.go call the helper built on IsSensitive in pkg/access/access.go and remove the family copy at pkg/access/reach.go:183. Check: The tests of the first section pass.
- [ ] 2.2 Return the readable causes and the hidden count in the group read in internal/api/server/principals.go and in the API description. Check: A group read returns the grant ID, the action and the object for the test group.
- [ ] 2.3 Edit docs/guides/principals-and-groups.md: build the table from SensitiveActions and mark the identity-provider limit as not yet enforced. Check: The guide test passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Route check: `GET /api/server/groups` keeps its authorization action. An e2e step calls it with an access token of a site administrator and checks the causes. The wiring is the existing route. Confirmed by: CI (make e2e). Check: The route test and the e2e step pass (make e2e).
- [ ] 4.2 Usability check: the administrator finds the cause and the unenforced limit from the guide alone. A person's step. Check: A reviewer runs the guide steps against a dev server.
- [ ] 4.3 Run make check with zero findings. Confirmed by: CI (make check). Check: make check
