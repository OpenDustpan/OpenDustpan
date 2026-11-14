# 361. A scope that covers every object names the namespaces that it leaves out

Status: accepted · 2026-11-08. Records the owner decision of 8 November 2026 on the IP allowlist and a scope with All set

Changes:

- None.

## Context

The IP allowlist gate filters the scope that `Authorizer.ListObjects` returns.
The gate is in `pkg/ipallow/gate.go`. It leaves out each namespace, collection
and unit in a namespace whose allowlist does not contain the client address.
A scope with `All` set stays unchanged, because the scope cannot say "every
object except the objects of namespace X". `identity.Scope` in
`pkg/identity/identity.go` has `All` and the explicit lists only.

`All` is set for an administrator that is not the bootstrap administrator,
and for a principal with a grant of the action on the installation. For these
principals, a client outside the allowlist of a namespace still sees that
namespace in a list. The namespace list in
`internal/api/server/namespaces.go` shows its name and home region. The
caller-access API in `pkg/access/caller_access.go` answers `all=true`. The
gated `Authorize` refuses the same principal on each object of that
namespace.

The caller-access and caller-grants routes in
`internal/api/server/review.go` list explicit namespace objects through the
concrete authorizer, with no gate.

Three options were considered. A: the scope names the namespaces that it
leaves out. B: leave out only the explicit objects and keep `All` unchanged
(what ships today). C: the gate turns `All` into an explicit list of
namespaces. C fails closed for objects outside every namespace, such as
installation principals, and a large installation makes a large scope.

This ADR reads with [ADR 55](0055-sign-in-methods-in-milestone-4.md)
decision 8, [ADR 122](0122-the-agent-tool-set.md) decision 6 and
[ADR 218](0218-what-a-namespace-is-for-and-when-to-make-one.md) decision 1.
ADR 55 decision 8 gives the IP allowlist its scopes. ADR 218 decision 1 makes
the namespace the object of the allowlist. ADR 122 decision 6 says that a
list filters with `Authorizer.ListObjects` and says when it left objects out.

The owner decided on 8 November 2026 for option A. The owner said, verbatim:
"Yes on A" (8 November 2026, about 01:12 UTC, to the product manager,
answering the options of #698). The decision is also recorded in a comment
on #698.

## Decision

1. **A scope that covers every object can name the namespaces that it leaves
   out (owner, 2026-11-08).** `identity.Scope` gets a list of excluded
   namespaces next to `All`. The list has a meaning only when `All` is set.
2. **The IP allowlist gate fills the list (owner, option A of #698).** For a scope with `All` set, the
   gate puts in the list each namespace whose allowlist refuses the client
   address. It reads the namespaces that have an allowlist through a new
   method of the `ipallow` store port. Both SQL adapters implement the
   method. The method gets conformance tests before any adapter implements
   it.
3. **Every consumer of the scope applies the list (owner, option A of
   #698).** The consumers are the namespace filter of both SQL dialects (a
   `NOT IN` clause), the deleted-units read scope, the namespace list and the
   caller-access API. The caller-access API shows the list next to `all`, so
   that a user can see why a namespace is not in a list.

## Not decided here

- The wire name of the new field of the caller-access API.
- Whether the bootstrap administrator gets the same list. Today it is outside
  the gate.
- The cost limit when an installation has many namespaces with an allowlist.

## Consequences

- A new case that each consumer reads through `Scope.Contains` or the shared
  SQL filter.
- (PM) Fail closed. A consumer that cannot apply the list refuses the
  request. A read error of the allowlist store gives a refusal, not a scope
  without the list. A test with a principal whose scope has `All` set, a
  namespace allowlist and a client address outside it shows that the
  principal does not list that namespace.
- (PM) The caller-access and caller-grants routes go through the gate too, so
  that an explicit namespace object of a refused namespace is not listed.
- (PM) The gated `Authorize` does not change. It already refuses each object
  of a refused namespace. The list only makes the lists agree with
  `Authorize`.
- (PM) The every-object case is a closed type ("all except") that consumers
  read only through `Scope.Contains` and one shared SQL filter helper, with
  no bare `All` field, so a consumer that ignores the exclusions is a compile
  error, not a silent leak. Each consumer has a hostile test.
- (PM) The implementation carries the excluded namespaces inside the closed
  every-object case. There is no bare `All` field.
- The namespace list and the caller-access API change shape. This is allowed
  before 1.0.
- The slice that implements it carries the user docs change of the IP
  allowlist page.
