## 0. Confirm against the code first

- [x] 0.1 Read the three facts of "Evidence" at their file and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Add a table-driven test of the command with a fake server: a list of two pools, an empty list, a 403, a 404 and an unknown JSON shape. Check: NEW TestRunnerPoolsList in internal/cli fails on main because the command does not exist.

## 2. Build

- [x] 2.1 Add the command in the NEW file internal/cli/runner_pools.go and one row in each of cmd/opendustpan/commands.go (the row `cli.RunnerPoolsListCommand(cli.RunnerPoolsList)` is NEW) and internal/cli/reference.go. Add one usage line `opendustpan runner pools list --namespace <namespace>` in the Operate group of the usage text in internal/cli/client/client.go, because TestUsageNamesEveryCommand and TestUsageGroupsByIntent in cmd/opendustpan/help_test.go require every command row in the usage. The new command also needs edits in internal/cli/settings.go, internal/cli/cli.go, internal/cli/collection_test.go and cmd/opendustpan/help_test.go (the settings and collection lists and the help test name each command). Regenerate docs/reference/configuration.md with `go test ./internal/cli/ -run TestConfigurationReference -update`, because the new reference row changes it. Check: `go test ./internal/cli/... ./cmd/opendustpan/...` passes TestRunnerPoolsList.

## 3. Definition of done

- [x] 3.1 Show the command in docs/guides/runner-pools.md, section "List the pools". The e2e test calls `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools` with an administrator access token. Check: `make docs-lint` passes and the docs test runs the command.
- [x] 3.2 Run the fast checks with `make test-changed`. Check: `make test-changed` passes. Confirmed by: CI (the agent runs the fast checks only).
