# M6-55: the job proxy core in the launcher module (pkg/launcher/proxy)

- Class: SECURITY (the only network exit of a job; credential injection)
- Routing: Sonnet author, Opus review
- Branch: `m6-55-job-proxy-core`
- Conflict group: `jobproxy-core` (RFC 16 work table, item X4)
- Persona path: none of P4 to P6 directly; milestone 6 exit test step 7.
- Depends on: none. X1 (`pkg/jobproxy`: model, `Parse`, `Render`,
  `CanonicalPath`, `Match`, reasons) is on main.

## Facts checked at main 9e96ae30c8c3

- `pkg/jobproxy/match.go`: `Request{Operation, Host, Port, Method, Path,
  PlainHTTP}`, `Decision{Allowed, RuleID, Reason}`, `Match(r Rendered, req)`;
  reasons `no-rule-host`, `no-rule-matched`, `path-not-canonical`,
  `credential-over-http`. The caller removes the query before `Match`.
- `pkg/jobproxy/render.go`: `Rendered` (opaque; zero value allows nothing),
  `Rendered.Rules()` returns `RenderedRule` with `Mode`, `Credential`,
  `ClientCertificate`, `ServerTrust`; `Rendered.Digest()`.
- `pkg/jobproxy/path.go:66`: `CanonicalPath`. `pkg/jobproxy/jobproxy.go`:
  `ErrInvalid`, `ErrRender`, `ErrCredential`, `Mode`, `Operation`.
- `pkg/egress/egress.go`: `Rules{Allow, LoopbackForTests}`,
  `(Rules).CheckAddr`, `ParseAllowedRanges`. `internal/egress/egresshttp` is
  internal: the launcher module cannot import it.
- `.golangci.yml:129-149` rule `launcher`: no `internal/`, no forge, no SQL
  driver. Rule `jobproxy` (150-): same for `pkg/jobproxy`.
  `pkg/launcher/deps_test.go` checks the linked packages of `pkg/launcher`.
- `pkg/launcher/proxy` does not exist. `pkg/launcher` has `session/`, `run/`,
  `helper/`.
- ADR 330 decisions 1 (our own Go code, `net/http` and `crypto/tls`, no new
  dependency), 6 (the CA of a claim), 7 (each request), 8 (outbound guard),
  10 (records). ADR 330 Consequences: where ADR 327 differs, ADR 330 holds.
- RFC 16 work table X4: CONNECT, the CA and the name binding, modes
  `inspect`, `tunnel`, `endpoint`, injection, HTTP/2 through the TLS end, the
  outbound guard dial, the upstream proxy, the response scan.

## Scope

X4 without the upstream proxy (that is M6-55b) and without the session, the
binary mode and the coordinator (later X items). The core takes its inputs
through small interfaces so that those items wire it.

## Files it may touch

- `pkg/launcher/proxy/` (new): `proxy.go` (the `http.Handler` of CONNECT and
  plain requests), `ca.go`, `dial.go`, `inject.go`, `scan.go`, `record.go`,
  tests, fuzz tests, `leak_test.go`, `deps_test.go`
- `.golangci.yml` only to add `pkg/launcher/proxy` to an existing rule if the
  `launcher` glob does not cover it (it should: `**/pkg/launcher/**`)

## Files it must not touch

- `pkg/jobproxy/*` (X1 is done; report a gap instead of changing it)
- `pkg/launcher/session`, `pkg/launcher/run`, `cmd/`, the runner repository
- `pkg/egress/*`

## Change

1. `proxy.New(Config)`: `Rendered jobproxy.Rendered`, `Operation`,
   `Egress egress.Rules`, `Credentials` (interface: `Credential(name)` returns
   a header, basic, query or client certificate; the core never logs it),
   `Records` (interface: `Write(Record) error`; buffer of 10,000; when full,
   refuse new requests), `Resolver` (interface over `net.Resolver` for tests),
   time limits.
2. CA: an ECDSA P-256 key and CA certificate in memory, name constraints to
   the hosts of the `inspect` rules, lifetime given by the caller. Expose the
   certificate only; the key never leaves the struct.
3. CONNECT: refuse a host that no rule names with 403 before any TLS.
   `tunnel`: resolve, check each address with `egress.Rules.CheckAddr`, dial
   the checked address (dial by IP, and check again in `net.Dialer.Control`).
   `inspect`: end TLS with a leaf of the CA; refuse an SNI or a `Host` that is
   not the CONNECT host; split path and query; refuse a non-canonical path
   with 400; `jobproxy.Match`; on deny, 403 with the body "blocked by the job
   proxy: <method> <host><path>: no rule matched". `endpoint`: loopback port only.
4. Injection: delete `Authorization`, `Proxy-Authorization`, `Cookie` and each
   header the credential sets, then add the credential. Never follow a
   redirect. Credential over plain HTTP only where a rule opts in, and only to
   loopback.
5. Response scan for a rule with a credential: the plain value, base64
   standard and URL with and without padding, the JSON escape form and the
   percent form, across chunk borders (window = longest form). On a match,
   break the stream.
6. Records: one per request with the fields of ADR 330 decision 10. A record
   never holds a header, a body, a query value, a credential or a library
   error text; errors map to fixed reasons. No request passes without a record.
7. HTTP/2 upstream where the server offers it, with the standard library only.

## Acceptance

- One hostile test for each layer, each alone:
  - boundary: CONNECT to an IP literal, to `host:0`, to a host with a
    trailing dot or upper case (byte compare: refused), a 70 KiB request line.
  - name binding: SNI `other.example` on a CONNECT to `api.example`; `Host`
    header that differs; HTTP/2 `:authority` that differs.
  - path: `..`, `%2e%2e`, `%2F`, `%5C`, `//`, a path with `;` (refused 400, no
    normalization).
  - outbound guard: a rule host that resolves to 127.0.0.1, 169.254.169.254,
    10.0.0.1 with no allowed range, and a DNS answer that changes between
    check and dial (the dial uses the checked IP; the Control check refuses).
  - injection: a client `Authorization` and `Cookie` are removed; the
    credential is not in any record or log line; a 302 is passed back, not followed.
  - scan: the credential in each of its encoded forms, split across two
    chunks, breaks the stream.
  - records: with the record sink failing and the buffer full, the next
    request is refused.
  - fail closed: the zero `Rendered` allows nothing; a resolver error is a
    fixed reason, not the library text.
- Fuzz tests: the CONNECT target parser and the scan window.
- `deps_test.go`: `pkg/launcher/proxy` links no `internal/`, no SQL driver, no
  forge, and of this module only `pkg/jobproxy`, `pkg/egress` and itself.
- No user docs page: no user-visible change until the session and the
  coordinator items wire it. `docs/reference/proxy-rules.md` stays as it is.
- `make check` zero findings, `go test -race ./pkg/launcher/...`.
