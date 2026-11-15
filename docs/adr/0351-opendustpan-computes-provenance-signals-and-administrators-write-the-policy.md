# 351. OpenDustpan computes provenance signals, and administrators write the policy

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Extends ADR 222: the gates over facts also decide the admission of a package.
- Extends ADR 347 decision 4: the integrity of the served bytes is a security control. The meaning of provenance is not.

## Context

RFC 93 asks how OpenDustpan vets the OpenTofu providers, the OpenTofu modules and the Pulumi resource plugins that a job runs. RFC 101 and ADR 269 cover the packages that a team uploads. RFC 123 asks the same question for sweep plugins.

The first design of RFC 93 had a built-in gate that no narrower scope could lower. It also had a signature result and a minimum age that the server read as a verdict.

On 7 November 2026 the project owner decided that OpenDustpan gives no verdict. The owner's words:

> We don't decide. We can provide guidelines, but at the end of the day admins need to decide what provenance means to their organization. This covers which vendors, GitHub or GitLab orgs, registries or individual provider versions they trust. It covers whether GitHub stars, repository age and last commit time mean anything to them. It covers what govulncheck means to them, and whether they want additional external scanners. These are just examples, not an exhaustive list. Provenance means one thing to an advertising startup. It means another thing to a medical company with an ISO regulated SOUP process.

## Decision

Decided by the project owner on 7 November 2026.

1. **Signals, not verdicts (owner, 2026-11-07).** OpenDustpan computes provenance signals and shows them as facts. A signal is a fact about a package. Examples: the publisher and its organization, the registry, the signature, and the attestation (SLSA, Sigstore, OpenPGP). More examples: the age of the version, the age of the repository, the last commit and the stars. Also a vulnerability scan result (govulncheck, OSV) and the results of external scanners. The list is not complete. OpenDustpan has no built-in answer to the question "is this package trusted". It gives no combined score.
2. **The administrator writes the policy (owner, 2026-11-07).** The policy is a gate over those facts, with the gate model of ADR 222 to ADR 230. An administrator puts it at the installation, the namespace or the unit. The administrator decides which vendors, organizations, registries and versions to trust. The administrator decides what each signal means. The administrator decides which external checks a package needs.
3. **Guidelines and example policies (owner, 2026-11-07).** OpenDustpan ships guidelines and example policies for each kind of organization. They are data that an administrator copies and edits. They are not defaults. The kinds are a startup, an enterprise with an allowlist, and a regulated organization. The regulated organization has a SOUP process and a record of each approved item.
4. **No magic (owner, 2026-11-07).** Each allow and each deny names the policy and the facts that it read. The API shows the effective policy. A signal from outside the installation is an unverified fact. The documentation says which signals an attacker can fake, for example stars. A fact shows its origin, the host that gave it and the time of its read.
5. **Integrity is code (owner, 2026-11-07).** The server serves only the bytes that a person admitted. It checks each hash, each size limit and each signed URL. This is not a view on provenance. An administrator cannot turn it off.
6. **One mechanism, two kinds of package (owner, 2026-11-07).** The same facts and the same gates serve every package. This covers the public packages of RFC 93, the private packages of ADR 269 and RFC 101, and the sweep plugins of RFC 123. The distinction between public and private packages is a fact and a default. It is not a second mechanism. The owner's words:

   > I would make a distinction between public and private packages. Teams often don't impose the same strict rules on their own code because they're by definition not SOUP.
   - **Ownership is a fact (owner, 2026-11-07).** Every package has an `ownership` fact with one of two values. `first-party` means that the organization built and published the package in its own private registry or namespace. `third-party` means public code or other outside code. In the regulated sense, third-party code is SOUP.
   - **The server knows the ownership from its own records (owner, 2026-11-07).** The value is computed, not claimed. A package is `first-party` only when all of these are true. The package is in a private registry or namespace of this installation. A principal uploaded it through the publish route. A grant of that principal allowed the upload. A package that the server fetched from an upstream host is always `third-party`. A name, a label or a field in the package that says "ours" has no effect. The fact shows the registry, the namespace, the principal and the grant that the server used.
   - **The defaults differ (owner, 2026-11-07).** A first-party package goes through the review flow that the team already has. That flow is the review request and the gates of the team. A first-party package has no provenance policy by default. A third-party package goes through the provenance policy of the administrator. The example policies follow this split.
   - **The administrator may apply strict rules to first-party packages (owner, 2026-11-07).** This is a choice, as in decision 2. The administrator writes a gate that reads `ownership`.

## Consequences

- RFC 93 follows this ADR. It removes the built-in final gate and the fixed minimum age. The decision point, the fact schema, the actions and the routes stay open in RFC 93 until the owner accepts them in a later ADR.
- RFC 123 needs an edit. Its rule about the signature of the project key becomes an example policy. The check of the admitted bytes stays fixed. This change does not edit PR #2572.
- ADR 269 stays as it is. A private version goes through the review flow of the team, with no provenance policy by default (decision 6). An administrator who wants provenance rules for private packages writes a gate that reads `ownership`.
- A host or a scanner can give a wrong or old fact. The gate and the reader see the origin and the time. The administrator owns the quality of the policy. The guidelines help.
- This ADR does not decide the fact schema or the fields of each signal. It does not decide the names of the actions, the routes or the external-check API. It does not decide the content of the example policies. RFC 93 holds these questions.
