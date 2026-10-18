# 206. The profile provider port, rotators and conformance suites

Status: accepted · 2026-10-18 · part of ADR 200

## Context

The project owner decided on 18 October 2026 (owner):

- One provider port, with the kinds `login`, `secrets` and `outputs`, plus
  rotators.
- Each integration is an adapter with a conformance suite.
- The core is shared and tool-neutral (ADR 12, ADR 13).

A **profile provider** is an adapter that a profile calls when a caller
opens the profile: it gets a short-lived credential (`login`), reads
values from an external store (`secrets`), or reads the outputs of a unit
(`outputs`). A **rotator** makes a new credential on a schedule and
retires the old one. ADR 207 lists the built-in adapters, and ADR 208
gives the protocol of an adapter that a team writes and hosts.

CLAUDE.md asks for small ports in the package that consumes them, each
documented with the errors that it returns, and a conformance suite for
each port before any adapter. Domain packages never import `net/http`. An
adapter of a cloud needs HTTP, so the adapters live in packages of their
own, as `pkg/blob/s3` does for the blob port.

## Decision

1. **The layout.**

   ```
   pkg/profile/              domain: profiles, documents, resolve, open, claims, ports
     profiletest/            conformance: Store, Sessions, Issuer keys
     providertest/           conformance: Provider and Rotator
     adapter/<name>/         one package for each built-in adapter (ADR 207)
     external/               the external adapter protocol: client and verifier (ADR 208)
   ```

   depguard holds `pkg/profile`, `profiletest` and `providertest` to the
   rules of the domain packages. An adapter package can import `net/http`
   and `pkg/egress`, and no store, no tool package and no `internal/api`
   package. An adapter never imports another adapter.
2. **The kinds.**

   ```go
   // Kind is what a provider gives to an open.
   type Kind string

   const (
       // KindLogin gives a short-lived credential, for example a cloud
       // session from an OIDC token of the issuer of the namespace.
       KindLogin Kind = "login"
       // KindSecrets reads values from an external store.
       KindSecrets Kind = "secrets"
       // KindOutputs reads the outputs of a unit.
       KindOutputs Kind = "outputs"
   )
   ```

   The key `use` of a provider block names the kind and the adapter, for
   example `login/aws` or `secrets/vault` (ADR 201). The registry refuses
   two adapters with one name and one kind at start.
3. **The provider port.**

   ```go
   // Provider is one integration of one kind. The open of a profile calls
   // it once for each block that the open needs. An adapter keeps no
   // state between two calls, except a cache of keys or discovery
   // documents with a limit.
   type Provider interface {
       // Adapter is the name of the adapter, for example "aws".
       Adapter() string
       // Kind is the kind of the adapter.
       Kind() Kind
       // Spec describes the inputs, the outputs and the default exports.
       // The save check, the console and the docs read it.
       Spec() Spec
       // Check validates the inputs of one block when a person saves a
       // revision. It calls no network. ErrInvalid names the path of
       // each bad input.
       Check(in Value) error
       // Open resolves one block for one open. ErrInvalid for an input
       // that the remote system refuses as malformed. ErrDenied when the
       // remote system refuses the credential or the trust. ErrNotFound
       // for a value that the remote system does not have.
       // ErrUnavailable for a timeout, a refused connection or an answer
       // of 5xx. ErrTooLarge for an answer above MaxProviderResult.
       Open(ctx context.Context, r Request) (Result, error)
   }

   // Request is one call of Open.
   type Request struct {
       // Inputs are the resolved inputs of the block. A leaf from a
       // secret keeps its secret mark.
       Inputs Value
       // Claims are the facts of the open (ADR 205). An adapter reads
       // them and never changes them.
       Claims Claims
       // Tokens mints tokens of the issuer of the namespace for this
       // block (ADR 204). It is nil for an adapter whose spec mints no
       // token.
       Tokens TokenSource
       // Login is the result of the login block that this block names
       // with "login", or nil.
       Login *Result
   }

   // TokenSource mints one token for one audience, with the subject
   // template of the block. ErrInvalid for an audience that the issuer
   // refuses (ADR 204 decision 7).
   type TokenSource interface {
       Token(ctx context.Context, audience string) (Token, error)
   }

   // Result is the outputs of one block.
   type Result struct {
       // Value holds the outputs. Each leaf that is a secret has the
       // secret mark. A login result marks each credential.
       Value Value
       // Expires is when the shortest credential in Value ends, or zero.
       Expires time.Time
       // Identity names the identity that the adapter used, for example
       // a role ARN, for the audit entry. It is never a credential.
       Identity string
   }
   ```

   The deadline of the context is the timeout of the block: 10 seconds by
   default, at most 30 seconds. An adapter returns its error with a message
   of at most 1 KiB that holds no secret and no body of an answer. The
   open wraps it with the block name (ADR 203).
4. **The spec of an adapter.** `profile.Spec` describes the inputs and the
   outputs as a small schema: the type of each field (string, number,
   boolean, list, map, duration), required or optional, secret or plain,
   and a description. The save check compares each block with it, and so
   does the console form. The spec also names the **default exports** of
   the adapter: the variables, the files and the tool config that the
   setting `export: true` of a block adds (ADR 201 decision 9). For
   example, the default exports of `login/aws` write a shared credentials
   file and name it in `AWS_SHARED_CREDENTIALS_FILE`.
5. **The rotator port.**

   ```go
   // Rotator makes a new credential and retires an old one. A rotation
   // keeps two credentials alive for the grace period, so that a run
   // that read the old one can end.
   type Rotator interface {
       // Adapter is the name of the adapter, for example "postgres".
       Adapter() string
       // Spec describes the inputs and the state of the rotator.
       Spec() Spec
       // Check validates the inputs of one rotator block at save. It
       // calls no network. ErrInvalid.
       Check(in Value) error
       // Rotate makes a new credential. It gets the current state and the
       // credential of the login block that the rotator names, and
       // returns the new state. It never retires the current credential.
       // ErrInvalid, ErrDenied, ErrNotFound, ErrUnavailable as for Open.
       Rotate(ctx context.Context, r RotateRequest) (Rotation, error)
       // Retire revokes the credential of an earlier rotation. A
       // credential that is already gone gives nil. ErrDenied,
       // ErrUnavailable.
       Retire(ctx context.Context, r RetireRequest) error
   }
   ```

   ADR 202 decision 8 gives the schedule, the state and the record of a
   rotation.
6. **Each call goes through one outbound client.** An adapter gets its HTTP
   client from the wiring, never from `http.DefaultClient`:
   `egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly)` with the dial
   guard of ADR 145 decision 20. An adapter reads at most
   `MaxProviderResult` (1 MiB) of an answer.
   - A homelab or a company network often runs Vault, Keycloak or a
     database on a private address. The installation setting
     `profileProviderNetworks` lists the private networks that a provider
     can reach, as CIDR blocks. It is empty by default. Only an operator
     sets it (`server:manage-settings`). A link-local address, for example
     `169.254.169.254`, and a loopback address stay refused for every
     setting, so that a profile cannot read the metadata service of the
     host of the server.
   - The egress proxy of RFC 16, when it exists, applies to these calls
     too.
7. **Errors.** `pkg/profile` holds the sentinels of the port:

   | Error | When |
   | --- | --- |
   | `ErrInvalid` | A bad input, at save or at open. |
   | `ErrDenied` | The remote system refused the credential, the token or the trust. |
   | `ErrNotFound` | The remote system has no such value or object. |
   | `ErrUnavailable` | A timeout, a refused connection, a DNS failure, a refused address or an answer of 5xx. |
   | `ErrTooLarge` | An answer above `MaxProviderResult`. |

   An adapter translates each error of its client library into one of
   them, and never returns an error of the library to the open.
8. **A conformance suite for each port (owner).** `providertest` holds
   `RunProvider` and `RunRotator`. Each built-in adapter and the external
   client run them, with a fake of the remote system in the test:
   - `Spec` and `Check`: each required input, each type, a secret input
     that the block gives as a plain literal (refused, ADR 201 decision 6).
   - `Open`: a valid call, each error of decision 7 from the fake, a
     deadline that ends, an answer of `MaxProviderResult` plus one byte,
     and a call with no network for an input that `Check` refuses.
   - Secrets: each output that the spec marks as secret has the mark in
     the result. The error message of each failure holds no input marked as
     secret and no byte of the answer of the fake. `Identity` holds no
     credential.
   - Tokens: a login adapter asks the `TokenSource` for the audience of
     its inputs, or for its default audience, and sends the token only to
     the fake of its remote system.
   - Rotators: `Rotate` keeps the current credential, `Retire` of a gone
     credential gives nil, and a `Rotate` that fails leaves the state as it
     was.
   - Leaks: the suite runs each case with `goleak` (ADR 36).

   A new adapter merges with its suite run. A new method of a port gets
   conformance cases before any adapter implements it.
9. **Tests against the real systems are separate.** An e2e step against the
   real system of an adapter (a cloud, Vault, Keycloak) is not part of
   `make check`, because it needs accounts and network. Each adapter
   documents its e2e step, and the release notes say which adapters had one
   for the release.

## Consequences

- An adapter is a small package: a spec, a check and one call. The
  conformance suite tells its author what a correct adapter does.
- The server calls clouds and stores from its own network. An operator who
  does not want that uses the login mode `token` (ADR 207 decision 2): the
  server mints the OIDC token and the tool exchanges it.
- `pkg/egress` gets the setting of decision 6, and a test for each class of
  address.
- The registry of adapters is part of the wiring of `dustpan.NewServer`. An
  SDK user can add an adapter with an option of the server (ADR 35).
