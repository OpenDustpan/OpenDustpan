## Why

**Persona: a site administrator.** They run a company directory (OpenLDAP or
Active Directory). They try to let their people sign in to the console with
the user name and password they already have, with no second set of passwords.

Today they cannot. The LDAP method (`internal/ldapauth`), the sign-in service
(`internal/signinsvc`, `CompletePassword`) and the setting that allows plain
connections (`egress.allowPlainConnections`) are built. No server path reaches
them. `cmd/opendustpan` builds no LDAP method, no static provider setting exists, and no route takes a user name and
a password for a provider. The guide `docs/guides/ldap-sign-in.md` says so in its
second paragraph. A built method that no route reaches is not delivered.

The user path this change must make work:

1. **Start the server.** The site administrator sets the provider with flags,
   variables or file keys, and the bind password in `DUSTPAN_LDAP_BIND_PASSWORD`.
   `GET /api/server/sign-in-providers` shows each effective setting and its
   source. An invalid setting stops the start and names the setting. A plain `ldap://` URL to a host that is not a loopback address
   is refused with the address, the setting `egress.allowPlainConnections` and
   the one `PATCH` call that changes it (see the guide).
2. **API.** The person sends a user name and a password for the provider. A
   right password gives a console session (`204` and the cookie). Every refusal gives
   one identical answer, `401 sign-in.failed`.
3. **CLI.** `internal/cli` reads the settings (`--ldap-*`, `DUSTPAN_LDAP_*`). No
   new CLI command is in this change. Every feature has a REST API (ADR 17), and
   the API is the contract.
4. **Docs page.** `docs/guides/ldap-sign-in.md` gives the settings, the password
   variables, the "Network" section for private directories, the answers of the
   route, and says that groups are read and not applied. Each step works end to
   end against a test directory.
5. **Console.** The console shows the provider and the sign-in form. The console
   repository (`OpenDustpan/console`) changes in its own pull request. Before
   the next server tag, that change merges.

Errors the person may hit, and the help each gives:

| Error | What the answer says |
| --- | --- |
| Wrong user name or password, locked name, disabled person | One answer, `401 sign-in.failed`, as ADR 285 decision 13 and ADR 320 decision 11 require. The audit entry holds the real reason. |
| Plain connection, opt-in off | The address, the setting `egress.allowPlainConnections`, and the `PATCH` call that sets it. |
| Directory does not answer | `503 sign-in.unavailable`, and the text "the directory of the sign-in provider does not answer: try again later". `GET /api/server/sign-in-providers` shows the settings. |
| Bind password missing | The start fails and names `DUSTPAN_LDAP_BIND_PASSWORD` and `DUSTPAN_LDAP_BIND_PASSWORD_FILE`. |

## What Changes

This change delivers a **static** LDAP provider: one provider, set by flags,
`DUSTPAN_LDAP_*` variables or file keys `ldap.*`. The provider that an
administrator stores through the API is its own change (issue #2950).

- 23 settings for the `api` component: `--ldap-*`, `DUSTPAN_LDAP_*`, file keys
  `ldap.*`. An `--ldap-*` setting with no `--ldap-url` fails the start.
- The bind password comes only from `DUSTPAN_LDAP_BIND_PASSWORD` or from the file
  that `DUSTPAN_LDAP_BIND_PASSWORD_FILE` names. No flag and no file key takes it.
  The serve command clears the variable after it reads it.
- `dustpan.WithLDAPProvider` builds an `ldapauth.Method`. The method reads
  `egress.allowPlainConnections` at each sign-in.
- The route `POST /api/dustpan/v1/sign-in/providers/{provider}/password` (below).
- `GET /api/server/sign-in-providers` shows the provider with `source: static`,
  `mode: ldap`, an `ldap` object with the effective value and the source of each
  setting, and `bindPasswordSet`. It never shows the password.
- The setting `ldap.subjectFailureLimit` (ADR 320 decision 20), as static
  configuration with its effective value and its source shown. The counting
  itself is in `signinsvc` already (`BindGate`, merged).
- `--ldap-allowed-ranges` for a directory on a private network.
- E2E steps against a test LDAP server: a sign-in over TLS, and a plain
  connection that is refused, allowed with the opt-in on, and refused again with
  the opt-in off.
- Hostile tests for each layer: settings, connection, method, handler.
- `docs/guides/ldap-sign-in.md` tells the steps end to end and says that groups
  are read and not applied.

**The route, exactly as in PR #2968.** `POST
/api/dustpan/v1/sign-in/providers/{provider}/password` is public. The body is
JSON: `{"userName": "...", "password": "..."}` (the type `PasswordSignInRequest`).

| Answer | When |
| --- | --- |
| `204` and the session cookie | The directory accepts the user name and the password. |
| `401 sign-in.failed`, one identical body | Wrong password, unknown user, locked name or person, disabled person, unknown provider, an OIDC provider ID, and a request of another site. Each spends one unit of the password budget of the source address. |
| `400 request.body-invalid` | The body is not valid. It spends one unit. |
| `429` | The budget of the source address is full. The handler does not read the body, and no connection to the directory opens. |
| `503 sign-in.unavailable` | The directory does not answer. It spends nothing. |

**The check against login CSRF, as in PR #2981.** The route calls
`sessionauth.Checker.SignInFromBody` before the budget and before any store or
directory call. The media type must be `application/json`. An `Origin` header,
when present, must be an allowed console origin (`null` is not one). A
`Sec-Fetch-Site: cross-site` header refuses the request. A request with neither
header comes from a client that is not a browser, and passes with the JSON
media type.

This change depends on PR #2981 and PR #2968. Neither is on `main` yet.

Out of scope:
- **LDAP groups.** The method reads the groups and the server applies none. The
  guide says so. Applying them is its own change (issue #2951).
- The provider that an administrator stores through the API (issue #2950).
- The directory sync, the sync holds, and the unlock routes (ADR 321 decision 11,
  ADR 320 decision 21).
- Binding the provider ID to its URL (issue #2969).

## ADR check

Each quote is from the file in `docs/adr/`. "Owner" means the decision carries
"(owner)" or "(owner, ADR N)".

**ADR 6 (not owner-tagged).** Changed by: ADR 17, 52, 53, 237, 339. Current rule:
this ADR with those. Quote: "OpenDustpan always issues its own access tokens. SSO
adapters only authenticate humans in the browser so a token or session can be
minted. ... LDAP is a directory credential check, not single sign-on." No
conflict: the route issues a console session and no LDAP-specific token.

**ADR 55 (not owner-tagged).** Changed by: 70, 285, 297, 301, 320, 321. Decision
1 puts LDAP in the binary. Decisions 1 and 2 are changed by ADR 320, so the port
is `session.PasswordMethod`, not `identity.Authenticator`. No conflict.

**ADR 63 decision 4 (cited as owner by ADR 321 decision 8).** Quote: "Secrets
never come from a flag. A secret comes from an environment variable or from a file
that a `*_FILE` key names." The bind password follows it.

**ADR 70 (not owner-tagged; some decisions are owner).** Changed by: 142, 210,
216, 222, 227, 245, 306, 320, 321, 332, 354. Decision 12: "Each membership
records its source." Decision 22: "These rules close the side doors." Decision 25:
"Break-glass accounts are accounts of last resort." The sign-in never signs in a
break-glass account (ADR 320 decision 9). No conflict.

**ADR 216 decision 9 (owner, ADR 216).** Quote: "A sign-in authenticates only
principals of its own home." The LDAP provider has a home. `signinsvc` checks it.
No conflict. ADR 273 changes decision 8 only.

**ADR 227 and ADR 301 (not owner-tagged).** One fresh-authentication window. An
LDAP person follows ADR 285 decision 21 (below). No change in this change.

**ADR 285 decision 13 (PM decision under the owner's delegation).** Quote: "A
password sign-in answers `401` with one code and one text, \"The user name or the
password is wrong.\"" The route answers one identical `401 sign-in.failed`. PR
#2968 words the text as "sign-in failed: check the user name and the password".
The one code and the one answer match decision 13; the wording is by error code
(ADR 276). Decision 21 (owner, through RFC 21 P7):
"`mfa.requireForAll` makes each password sign-in ask for a second factor." Changed
by: 297, 301, 312, 320, 333. The route keeps the pending sign-in of ADR 297.

**ADR 292 (owner, all decisions).** Not changed by a later ADR. Quote: "The server
refuses an LDAP connection and an SMTP connection that has no TLS, except to a
loopback address. (owner)" and "An installation can allow plain connections with
one setting. The setting is off by default. (owner)" and "The hosted service does
not allow the setting. (owner)". The adapter reads the setting at the point of use.
This change adds a test that sends a plain connection through the route with the
setting off. No conflict.

**ADR 320 (PM, accepted with the merge).** Changed by: ADR 332 (decision 12).
Decisions 7, 11, 19, 24, 25 apply: one order of steps, one answer, counts on the
typed name and the subject, no lockout for other methods, a settings struct for
each mode. Decision 20 (the setting `ldap.subjectFailureLimit`, 5 by default,
1 to 100) is in this change, as static configuration. Decision 21 (unlock routes)
is not. Decision 25 says a mode has its settings in `ProviderConfig`: the stored
provider is a later change, so this change keeps the static provider outside
`ProviderConfig`, and the mode stays refused there. Decision 12 (group mapping)
is changed by ADR 332; no group is applied here, which is the rule "No mapping, no
change" of decision 13.

**ADR 321 decisions 2, 3, 4, 5, 7, 8, 9, 10 (decision 7 is owner, ADR 292;
decision 8 is owner, ADR 63).** Changed by: ADR 332 (decisions 19, 30, 33 and 38,
which are the SAML, OAuth 2.0 and header group decisions, not LDAP). Decisions 11
and 12 (sync and holds) are out of scope.

**ADR 332 (not owner-tagged).** Extends decisions 13, 14, 15 and 22 of ADR 321. It
matters for the LDAP groups only, and they are out of scope.

**ADR 17 and ADR 73.** Every feature has a REST API with declared actions. The
sign-in route is on the explicit public list. The tool-neutral APIs may break
before 1.0; the console change merges before the next tag.

No conflict with an owner-tagged ADR was found. The owner decides nothing before
this change can start.

## Open questions

none

Decided by the PM on 2026-11-08, with the ADR check done:

1. The route and the body are those of PR #2968, with the check of PR #2981.
2. The bind password comes only from `DUSTPAN_LDAP_BIND_PASSWORD` or
   `DUSTPAN_LDAP_BIND_PASSWORD_FILE` (ADR 321 decision 8, ADR 63 decision 4). It is
   never in a flag or a config file. The PM message named the variable with the
   provider ID as a suffix. This change follows PR #2968, which has one static
   provider and one variable, because a provider ID has hyphens that a variable
   name cannot hold. A suffix by ID comes with the stored provider (issue #2950).
3. `ldap.subjectFailureLimit` is in scope, as static configuration.
4. LDAP groups are out of scope.
5. The static provider comes first. The stored provider is its own change.

Source of this change: the file `orchestration/specs/ldap-sign-in.md` on the
branch `pm/spec-driven` of `OpenDustpan/product` could not be read (the read was
refused). This change is written from issue #2943, from PR #2968 and PR #2981, and
from the code on `main`.
