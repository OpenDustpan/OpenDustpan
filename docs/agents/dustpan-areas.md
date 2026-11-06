# Area files of pkg/dustpan

This is the recipe for the move of an area out of `pkg/dustpan/dustpan.go`
into its own file `pkg/dustpan/area_<name>.go` (ADR 326 decision 5). Follow
each step in order. Do one area in each pull request.

## Rules

1. The assembly is plain sequential calls with explicit arguments.
   `NewServer`, `assemble`, `newAdapters`, `mountAPI` and the group helpers of
   the assembly stay in `dustpan.go`.
2. An area constructor does not take `*settings`. It takes the values that
   it reads, as arguments. When it reads more than about five values, it
   takes one struct of its own, `<name>Inputs`, with one field for each
   value. The assembly fills each field by name.
3. An area constructor returns its outputs. It does not write into
   `settings`, into `services` or into another shared struct. The assembly
   gives each output to the next call as an argument.
4. A helper that calls the constructors of two or more areas is assembly.
   It stays in `dustpan.go`. Examples: `newInstallationAPI` (backup area and
   installation area), `newProfileAPIs` (profile, issuer and sinks).
5. Do not put a list of areas, an area interface or an `init()` in place of
   a call.
6. Do not change the order of the calls. When a value is not yet built at the
   place of a call, give the zero value explicitly and write a comment that
   says why. Example: `newBackupService` gets `watch: nil`, because
   `prepareBackground` makes the worker watch later.
7. Security code keeps every check, every defense-in-depth comment and every
   fail-closed path. Do not change `mountAPI` or the order of a check.

## The authenticator chain

`newAuthPorts` in `dustpan.go` builds the authenticator chain and the
authorizer chain in one function, one statement for each step. From the
inside out:

1. `gatedAuthenticator`: the configured authenticator, then the stored access
   tokens behind their failure gate. It also returns the sign-in gates.
2. `tempAdminDecorator`: the marks of the temporary administrators.
3. `principal.RefuseDevSubjects`: not on a dev installation.
4. `principal.RefuseNamespaceServiceSubjects`.

The authorizer chain: the configured authorizer, `identity.SelfAllowed`,
`identity.NamespaceServiceRule`, then the IP allowlist gate when the feature
is on.

`TestNewServerAuthenticatorChain` checks this order through a real request.
When you change the chain, change the test in the same commit.

Every service authorizes through the one chain. `NewServer` builds the auth
ports with `newAuthPorts` and writes the chain into `cfg.authenticator` and
`cfg.authorizer` before `newServices`. So a constructor that runs after that
write cannot read the bare configured port. Only `newIdentityServices` runs
before the write and can see it. A service that checks an action takes the
wrapped authorizer as an argument. The manual lock service is one example:
its lock, unlock and manage checks pass the IP allowlist gate and the
namespace service rule.

What guarantees this is the order in `NewServer`. The test proves less:
`TestNewServerAuthenticatorChain` checks each `Authorize` call that it
records, and it records only the calls that it makes. A call that does not
come through the whole chain fails the test, unless the function that makes
the call is named in `bareAuthorizerCallers`
(`pkg/dustpan/authchain_internal_test.go`). A service that holds the bare port
and that the test does not call is not found by the test. When you add a
service that checks an action, add a call of it to the test.

The named exceptions are the two callers that run before the write. This
layer has no IP allowlist gate and no namespace service rule. The routes in
front of them use the chain, but these layers do not rely on that. The test
does not exercise these entries, because with the recording authorizer they
build their own `*access.Authorizer`.

- `access.(*Service)`: the escalation check of a grant needs the grant model
  of the concrete `*access.Authorizer` (the stored roles and grants). It is
  not a check of a request.
- `accesstoken.(*Service)`: it gets that authorizer of the access service for
  the escalation check of a token for another principal.

To add an exception, add the name and the reason to that list in the same
commit.

`NewServer` writes the two chains into `cfg.authenticator` and
`cfg.authorizer` one time, for the adapter code that is not in an area file
yet. An area file must not read them from `settings`. Its caller passes them
as arguments. When no reader of `cfg.authenticator` and `cfg.authorizer` is
left after `NewServer`, remove that write.

## Steps

1. **Pick the area.** Map the changed lines of the newest 200 first-parent
   commits on `main` to top-level declarations
   (`git diff -U0 c^1 c -- pkg/dustpan/dustpan.go`, then a `go/ast` lookup of
   each line). Choose an area that many commits change and that has few
   readers in other areas. Do not choose the assembly or `mountAPI`. Put the
   table of commits for each declaration in the pull request body.

2. **Commit 1: explicit arguments, in place.** In `dustpan.go`, change each
   constructor of the area to rules 2 and 3. Change its callers to pass the
   values by name. Change the tests that call it. Do not move code in this
   commit. If the area is security code, or if it builds a value that another
   area reads later, add a test at the level of `NewServer` that fails when
   the order changes. Run it on `main` first: it must pass there.

3. **Ownership.** Make a list of each declaration of `dustpan.go` whose only
   non-test, non-comment callers are in the area. For each one, write in the
   pull request body "moved" or "stayed, because ...". A constructor of a
   domain service (`new<Domain>Service`) needs this decision too, because it
   can belong to its consumer or to its own later area. Apply these rules:
   - A helper moves with the area that owns it, also when other files call
     it.
   - A settings field moves only when this area alone sets it, and only when
     an option sets it. A build output is never a settings field (rule 3).
   - Shared fields such as `namespaceStore` and `blobStore` stay in
     `settings`.
   - A group helper of the assembly stays (rule 4).
   - Existing area files (`githost.go`, `sinks.go`, `trustrules.go` and the
     like) keep their names.

4. **Commit 2: the move.** Move to `area_<name>.go`: the options
   (`With...`) of the area, its sentinel errors, its settings fields as one
   `<name>Settings` struct in one named field of `settings` (not embedded),
   a `default<Name>Settings()` function that names every field, its
   `<name>Inputs` structs, its start checks and its constructors with their
   helper types. Move each declaration with its doc comment as it is.

5. **Order of the file:** the error `var` block, the settings struct, the
   defaults, the options, the input structs, the constructors, the helper
   types.

6. **Mechanics:** cut the exact line ranges with a script, so that the bytes
   stay the same. Rewrite only the field paths. Run `gofmt -w` and remove the
   imports that are not used now. If a `var (...)` group in `dustpan.go` keeps
   only one spec, gofumpt wants a single `var`.

7. **Prove that the move changes nothing.** Build `scripts/declhash`
   (`go build -o /var/tmp/declhash scripts/declhash/main.go`). Compare
   `dustpan.go` of commit 1 with `dustpan.go` and `area_<name>.go` of
   commit 2. Run it once with no rewrites and once with `-r old=new` for each
   field path rewrite. When the move renames no field path, the two runs are
   one run. With the rewrites, each moved declaration must have the same
   hash. Only `settings`, `defaultSettings` and the new settings struct and
   defaults function can differ. `NewServer`, `assemble` and `mountAPI` must
   have the same hash. Paste the diff of the two lists in the pull request
   body. Paste `git diff` of each changed file that the hash set does not
   include.

8. **Gates:** paste the output in the pull request body.
   - `golangci-lint run ./...` and `golangci-lint run --build-tags e2e ./...`
   - `make nilaway` and `make semgrep`
   - `go test -race -timeout 60m ./pkg/dustpan/... ./cmd/...` with
     `DUSTPAN_TEST_POSTGRES_URL` set to a local PostgreSQL 18 (TCP on
     127.0.0.1, UTF8, data under `/var/tmp`)
   - `E2E_DATABASES=sqlite go test -tags e2e -race -count=1 -run '^TestCLIs$' ./e2e/`

9. Land each area just after a train, with an architecture review before the
   merge.
