## Problem

**Persona: an operator who turns on the IP allowlist of a namespace.** The
operator sets the list of the namespace `locked`. A client outside the list
reads the triggers, the context, or the unit settings of a unit of that
namespace. The answer is 404, as if the object did not exist, although the
guide and the glossary say that a request that the list refuses gets 403 with
the code `access.ip-not-allowed`. The caller cannot tell a missing object from
a refusal, and the console shows "not found" where the operator expects "the
address is not allowed". Three packages copy the read check and map every
refusal to their own 404, and they do not test `ipallow.ErrAddressNotAllowed`
first. Every other package uses the shared helper and answers 403.

## Evidence

Each line is from the current checkout. Open each file at the line.

- `pkg/ipallow/gate.go:154-169`: `ErrAddressNotAllowed`, and `addressError.Is(target)`, which reports true for both `identity.ErrDenied` and `ErrAddressNotAllowed`. `checkAddr` (line 174) returns the error with the text of an `identity.ErrDenied` wrap.
- `internal/api/route/authorize.go:43-46`: "A refused read on an address that the IP allowlist of the namespace refuses answers that refusal, which is 403, as on the other routes of the namespace." The shared `readRefusal` (line 92) tests `ipallow.ErrAddressNotAllowed` first and keeps the refusal; it maps `identity.ErrDenied` to the 404 of the caller.
- The three copies, each with the doc comment "readRefusal maps the result of a read check: a refusal is the 404 of an unknown object" and each with `case errors.Is(err, identity.ErrDenied): return errNotFound`:
  - `internal/api/triggers/routes.go:252-263`, used at lines 233 and 238 by `authorizeRoute` (line 227). `errNotFound` is `internal/api/triggers/triggers.go:182`; `classify` (line 224) answers 404 `TriggerNotFound` (line 237).
  - `internal/api/objectcontext/routes.go:179-190`, used at lines 155 and 165 by `authorizeRoute` (line 149). `errNotFound` is `internal/api/objectcontext/objectcontext.go:154`; `contextErrors` (line 178) answers 404 `ContextObjectNotFound` (line 183).
  - `internal/api/settings/routes.go:262-273`, used at lines 243 and 248 by `authorizeRoute` (line 237), and at `internal/api/settings/serve.go:499`. `errNotFound` is `internal/api/settings/settings.go:194`; `classify` (line 227) answers 404 `UnitSettingsNotFound` (line 231).
- The common answer: `internal/errcode/errcode.go:81-82` maps the allowlist error to `AccessIPNotAllowed`, and `internal/api/problem/problem.go:306-366` answers it as 403 with the code `access.ip-not-allowed` and the message `forbidden`.
- Packages that use the shared helper and answer 403 today: `internal/api/jobs/routes.go:314-322`, `internal/api/drift/routes.go:172-180`, `internal/api/runnerapi/enforce.go:115-117`.
- `docs/guides/ip-allowlists.md:104-105`: "A read that is refused by the list gets `403`, not the `404` that a denied object normally gets." `GLOSSARY.md:191` says the same. The three copies contradict both.
- `e2e/ipallow_test.go:181-197` reads `GET /api/server/namespaces/locked/override-switches` from outside the list and expects 403 with `access.ip-not-allowed`. The three surfaces have no such step, and no allowlist test exists in the three packages.

The report holds on the checkout: the three surfaces answer 404 for a refusal,
and the guide promises 403.

## Fix boundary

IN:

- `internal/api/triggers/routes.go`, `internal/api/objectcontext/routes.go`,
  `internal/api/settings/routes.go`: the local `authorizeRoute` and `readRefusal`
  go. Each call site uses `route.Guard{Refusals: h.guard}.AuthorizeObject` with
  `route.Checks{Namespace: identity.NamespaceObject(t.ns.Name), Reads: ...,
  Actions: ..., NotFound: errNotFound, RequireAction: false}`.
- `internal/api/objectcontext/routes.go`: the caller keeps the rule that drops
  the namespace read when the object is the namespace, because the helper reads
  it first.
- The 404 of an object that the caller cannot read stays: it is the answer of
  `route.Checks.NotFound`, and the refusal of the list keeps its own 403 code.

OUT:

- The other packages that already use the shared helper.
- The 404 answer of the tool-compatible APIs: their error shapes follow their
  tools, and they answer no code of the catalog.
- The IP allowlist of the sign-in routes and of the address-only routes: they
  have no read check and no object.
- A change of the refusal text or of the code `access.ip-not-allowed`.

## Check

| Guard | Test | Mutation that fails it |
| --- | --- | --- |
| The triggers surface keeps the refusal of the list | `TestAddressOutsideTheAllowlistAnswers403` in `internal/api/triggers` (an authorizer that joins `identity.ErrDenied` and `ipallow.ErrAddressNotAllowed` on the namespace read; the answer is 403 `access.ip-not-allowed` with the message `forbidden`) | restore the local `readRefusal` |
| The context surface | `TestAddressOutsideTheAllowlistAnswers403` in `internal/api/objectcontext` | the same |
| The unit settings surface | `TestAddressOutsideTheAllowlistAnswers403` in `internal/api/settings` | the same |
| An object that the caller cannot read still answers 404 | the existing not-found tests of the three packages | answer 403 for every `identity.ErrDenied` |

The tests model `internal/api/jobs/ipallow_test.go`, which joins the two errors
in `addressRefused` and expects `errcodetest.Cause{Code: errcode.AccessIPNotAllowed,
Message: "forbidden", Fixed: true}`. `internal/api/route/authorize_test.go:116-123`
covers the shared helper itself.

## ADR check

**ADR 55 decision 8 (not owner-tagged).** "IP allowlists are off by default, and
each scope opts in." Changed by: ADR 70, 285, 297, 301, 320 and 321; none
changes decision 8. Current rule: this ADR with those. The fix does not change
the default or the scopes. No conflict.

**ADR 361 decisions 2 and 3 (owner, option A).** Decision 2: "**The IP allowlist
gate fills the list (owner, option A).** For a scope with `All` set, the gate
puts in the list each namespace whose allowlist refuses the client address."
Decision 3: "**Every consumer of the scope applies the list (owner, option
A).** The consumers are the namespace filter of both SQL dialects (a `NOT IN`
clause), the deleted-units read scope, the namespace list and the caller-access
API." The three surfaces are consumers, and they apply the list. The fix makes
their answer agree with the other consumers. No conflict.

**ADR 250 decisions 1 and 4 (not owner-tagged by the rule; the owner decided
the ADR).** "Never only one layer." The fix removes a copy of a check and keeps
the shared one. The 404 of an unreadable object stays its own layer, and its
test stays. No conflict.

**ADR 276 decision 1 (owner).** "Every error answer has a code (owner). Each 4xx
and 5xx answer of each HTTP adapter has a stable machine code." The fix answers
the existing code `access.ip-not-allowed`. No conflict.

**ADR 182 decision 1 (owner).** "A stable machine code for each error (owner).
Each error that an API answers has a machine code." Changed by: ADR 210, 234
and 276. Current rule: this ADR with those. The fix adds no code and changes
no meaning. No conflict.

No conflict with an owner-tagged decision.

## Open questions

none
