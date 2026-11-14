## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Gate record

- [ ] 1.1 Create NEW `docs/clients/dotnet-java-start-gate.md` to record the owner stable-API decision and the rule that client-dotnet and client-java start only after it. Check: `test -f NEW docs/clients/dotnet-java-start-gate.md` shows the gate file exists

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Run `make check` for NEW `docs/clients/dotnet-java-start-gate.md` with zero findings. Check: `make check` passes with zero findings
