# 330. The job proxy: core decisions P1 to P12

Status: accepted · 2026-11-06. Decided by the project owner on 6 November 2026

Changes:

- Extends ADR 327: it accepts items P1 to P12 of RFC 16. ADR 327 held only the part of the items that the owner decided on 26 October 2026.
- Extends ADR 134 decision 6: the launcher writes the CA bundle and the proxy variables for the tool steps (decision 6).
- Extends ADR 137 decision 6: a sandbox has one route, to the job proxy (decision 2).

## Context

[ADR 327](0327-the-job-proxy.md) accepts the parts of [RFC 16](../rfc/0016-egress-proxy.md) that have an owner source. It says that the other parts wait for the owner in the RFC 16 section "Proposed (PM), waiting for the owner".

On 6 November 2026, about 13:55 UTC, the owner wrote: "All are approved by me as proposed. RFC55 on 'no feature gets cut'." The PM session gave the scope for RFC 16: the core proposals P1 to P12. This ADR accepts these twelve items.

Items P13 to P17 and P19 are not part of this decision. They stay proposals for later, each with its own review.

## Decision

Decided by the project owner on 6 November 2026. The owner approved items P1 to P12 as proposed in the RFC. Decision n of this ADR is item Pn of RFC 16. The text is the text of the RFC.

1. **One binary, a separate process.**
   The job proxy is our own code in Go, with `net/http` and `crypto/tls`,
   and no new dependency. It is the mode `proxy` of `dustpan-launcher`.
   One release carries it, and the skew rules of ADR 132 apply. It always
   runs as its own process, as its own user, outside the sandbox of the
   steps. It shares no file system, no process space and no memory with a
   step or with the launcher of the steps.

2. **Where it runs, for each isolation kind.**
   - `firecracker` and `gvisor`: the machine coordinator starts the job
     proxy on the host for each claim of a unit with proxy rules, as a
     user of its own. The network namespace of the sandbox gets one route,
     to the address of the job proxy. The coordinator drops each other
     packet of the sandbox, DNS included. The tool needs no DNS, because
     it sends the name in the CONNECT.
   - `kubernetes`: for each such claim, the coordinator creates a second
     Kubernetes Job, the proxy Job, from `opendustpan/launcher:<release>`.
     It also creates a NetworkPolicy for that claim: the job pod can reach
     only the proxy pod, and the proxy pod accepts only the job pod. The
     permission of the coordinator for this NetworkPolicy is item P19.
     The proxy pod runs as a service account with no role, from the Helm
     values. The proxy sends its pod address through its proxy
     session, and the launcher reads it through its own session.
   - The isolation probe: a NetworkPolicy works only with a network plugin
     that enforces it, and a drop rule can be wrong. So at each claim,
     before the first step, the launcher tries to open a TCP connection to
     the OpenDustpan server directly, not through the job proxy, with a
     time limit of 2 seconds. If the connection opens, the launcher runs
     no step, reports the phase failed with the reason
     `network-not-isolated`, and ends with the exit code 72. The runner
     then lets the claim expire.
   - `process`: this isolation has no kernel boundary. A unit with proxy
     rules never runs on it. The start of such a job on a pool that allows
     only `process` fails with `runner.ErrIsolation`, which names the
     unit. A runner with `process` isolation never claims it.

3. **One exit.** The job proxy is the only exit of the sandbox. The
   launcher and the tool reach the OpenDustpan server through it too, with
   a built-in tunnel rule for the host of the server only. The launcher
   keeps its own TLS check of the server.

4. **Proxy rules are plain data.** A
   proxy rule set is a JSON document that a namespace, a blueprint version
   (field `proxy`) or a unit holds. The proxy rule sets of the scopes of a
   unit are joined. A proxy rule only allows. A proxy rule has:
   - `id` (1 to 64 bytes: letters, digits and `-`), and `operations`, a
     list of `plan`, `apply`, `destroy` and `refresh`,
   - `host`, an exact DNS name in lowercase, and `port` (default 443),
   - `mode`: `inspect` (a TLS end), `tunnel` (the host only) or `endpoint`
     (a loopback port),
   - for `inspect` and `endpoint`: `methods`, and `paths` with `*` (one
     segment), `**` (the rest, last only) and `{{ variable }}` (one whole
     segment),
   - `credential` and `clientCertificate`: names of exports of a profile of
     the unit (item P5),
   - `serverTrust`: `system` (the default) or the name of a CA export of a
     profile.

   The parser refuses an unknown field, a duplicate key, an IP literal host
   (unless the pool allows the range, decision 8), a path pattern that is
   not canonical, and a document over 64 KiB or over 256 proxy rules.

   The variables are `unit.tool`, `unit.namespace`, `unit.name`,
   `unit.labels.<key>`, `operation`, `inputs.<name>` (a string answer of a
   Catalog request) and `profile.<name>.<key>` (a value that is not
   secret). The job start renders the proxy rules once. A value that is not
   exactly one valid path segment, or a value marked secret, fails the
   start with `jobproxy.ErrRender`. The error names the proxy rule and the
   variable. The job spec holds the SHA-256 of the rendered proxy rules, so
   each phase runs the same proxy rules.

   "Or" comes from a list of proxy rules. A proxy rule gets "or" and "not"
   conditions only when a real case needs them.

5. **Credentials for the job proxy only.** A profile document gets the
   export key `proxy`: a map from a name to a `header` (name and value),
   `basic` (user and password), `query` (name and value),
   `client-certificate` (certificate and key) or `ca` (certificates). Only
   the consumer `job-proxy` gets `exports.proxy`. The consumer `launcher`
   never gets it, and a test checks the answer for each consumer. A proxy
   rule that names an export that no attached profile gives fails the job
   start with `jobproxy.ErrCredential`. The tool gets a placeholder, for
   example `dustpan-placeholder`, in the variable that the proxy rule names.
6. **The CA of a claim.** At start,
   the job proxy makes an ECDSA P-256 key and a CA certificate in memory.
   The CA has a name constraint to the hosts of the `inspect` proxy rules
   and the lifetime of the claim. Only the job proxy holds the key. The job
   proxy sends the certificate, never the key, to the server through its
   session. The launcher reads the certificate through its own session. It
   writes a bundle (the roots of the image plus the CA) for the tool steps,
   and names it in `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`,
   `REQUESTS_CA_BUNDLE` and `AWS_CA_BUNDLE`. `HTTPS_PROXY` and `HTTP_PROXY`
   name the job proxy, and `NO_PROXY` is empty.

7. **Each request.** For a CONNECT,
   the job proxy does these steps:
   1. It refuses a host that no proxy rule names, with 403, before any TLS.
   2. For `tunnel`, it resolves the host, checks each address with the
      outbound guard, and connects to the address that it checked.
   3. For `inspect`, it ends the TLS of the client with a leaf certificate
      of the CA of the claim. It refuses an SNI or a `Host` (`:authority`)
      that is not the CONNECT host. It refuses a path that is not
      canonical with 400. It does not normalize a path. It matches the
      method and the path against the rendered proxy rules. It refuses a
      request that no proxy rule allows with 403 and the body "blocked by
      the job proxy: <method> <host><path>: no rule matched".
   4. It deletes `Authorization`, `Proxy-Authorization`, `Cookie` and each
      header that the credential sets, and adds the credential. It sends
      the request with its own TLS to the address that the outbound guard
      checked, with HTTP/2 where the server offers it.
   5. It never follows a redirect. The tool follows it as a new request,
      and the proxy rules check it again.
   6. For a proxy rule with a credential, it scans the response for each
      form of the credential: the plain value, base64 (standard and URL,
      with and without padding), the JSON escape form and the percent
      form. The window is as long as the longest form. On a match, it
      breaks the stream.

   The job proxy refuses a credential over plain `http`, unless a proxy
   rule opts in, and then only to loopback.

8. **The outbound guard and the upstream proxy.** The job proxy dials with the rules of `pkg/egress`. It
   never opens the always-denied addresses. It opens a private address
   only in a range that the runner pool allows
   (`egress.ParseAllowedRanges`). A pool can name an upstream proxy, with a
   URL and a credential. The credential comes from the pool, never from the
   profile of a unit. The pool seals it with the data key of the namespace,
   and only a proxy session gets it. With an upstream proxy, the job proxy
   still resolves and checks each rule host before the CONNECT. The docs
   say that the upstream proxy must apply its own rules to the final
   address. An error text never holds the URL or the credential of the
   upstream proxy: the job proxy maps each error to a fixed reason.

9. **Gates read a fact.** The fact schemas of `change.propose`,
   `change.planned` and `change.apply` get the group `proxy`, as a minor
   version: `enabled`, `rules` (the count), `ruleSetDigest`,
   `credentialHosts` (a set of hosts), `plainHttp` and `learnMode`. A gate
   decides on the change. A gate never matches one request of a job.
10. **Records.** The job proxy writes
     one record for each request: the job, the phase, the unit, the
     operation, the mode, the method, the host, the path when it is
     canonical, the proxy rule, the decision, a fixed reason, the status,
     the bytes and the time. A record never holds a header, a body, a query
     value, a credential or the error text of a library. The records of a
     phase are a new kind of job log, under the retention of job logs, and
     are sealed with an item key. The audit log gets one entry for each
     phase, with the counts of allowed and denied requests and the digest of
     the proxy rule set. If the job proxy cannot send its records, it
     refuses new requests when its buffer of 10,000 records is full. No
     request passes without a record. The job log shows "blocked by the job
     proxy" with the host and the path.

11. **Actions.** `namespace:manage-proxy-rules` on the namespace and
     `unit:manage-proxy-rules` on the unit save a proxy rule set. The save
     needs a fresh authentication, and writes an audit entry with the old
     and the new digest. A blueprint version holds proxy rules through
     `blueprint:publish`. `namespace:manage-runners` sets the allowed ranges
     and the upstream proxy of a pool.
12. **Routes.** `GET` and `PUT .../namespaces/{namespace}/proxy-rules`,
     `GET` and `PUT .../units/{unit_id}/proxy-rules`,
     `GET .../units/{unit_id}/proxy-rules/rendered?operation=plan` (the
     proxy rules of the unit with their values, for a person who debugs),
     and `GET .../jobs/{id}/proxy-records`. The runner protocol gets the
     proxy session paths under `/runner/v1`. The proxy fields raise the
     assignment version.


## Consequences

- Where this ADR and ADR 327 differ on the same subject, this ADR holds.
- Items P13 to P17 and P19 of RFC 16 stay proposals. Decision 2 names item P19 (the permission of the coordinator for the NetworkPolicy). Until the owner accepts P19, this ADR does not decide that permission.
- RFC 16 marks items P1 to P12 as accepted and links to this ADR.
