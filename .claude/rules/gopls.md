---
paths:
  - cmd/**/*.go
  - e2e/**/*.go
  - internal/**/*.go
  - load/**/*.go
  - pkg/**/*.go
  - soak/**/*.go
---

# The gopls MCP server

The gopls tools are optional. Use them when they are connected. If they are not connected, or a call times out, do not
wait or retry: use `go build`, `go vet` and `rg`.

- To find a symbol, use `go_search`.
- To see what a package gives to other packages, use `go_package_api`.
- To see what a file uses from the other files of its package, use `go_file_context`. Use it only when you need that
  information. Do not use it after each read.
- Before you change the definition of a symbol, use `go_symbol_references` to find each reference. Read the references
  that the change can affect.
- After you edit Go files, use `go_diagnostics` with the paths of the edited files. Fix each error. Ignore a hint or an
  info diagnostic that is not related to the task.
- Use `go_vulncheck` only after a change to `go.mod`. Do not use it at the start of a session. CI runs `make vuln`.
- After `go_diagnostics` reports no errors, run `go test` on the changed packages. Do not run `go test ./...` unless
  the user asks for it.
