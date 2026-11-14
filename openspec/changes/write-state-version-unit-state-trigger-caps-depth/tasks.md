## 1. Failing tests

- [ ] 1.1 Add failing conformance cases for per-unit and per-source caps on trigger.Store.Put. Check: trigger storetest: save 21st trigger of one unit fails with cap 20; save 51st follower of one source fails with cap 50; one save at both caps names both numbers
- [ ] 1.2 Add failing conformance cases for PUT count rules and lowered caps. Check: trigger storetest: PUT that keeps unit and source succeeds at cap; PUT that moves source recounts followers; lowered cap keeps rows and refuses only a new add
- [ ] 1.3 Add failing conformance cases for save-time cycle refusal with serialized namespace writes. Check: trigger storetest: closing save fails and names the loop path in order; concurrent closing saves of one namespace serialize so one is refused
- [ ] 1.4 Add failing tests for the depth counter in the job origin and the refusal above depth 10. Check: dispatcher test: chain depth 11 refuses the start and writes one audit entry and one log line that names the loop; missing origin starts at zero; manual fire resets to zero; audit failure keeps the refusal

## 2. Definition of done

- [ ] 2.1 Wire caps, cycle check, and depth guard through the trigger routes and the job dispatcher. Check: PUT trigger path returns 422 with cap number and loop path; settings GET shows both caps as effective values
- [ ] 2.2 Add e2e steps against the real server for caps, cycle refusal, and depth stop. Check: e2e with real CLI: 21st trigger refused, 51st follower refused, loop save refused, chain stops at depth 10 with audit entry
- [ ] 2.3 Write the docs page for trigger caps, cycle errors, and the depth guard. Check: docs page for trigger limits and depth guard passes `make docs-lint`
- [ ] 2.4 Run the full definition of done: wiring, routes with actions, e2e, docs, usability check from the docs page alone. Check: `make check` reports zero findings
