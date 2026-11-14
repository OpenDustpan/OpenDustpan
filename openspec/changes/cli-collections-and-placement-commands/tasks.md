## 1. Failing tests

- [x] 1.1 Write table tests for the five commands in the NEW file `internal/cli/collection_test.go` and the NEW file `internal/cli/placement_test.go` against an `httptest` server: the request of each command, the token from `DUSTPAN_ADMIN_TOKEN` and `DUSTPAN_ADMIN_TOKEN_FILE`, a missing token, a bad `<namespace>/<name>`, an unknown collection name, and the server errors printed with their code. Check: the tests fail on main

## 2. Build

- [x] 2.1 Add the commands in the NEW files `internal/cli/collection.go` and `internal/cli/placement.go` and register them next to `commandNamespace` (`internal/cli/cli.go:39`). Check: `go test ./internal/cli/...` passes
- [x] 2.2 Name `opendustpan placement add` in the text of `placement.no-match` (`internal/errcode/catalog_placement.go:42`). Check: `go test ./internal/errcode/...` passes
- [x] 2.3 List the commands in `docs/reference/configuration.md` (regenerate the client flags table with the `-update` flag of its test) and use them in `docs/guides/quickstart.md` in place of curl. Check: make docs-lint passes. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Add the e2e step TestQuickstartCollectionCommands in the NEW file `e2e/cli_collections_test.go`: it runs the collection and placement commands of the quickstart against the e2e test server with the admin token (the routes /api/dustpan/v1/namespaces/{namespace}/collections and /api/dustpan/v1/namespaces/{namespace}/placement-rules), then a real `tofu apply` places its workspace in the collection. Check: the step passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 Run the local checks (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).

### Validation status

The e2e test is written and compiles. It was not run.
The current collection API omits stable IDs from its answers.
The placement API accepts collection names, not stable IDs.
The client refuses missing IDs instead of sending a name-based reference.
The quickstart cannot pass until those API contracts support stable IDs.
Server and API changes are outside this change's permitted scope.

The required local fast checks pass. The docs check has no hard findings.
The full `make check` and `make e2e` remain for CI.
