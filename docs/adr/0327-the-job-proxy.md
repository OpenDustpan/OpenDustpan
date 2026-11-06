# 327. The job proxy

Status: accepted · 2026-11-05. Decided by the project owner on 18 October, 26 October and 1 November 2026. Accepts RFC 16 in part. The other items of the RFC proposal wait for the owner in RFC 16

Changed by:

- ADR 330 extends: items P1 to P12 of RFC 16.

Changes:

- None.

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
  items 1 to 9. This ADR cites only these items. The items name ways A to
  D. The table of the RFC 16 subsection "Findings on credential injection"
  defines these names.
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
| 26 October, item 9: a fixed reason for each failure, and "blocked by the job proxy" in the job log | 10 |
| 1 November: the words of milestone 6 | 18 |
| 1 November: no scope cut | 20 |

Decision 10 has the name "Records" in the proposal. Here it holds only
item 9: the fixed reasons and the line in the job log. The fields of a
record, its storage, its retention and its buffer are item P10.

Each decision says what holds until the owner accepts each item that it
needs. Until the owner accepts P1, P2 and P4, no runner can start a job
proxy and no unit can hold a proxy rule. So until then, each unit works as
before, with no job proxy. The job proxy is security code. SSRF through a rule host is in scope.

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
   stays an option for later.
   - Until the owner accepts P1, this decision does not say in which binary
     the job proxy runs. P1 must keep Go, `net/http`, `crypto/tls` and no
     new dependency.
2. **Next to each job (owner, 26 October 2026: Recommendation item 1).** The
   job proxy runs next to each job of a unit with proxy rules.
   - Until the owner accepts P2, this decision does not say how each
     coordinator starts the job proxy, or how the network of the job
     reaches it. So no runner starts a job proxy until then. P2 and P19
     must keep one job proxy next to each job.
   - Until the owner accepts P12, the job proxy has no path to get its
     proxy rules from the server.

### Proxy rules

4. **Proxy rules are plain data (owner, 26 October 2026: Recommendation
   items 2 and 3).**
   - A proxy rule matches the host, the method and the path of a request,
     with `*` and `**` in a path pattern, and with templates that render
     when the job starts. A proxy rule gets "or" and "not" conditions only
     when a real case needs them.
   - A proxy rule uses way A for a host with a credential or a path rule,
     way D for a host with a host rule only, and way B as an opt-in for each
     proxy rule. Way C is refused, unless a proxy rule opts in, and then
     only to loopback.
   - Until the owner accepts P4, no document format and no scope for proxy
     rules exist, so no unit holds a proxy rule. P4 must keep plain data on
     the host, the method and the path, and templates that render when the
     job starts.
   - Until the owner accepts P5, no proxy rule names a credential, and the
     job proxy adds none. Then each proxy rule uses way A for a path rule,
     or way D for a host rule only.
6. **One CA for each job (owner, 26 October 2026: Recommendation item 4).**
   The job proxy uses one CA for each job, with a name constraint and the
   lifetime of the job. The key stays in the job proxy.
   - Until the owner accepts P6 and P12, the key type of the CA and the path
     of the certificate to the job are not decided, so way A is not
     available. P6 and P12 must keep one CA for each job, the name
     constraint, the lifetime of the job, and the key in the job proxy
     only.

### Each request

7. **Each request (owner, 26 October 2026: Recommendation items 3, 5, 6 and
   8).**
   - The job proxy refuses a path that is not canonical. It does not
     normalize a path.
   - It checks the CONNECT host, the SNI and the `Host` header against each
     other.
   - It scans each response of a proxy rule with a credential for the plain
     value of that credential, and breaks the stream on a match.
   - It refuses way C, unless a proxy rule opts in, and then only to
     loopback (decision 4).
   - Until the owner accepts P5, no proxy rule has a credential (decision
     4), so no response needs the scan.
   - Until the owner accepts P7, the headers that the job proxy deletes,
     redirects, HTTP/2, the encoded forms of the credential in the scan and
     the text of a refusal are not decided. The scan then looks for the
     plain value only, as this decision says.
8. **The outbound guard and the upstream proxy (owner, 26 October 2026:
   Recommendation item 7).** The job proxy applies the outbound guard to
   the resolved address of each rule host, also when an upstream proxy
   carries the connection. The docs say that the upstream proxy must apply
   its own rules.
   - Until the owner accepts P8, no setting names an upstream proxy, so the
     job proxy uses none. The private ranges that a pool allows and the map
     of each error to a fixed reason are not decided. The outbound guard
     then applies its own rules with no extra range.

### Records

10. **Fixed reasons (owner, 26 October 2026: Recommendation item 9).** The
    job proxy writes each failure as a fixed reason in the audit record. The
    job log shows "blocked by the job proxy", with the host and the path.
    - Until the owner accepts P10, the fields of a record, where it is kept,
      its retention, the audit entry of a phase and the record buffer are
      not decided. P10 must keep a fixed reason for each failure and the
      line in the job log.

## Consequences

- A unit with no proxy rule set works as before, with no job proxy.
- Each layer of the job proxy gets its own test with hostile input (ADR
  250).
- Code in the job can still make each request that the proxy rules allow.
  The job proxy narrows what that is. It does
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
