# 212. The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API

Status: accepted · 2026-10-18. Supersedes in part ADR 27 decision 2. Extends ADR 46 decision 8, ADR 52, ADR 56, ADR 103 decision 2 and ADR 180 decision 11. Accepts part of RFC 68. The window and the "set to 0" item of decision 3 superseded by ADR 227. Decision 6 superseded in part by ADRs 232 and 235

Changed by:

- ADR 227 changes decision 3: the window and the set to 0 item.
- ADR 232 changes decision 6: the public sign-in answer gets
  translationFeedbackLinks.
- ADR 235 changes decision 6: the sign-in answer holds the branding, not the
  preset only.
- ADR 301 extends decision 2: the SMTP sender of the installation also sends
  the one-time code of the first enrollment of a factor.
- ADR 365 changes decision 5: a wording refinement, the restore entries
  follow the audit switch.

## Context

Decided by the project manager under the owner's decision rule, 2026-10-18.
Decision 7 and the approval part of decision 3 are owner decisions of
2026-10-18.

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
- RFC 68 ("API shape") proposes one settings API. Today each policy has its
  own route: `/api/server/session-policy`, `/api/server/token-policy` and
  `/api/server/namespaces/{namespace}/ip-allowlist`, each with its own
  shape.
- [RFC 13](../rfc/0013-policy-and-approvals.md): which approvals need a
  fresh authentication. ADR 103 decision 2 asks for one only at the
  confirmation of an agent job in approve-first mode.

## Decision

1. **A built-in `planner` role.** `planner` is a data role. It holds each
   action of `viewer`, and `unit:run-plan` and `job:read-plans` (ADR 103
   decision 8). So a planner can start a runner job that only plans, and
   can read the masked plan. It cannot apply, destroy, approve or read a
   secret value. A plan job that a planner starts gets the secrets of the
   unit only when its commit is on a protected branch (ADR 213 decision
   2). ADR 197 decision 3 also gives it `changeorder:author`,
   because it has `unit:run-plan`.

   **The caveat for a workstation.** The role limits what the server runs
   and records. A planner who runs `tofu plan` or `pulumi preview` on a
   workstation reads state with the state actions that the role gives, as
   `viewer` does. That plan is not a job and the server keeps no record of
   it (ADR 152 decision 12). A person who also holds cloud credentials can
   change the cloud from a workstation. The role does not prevent that. The
   role page in the docs says so. The role page also says that a plan of a
   planner on a branch that is not protected gets no secrets.

   Benchmark: HCP Terraform has a fixed "plan" permission set on a
   workspace, between "read" and "write". It separates a plan from an
   apply.
2. **SMTP settings are installation settings.** The relay, the sender and
   the credentials of SMTP are installation settings with the precedence of
   ADR 63 decision 1. The password is a sensitive setting (ADR 63 decision
   6). It comes only from an environment variable or from a file that a
   `*_FILE` key names (ADR 63 decision 4). It never comes from a flag or
   from the database. So the declaration of the SMTP password does not
   allow the database, and a `PATCH` of it answers 409 (decision 7).

   Benchmark: the `*_FILE` convention of container images for secrets.
   Designed against PCI DSS v4.0.1 requirement 8.6.2: no password of a
   system account in a script or in a configuration file.
3. **Fresh MFA for a change that reduces security, and for approvals.**

   **Settings.** A change of an installation setting or of a namespace
   setting through the API needs a fresh MFA step when it makes the
   installation or the namespace less secure. When one key of a `PATCH`
   reduces security, the whole `PATCH` needs the step. The step
   is the step of ADR 70: the window of ADR 56 for built-in MFA, and the
   window of ADR 71 for a console session from an SSO provider. With
   built-in MFA off and no SSO provider, ADR 56 decision 5 applies, and the
   change needs no step.

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
   - a longer list `protectedBranches` of a source policy,
   - "fresh MFA for every approval" of a namespace from on to off,
   - the three changes of ADR 70 decisions 16, 17 and 23.

   The declaration of each setting says which change reduces security, as
   it says whether the setting is sensitive. A test checks each entry of
   the list.

   **The static layer needs no step.** A value from a flag, an environment
   variable or the configuration file (ADR 63 decision 1) needs no MFA
   step. A person who can change these already controls the host or the
   deployment, and the server cannot ask them for a step. The reload or
   the start writes the audit entry of RFC 68 with the changed keys.

   **An access token gets a refusal.** An access token acts with no MFA
   step (ADR 46 decision 8), so it can never pass this step. A request
   with an access token for a change that reduces security answers 403
   with the error code `mfa.fresh-step-required`. The message names the
   console page that makes the change, for example "Sign in to the console
   and change this setting on Settings > Security". The request changes
   nothing. A change that does not reduce security works with an access
   token, as before.

   **Approvals (owner decision 2026-10-18).** An approval of a gate of a
   `destroy` phase (ADR 103 decision 2) needs a fresh MFA step, as
   for a setting above. A namespace setting, "fresh MFA for every
   approval", off by default, makes every approval in the namespace need
   the step. The step is the step of the confirmation of ADR 103 decision 2:
   a new MFA step where built-in MFA covers the person, or else a new
   authentication at the SSO identity provider, at most 15 minutes old.
   The approval records the kind and the time of the step. With built-in
   MFA off and no SSO provider, ADR 56 decision 5 applies, and the
   approval needs no step.

   An approval through the API stays possible. A client passes the step
   with a session from the sign-in flow of the API, with the MFA step or
   the SSO authentication in it. An access token alone gets the refusal
   above, with the console page of the approval in the message.

   Benchmark: Google asks for a new sign-in before a change of security
   settings of an account. GitHub asks for "sudo mode", a new
   authentication, before a sensitive action, and a personal access token
   cannot do some of those actions. Designed against NIST SP 800-53 Rev. 5
   IA-11 (authenticate again for privileged functions).
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

   Benchmark: AWS Elastic Load Balancing appends the client address at the
   right end of `X-Forwarded-For`. The Google Cloud external Application
   Load Balancer appends two entries, `<client-ip>,<load-balancer-ip>`, so
   the rightmost entry is the address of the load balancer, not of the
   client. An operator behind it adds the load balancer address to
   `--trusted-proxies`, and the walk from the right then takes the client
   address. The security model page gives both setups.
5. **A restore writes three audit entries.** When the audit log is on
   (ADR 60 decision 2), a restore writes these audit entries on the object
   `@server`. Each names the principal, the backup ID
   and the SHA-256 of the backup manifest.
   - **Start.** Before the server changes any data, it writes an entry of
     the restore start. A sink of ADR 60 receives it, so the record of the
     start leaves the installation before the restore replaces the log.
   - **End.** After the restored data is in place, the server writes the
     entry of the restore end in the restored log. So a verifier can tell a
     restore from a rewrite of the log.
   - **Failure.** A restore that fails writes an entry of the failure with
     the error code, in the log that is in place after the failure.

   Benchmark: designed against SOC 2 CC7.2 and ISO/IEC 27001:2022 Annex A
   8.15 (logging of events that change the system, also when they fail).
6. **The branding preset is an installation setting, in the public sign-in
   answer.** The preset of ADR 180 decision 11 is the installation setting
   `branding.preset`, with the precedence of ADR 63 decision 1. A person
   changes it through the settings resource of decision 7, with the action
   `installation:manage-settings` (ADR 210 decision 7). Each change writes
   an audit entry.

   The sign-in page shows the preset before a person signs in. So the
   public answer of the sign-in page, `GET /api/dustpan/v1/sign-in`, holds
   it next to the list of sign-in providers (the ID and the name of each, no
   other part):

   ```json
   {
     "providers": [{ "id": "corp-sso", "displayName": "Corp SSO" }],
     "brandingPreset": "high-contrast"
   }
   ```

   The answer holds the preset only, no other setting and no secret. It
   has the header `Cache-Control: public, max-age=300`. The route is on the
   public list of the route test.

   Benchmark: Microsoft Entra ID keeps company branding of the sign-in page
   as one setting of the tenant, which an administrator sets through the
   Microsoft Graph API, and serves it to the sign-in page before the
   sign-in.
7. **One settings API (owner decision 2026-10-18).** The installation has
   one settings resource, and each namespace has one:
   - `GET /api/dustpan/v1/settings` and
     `GET /api/dustpan/v1/namespaces/{namespace}/settings` answer each
     declared setting of the scope with its effective value and its source,
     as RFC 68 ("API shape") proposes.
   - `PATCH` on the same path changes one or more database values with JSON
     Merge Patch (ADR 210 decision 10). All of them change, or none do. A
     `null` deletes the database value, and the effective value goes to
     the default.
   - The resource has one revision. The revision changes at each change
     of a database value, and at each reload or start that changes an
     effective value or a source. The `ETag` is the revision.
     Each answer holds its `ETag`. A `PATCH` needs `If-Match`. A stale
     revision answers 412 and changes nothing.
   - A key that the static layer sets is locked: a `PATCH` that sets it
     answers 409 with the key and its source, and changes no key. A `null`
     for a locked key deletes the database value that the static value
     shadows. The answer shows such a database value with
     `shadowed: true`.
   - The actions are `installation:read-settings` and
     `installation:manage-settings` on `@server`, and
     `namespace:read-settings` and `namespace:manage-settings` on the
     namespace.
   - A secret setting answers `"secret": true` and whether it is set, never
     its value. A `PATCH` can set or replace only a secret setting whose
     declaration allows the database, and never reads it. A `PATCH` of a
     secret setting that only the static layer can set, for example the
     SMTP password, answers 409.

   The routes of one policy become views of the resource:
   `/api/dustpan/v1/settings/session-policy`,
   `/api/dustpan/v1/settings/token-policy` and
   `/api/dustpan/v1/namespaces/{namespace}/settings/ip-allowlist`. A view
   answers the keys of its group, with the same revision, `ETag`, `PATCH`
   and lock rules. A view adds no setting and no rule of its own. A change
   through a view is a change of the resource.

   Benchmark: Google Cloud Organization Policy and AWS Systems Manager
   Parameter Store keep settings as one resource with versions, and
   Kubernetes keeps a `resourceVersion` on each object for optimistic
   concurrency.

## Not decided here

- The names of the SMTP settings, and whether email needs TLS.
- The rest of the white-label mechanism of RFC 9: logos, names and custom
  presets.

## Consequences

- `pkg/access` gets the `planner` role. The role page in the docs gives the
  caveat of decision 1, and says that a plan of a planner on a branch that
  is not protected gets no secrets.
- The settings declarations get the field for decision 3. The settings API
  answers a change that needs a fresh MFA step as ADR 71 does: 401 with the
  path of a new sign-in, or the MFA prompt of ADR 56. An access token gets
  the 403 of decision 3. The error code `mfa.fresh-step-required` gets its
  entry in the registry of ADR 182.
- The gate code of ADR 103 checks the step for a `destroy` approval and for
  each approval in a namespace with the switch on. The `jobtest` suite gets
  a case for each.
- The restore code writes the three entries of decision 5. The audit guide says how
  a verifier finds it.
- `docs/explanation/security-model.md` describes the proxy rule of decision
  4 as the decided rule.
- The settings routes of RFC 68 and the policy routes move to the one
  settings resource of decision 7. The console change merges before the
  next server tag (ADR 73).
- The sign-in page reads `GET /api/dustpan/v1/sign-in` for its providers
  and its preset.

## Changelog

- 2026-11-13, wording refinement (ADR 365): decision 5 now starts "When the
  audit log is on (ADR 60 decision 2)". ADR 60 decision 2 already said that
  the server writes no entry when the switch is off.
