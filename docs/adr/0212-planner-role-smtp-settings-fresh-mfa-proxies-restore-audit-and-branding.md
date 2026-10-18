# 212. The planner role, SMTP settings, fresh MFA for weaker settings, trusted proxies, the restore audit entry and the branding preset

Status: accepted · 2026-10-18 · supersedes in part ADR 27 decision 2 · extends ADR 52, ADR 56 and ADR 180 decision 11

## Context

Decided by the project manager under the owner's decision rule, 2026-10-18.

Six open questions of the identity, settings and audit RFCs have one answer
that Google, AWS, PCI DSS, SOC 2 and ISO 27001 share:

- [RFC 19](../rfc/0019-users-teams-roles.md): is "planner" a built-in role?
  The data roles exist: `viewer`, `auditor`, `secret-viewer`, `writer`,
  `maintainer` and `admin`. A planner role waited "for a route that
  separates a plan from an apply". ADR 103 decision 8 gives that route:
  `unit:run-plan` starts a runner job that only plans.
- [RFC 20](../rfc/0020-access-tokens.md) and ADR 53: where the SMTP settings
  live. The email warning of an expiring token needs them, and ADR 145
  sends email through the SMTP relay of the installation.
- [RFC 68](../rfc/0068-installation-settings.md) question 4: which setting
  changes need a fresh MFA step. ADR 70 already asks for one at three
  changes, for example "full sync" from the identity provider.
- [RFC 22](../rfc/0022-audit-log.md): which client address the server
  believes behind a proxy, and what a restore writes to the audit log.
  ADR 27 decision 2 says that "the server never trusts `X-Forwarded-*`
  headers". The code on `main` does more: the settings `trusted-proxies`
  and `trusted-proxy-header` (package `internal/clientip`) make the server
  read one forwarding header from named proxies.
- [RFC 9](../rfc/0009-themes-and-white-labeling.md) and ADR 180 decision 11:
  an operator sets a branding preset for the installation. Where it lives
  and how a client sets it are open.

## Decision

1. **A built-in `planner` role.** `planner` is a data role. It holds each
   action of `viewer`, and `unit:run-plan` and `job:read-plans` (ADR 103
   decision 8). So a planner can start a runner job that only plans, and
   can read the masked plan. It cannot apply, destroy, approve or read a
   secret value. ADR 197 decision 3 also gives it `changeorder:author`,
   because it has `unit:run-plan`.

   **The caveat for a workstation.** The role limits what the server runs
   and records. A planner who runs `tofu plan` or `pulumi preview` on a
   workstation reads state with the state actions that the role gives, as
   `viewer` does. That plan is not a job and the server keeps no record of
   it (ADR 152 decision 12). A person who also holds cloud credentials can
   change the cloud from a workstation. The role does not prevent that. The
   role page in the docs says so.

   Benchmark: HCP Terraform has a fixed "plan" permission set on a
   workspace, between "read" and "write". It separates a plan from an
   apply.
2. **SMTP settings are installation settings.** The relay, the sender and
   the credentials of SMTP are installation settings with the precedence of
   ADR 63 decision 1. The password is a sensitive setting (ADR 63 decision
   6). It comes only from an environment variable or from a file that a
   `*_FILE` key names (ADR 63 decision 4). It never comes from a flag or
   from the database.

   Benchmark: the `*_FILE` convention of container images for secrets.
   Designed against PCI DSS v4.0.1 requirement 8.6.2: no password of a
   system account in a script or in a configuration file.
3. **A change that reduces security needs a fresh MFA step.** A change of
   an installation setting through the API needs a fresh MFA step when it
   makes the installation less secure. The step is the step of ADR 70: the
   window of ADR 56 for built-in MFA, and the window of ADR 71 for a console
   session from an SSO provider. With built-in MFA off and no SSO provider,
   ADR 56 decision 5 applies, and the change needs no step.

   The settings inventory of RFC 68 marks each setting and each direction
   that reduces security. The first list:
   - built-in MFA from on to off, "require MFA for all users" from on to
     off, and "require MFA for a secret reveal" from on to off,
   - an MFA freshness window or the token freshness window made longer, or
     set to 0,
   - a longer largest lifetime of an access token or of a session,
   - trusted-header sign-in from off to on,
   - IP allowlists from on to off,
   - "revoke on removal" from on to off,
   - the audit log, the hash chain or signed checkpoints from on to off,
     when the API can set the setting,
   - a longer list of trusted proxies, when the API can set the setting,
   - the three changes of ADR 70 decisions 16, 17 and 23.

   The declaration of each setting says which change reduces security, as
   it says whether the setting is sensitive. A test checks each entry of
   the list.

   Benchmark: Google asks for a new sign-in before a change of security
   settings of an account. Designed against NIST SP 800-53 Rev. 5 IA-11
   (authenticate again for privileged functions).
4. **Trusted proxies: named proxies, one named header, a walk from the
   right.** This replaces "the server never trusts `X-Forwarded-*` headers"
   of ADR 27 decision 2. The rest of that decision stays.
   - By default the client address is the TCP peer.
   - `--trusted-proxies` names the trusted proxies as CIDRs or addresses.
     The server refuses a list that holds `/0` or a prefix with host bits.
   - `--trusted-proxy-header` names the one header that the server reads:
     `x-forwarded-for` (the default) or `forwarded` (RFC 7239). The server
     never reads the other header.
   - The server reads the header only from a trusted peer. It walks the
     header from the right and takes the first address outside the trusted
     list. So a value that a client prepends never wins over the value of
     a trusted proxy. A walk stops after 32 trusted entries.
   - In each doubt the address is unknown, never the peer. An IP allowlist
     refuses an unknown address.

   Benchmark: AWS Elastic Load Balancing and Google Cloud Load Balancing
   add the client address at the right end of `X-Forwarded-For`. So the
   entries on the right are the entries that the trusted proxy wrote.
5. **A restore writes one audit entry.** Each restore writes one audit
   entry on the object `@server`, after the restored data is in place. It
   names the principal, the backup ID and the SHA-256 of the backup
   manifest. So a verifier can tell a restore from a rewrite of the log.

   Benchmark: designed against SOC 2 CC7.2 and ISO/IEC 27001:2022 Annex A
   8.15 (logging of events that change the system).
6. **The branding preset is an installation setting.** The preset of ADR
   180 decision 11 is an installation setting with the precedence of ADR 63
   decision 1. `GET /api/dustpan/v1/settings/branding-preset` answers it.
   `PUT` on the same path changes it, with the action
   `installation:manage-settings` (ADR 210 decision 3), and writes an audit
   entry. The `GET` route is on the public list of the route test, because
   the sign-in page shows the preset before a person signs in. It holds no
   secret.

   Benchmark: Microsoft Entra ID keeps company branding of the sign-in page
   as one setting of the tenant, which an administrator sets through the
   Microsoft Graph API.

## Not decided here

- How a request with an access token passes the fresh MFA step of decision
  3. ADR 46 decision 8 lets an access token act with no MFA step.
- The names of the SMTP settings, and whether email needs TLS.
- The rest of the white-label mechanism of RFC 9: logos, names and custom
  presets.

## Consequences

- `pkg/access` gets the `planner` role. The role page in the docs gives the
  caveat of decision 1.
- The settings declarations get the field for decision 3. The settings API
  answers a change that needs a fresh MFA step as ADR 71 does: 401 with the
  path of a new sign-in, or the MFA prompt of ADR 56.
- The restore code writes the entry of decision 5. The audit guide says how
  a verifier finds it.
- `docs/explanation/security-model.md` describes the proxy rule of decision
  4 as the decided rule.
