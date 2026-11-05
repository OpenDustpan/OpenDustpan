# 327. The job proxy

Status: accepted · 2026-11-05. Decided by the project owner on 18 October, 26 October and 1 November 2026. Accepts RFC 16 in part. The other items of the RFC proposal wait for the owner in RFC 16

Changes:

- Changes ADR 134 decision 6: the steps of the role `tool` of a unit with
  proxy rules get `HTTPS_PROXY` and the path of the CA of the job.
- Changes ADR 137 decision 6: for a unit with proxy rules, the sandbox can
  reach only the job proxy.

## Context

Many products that teams manage with OpenTofu or the Pulumi CLI have a weak
access model: one admin key or one root token, and no RBAC. A job that
manages one small part of such a product gets that key. Code in the job can
then change all of the product, and can send the key anywhere.

[RFC 16](../rfc/0016-egress-proxy.md) proposes a job proxy between a job and
the network. The job proxy allows only the requests that a rule allows, and
adds the credential itself. So the job never holds the credential. The RFC
holds the problem, the prototype findings of the spike of 26 October 2026,
the facts of other proxies and the full proposal of 30 October 2026. Where
this ADR and the RFC differ, this ADR holds.

The job proxy is an escape hatch, not a default. When a product has native
RBAC and short-lived credentials, teams use those. The job proxy makes a
root token safer. It does not make it safe.

### The owner decisions

- 18 October 2026: a research spike comes first. The names are "job proxy",
  "outbound guard" (`pkg/egress`) and "upstream proxy".
- 26 October 2026: accept the recommendation of the spike as an ADR. That
  is the RFC 16 section "Prototype findings", subsection "Recommendation",
  items 1 to 9. Each item sums up a finding of the same section. This ADR
  cites the item and, where it needs more words, the finding that the item
  sums up.
- 1 November 2026: no scope cut in milestones 4 to 6. RFC 16 stays in
  milestone 6.
- 1 November 2026: the new words of milestone 6 are accepted as listed.
  The list named "proxy rule" for one allow line of the job proxy.

### Where each decision comes from

The decisions below keep the numbers of the decisions of the RFC section
"Proposal (2026-10-30)", so that the work items can name them. That
proposal came after the owner decision of 26 October. So each decision here
holds only the part that the recommendation of 26 October already says.
The rest of each proposal decision waits for the owner in the RFC 16
section "Proposed (PM), waiting for the owner", as the item with the same
number (for example P2 for decision 2). The numbers 3, 5, 9, 11 to 17 and
19 are not here, because no owner decision covers them.

| Owner decision | Decision of this ADR |
| --- | --- |
| 26 October, item 1: our own proxy in Go, next to each job, no dependency | 1, 2 |
| 26 October, item 2: plain data rules on host, method and path. Templates render at the job start | 4 |
| 26 October, item 3: way A, way D and way B. Way C only as an opt-in to loopback | 4, 7 |
| 26 October, item 4: one CA for each job, with a name constraint and the lifetime of the job | 6 |
| 26 October, item 5: refuse a path that is not canonical | 7 |
| 26 October, item 6: check the CONNECT host, the SNI and the `Host` header against each other | 7 |
| 26 October, item 7: the outbound guard on each rule host, also with an upstream proxy | 8 |
| 26 October, item 8: scan a response for the plain value of the credential | 7 |
| 26 October, item 9: a fixed reason for each failure, and "blocked by the job proxy" in the job log | 7, 10 |
| 1 November: the words of milestone 6 | 18 |
| 1 November: no scope cut | 20 |

Decision 10 has the name "Records" in the proposal. Here it holds only
item 9: the fixed reasons and the line in the job log. The fields of a
record, its storage, its retention and its buffer are item P10.

Each decision says what it needs from an item that waits for the owner.
The job proxy is security code. SSRF through a rule host is in scope.

## Decision

Each decision has the label "(owner, <date>: <source>)".

### The words and the scope

18. **The words (owner, 1 November 2026: the words of milestone 6).** One
    allow line of the job proxy is a **proxy rule**. The set of proxy rules
    of a unit is a **proxy rule set**. Text always writes "proxy rule",
    never "rule" alone, and never "egress rule". "Egress" names the
    outbound guard and the egress allowlist of model calls.
20. **The scope (owner, 1 November 2026: no scope cut).** All of RFC 16
    ships in milestone 6. That includes the decisions of this ADR and the
    items of RFC 16 that the owner accepts later.

### Where the job proxy runs

1. **Our own proxy (owner, 26 October 2026: Recommendation item 1).** The
   job proxy is our own code in Go, with `net/http` and `crypto/tls`, and
   no new dependency. Configuration that OpenDustpan generates for Envoy
   stays an option for later. In which binary the job proxy runs is item
   P1.
2. **Next to each job (owner, 26 October 2026: Recommendation item 1, and
   the RFC 16 section "Where the proxy runs", option 1, which item 1
   names).** The runner starts the job proxy next to each job of a unit
   with proxy rules, in its own container or process, with its own user.
   The network of the tool reaches only the job proxy, through a network
   namespace or a Kubernetes NetworkPolicy. The job proxy, not the tool,
   gets the credentials and the proxy rules of this one job from the
   server.
   - This decision holds the rule, not the mechanism of each coordinator.
     The mechanism (the routes and drop rules of the machine coordinator,
     the proxy Job and the NetworkPolicy of the Kubernetes coordinator, the
     isolation probe and its exit code, and the refusal of the isolation
     `process`) is item P2. The permission of the Kubernetes coordinator for
     a NetworkPolicy is item P19.
   - Until the owner accepts P2 and P19, a coordinator that cannot make the
     tool reach only the job proxy cannot run a job of a unit with proxy
     rules. That follows from this decision. It is not a new rule.
   - How the job proxy gets the credentials and the proxy rules from the
     server (the proxy session) is item P12.

### Proxy rules

4. **Proxy rules are plain data (owner, 26 October 2026: Recommendation
   items 2 and 3, and "Findings on the rule shape", findings 1 and 3).**
   - A proxy rule matches the host, the method and the path of a request.
     A path pattern can use `*` (one segment) and `**` (the rest).
   - A proxy rule only allows. A request that no proxy rule allows is
     refused. A list of proxy rules gives "or". A proxy rule gets "or" and
     "not" conditions only when a real case needs them.
   - A template in a pattern renders when the job starts, once. A template
     value that is not exactly one path segment fails the render. The job
     then fails before it starts, and the error names the proxy rule and
     the variable.
   - Each proxy rule uses one of three ways: way A (a TLS end) for a host
     with a credential or a path rule, way D (a tunnel, the host only) for
     a host with a host rule only, and way B (the endpoint mode) as an
     opt-in for each proxy rule.
   - The document format, the fields, the scopes, the size limits, the
     variables and the errors are item P4.
   - A proxy rule can name a credential to add. Where the credential comes
     from is item P5. Until the owner accepts P5, a proxy rule cannot name
     a credential, and the job proxy adds none.
6. **One CA for each job (owner, 26 October 2026: Recommendation item 4,
   and "Security trade-offs", first item).** The job proxy uses one CA for
   each job. The CA has a name constraint to the hosts of the proxy rules
   of the job, and the lifetime of the job. Only the job proxy holds the
   key. The job gets the certificate, never the key. The tool steps get
   `HTTPS_PROXY` and the path of the CA (way A in "Findings on credential
   injection").
   - The key type, the variables of each runtime and how the certificate
     reaches the launcher are item P6. The path of the certificate through
     the server is item P12.

### Each request

7. **Each request (owner, 26 October 2026: Recommendation items 3, 5, 6, 8
   and 9, and "Findings on the rule shape", findings 2, 4 and 5).**
   - A CONNECT to a host that no proxy rule names is refused with 403,
     before any TLS (the table "Failure modes", which item 9 names).
   - For way A, the job proxy ends the TLS of the job with a certificate of
     the CA of the job. It refuses a request when the CONNECT host, the SNI
     and the `Host` header (`:authority` in HTTP/2) are not the same name.
   - It refuses a path that is not canonical, with 400. It does not
     normalize a path. It sends the path that it checked, with no change.
   - It matches the method and the path against the proxy rules, and
     refuses a request that no proxy rule allows with 403.
   - For a proxy rule with a credential, it adds the credential to the
     allowed request. It scans each response of that proxy rule for the
     plain value of the credential, and breaks the stream on a match.
   - It refuses a credential over plain `http`, unless a proxy rule opts
     in, and then only to loopback.
   - Without P5, no proxy rule has a credential (decision 4). The steps of
     this decision that need a credential then do not apply, and the other
     steps apply.
   - The headers that the job proxy deletes, redirects, HTTP/2, the encoded
     forms of the credential in the scan and the text of a refusal are item
     P7.
8. **The outbound guard and the upstream proxy (owner, 26 October 2026:
   Recommendation item 7, and "Findings on the upstream proxy", finding
   2).** The job proxy applies the outbound guard to the resolved address
   of each rule host. With an upstream proxy, it resolves and checks each
   rule host before it sends the CONNECT. The docs say that the upstream
   proxy must apply its own rules to the final address.
   - Where the URL and the credential of the upstream proxy come from, the
     private ranges that a pool allows, and the mapping of each error to a
     fixed reason are item P8.

### Records

10. **Fixed reasons (owner, 26 October 2026: Recommendation item 9, and the
    table "Failure modes").** The job proxy writes each failure as a fixed
    reason in its record of the request. The job log shows "blocked by the
    job proxy", with the host and the path.
    - The fields of a record, where records are kept, their retention, the
      audit entry of a phase and the record buffer are item P10.

## Consequences

- A unit with no proxy rule set works as before, with no job proxy.
- The package `pkg/jobproxy` holds the rule model, the canonical path
  check, the match and the fixed reasons.
- Each layer of the job proxy gets its own test with hostile input, as the
  RFC section "Security tests" lists for the decisions of this ADR: the
  boundary, the encoding, the limits, the authorization at the point of
  use, and the fail-closed records.
- The e2e suite runs the steps of the RFC section "End-to-end steps" that
  the decisions of this ADR cover, for OpenTofu and for the Pulumi CLI.
- The docs page "Narrow an admin key with the job proxy" says when to use
  the job proxy, and when to use the native RBAC and short-lived
  credentials of a product.
- Code in the job can still make each request that the proxy rules allow,
  with the injected credential. The job proxy narrows what that is. It does
  not judge intent.
- A product that echoes the credential in a form that the scan does not
  know passes the scan. The scan is a second layer, not a guarantee.
- Name constraints protect only clients that honor them. Go honored them in
  the spike. Other clients may not.
- With an upstream proxy, a DNS answer that changes between the check of
  the job proxy and the dial of the upstream proxy is outside the reach of
  the job proxy. The upstream proxy is the only layer that sees the final
  address.
- The questions that RFC 16 keeps open stay open there.
