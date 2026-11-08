## Why

**Persona: a site administrator.** They run a company directory (OpenLDAP or
Active Directory). They try to let their people sign in to the console with
the user name and password they already have, with no second set of passwords.

Today they cannot. The LDAP method (`internal/ldapauth`), the sign-in service
(`internal/signinsvc`, `CompletePassword`) and the setting that allows plain
connections (`egress.allowPlainConnections`) are built. No server path reaches
them. `cmd/opendustpan` builds no LDAP method, a provider in the mode `ldap` is
refused by `session.ProviderConfig.Validate`, and no route takes a user name and
a password for a provider. The guide `docs/guides/ldap-sign-in.md` says so in its
second paragraph. A built method that no route reaches is not delivered.

The user path this change must make work:

1. **API.** The site administrator adds a provider in the mode `ldap` with the
   settings of the guide. The answer shows each default and its effective value.
   An invalid setting is refused with the name of the setting and the one call
   that fixes it. A plain `ldap://` URL to a host that is not a loopback address
   is refused with the address, the setting `egress.allowPlainConnections` and
   the one `PATCH` call that changes it.
2. **API.** The person sends a user name and a password for the provider. A
   right password gives a console session. Every failure gives one answer, "The
   user name or the password is wrong.", with the status `401`.
3. **CLI.** `internal/cli` is the client of the REST API. Whether a CLI command for
   sign-in providers exists must be checked before the tasks start. Every feature has a
   REST API (ADR 17), so the API is the contract; a CLI command is added only if
   one for providers exists today.
4. **Docs page.** `docs/guides/ldap-sign-in.md` loses its sentence "The routes
   that add an LDAP provider are not in the server yet", and each step of the
   page works end to end against a test directory.
5. **Console.** The console shows the provider and the sign-in form. The console
   repository (`OpenDustpan/console`) changes in its own pull request. Before
   the next server tag, that change merges.

Errors the person may hit, and the help each gives:

| Error | What the answer says |
| --- | --- |
| Wrong user name or password, locked name, disabled person | One answer, `401`, the text of ADR 285 decision 13. The audit entry holds the real reason. |
| Plain connection, opt-in off | The address, the setting `egress.allowPlainConnections`, and the `PATCH` call that sets it. |
| Directory does not answer | `503`, the provider ID, and `GET /api/server/sign-in-providers/{id}` to read the settings. |
| Bind password missing | The provider ID and the name of the environment variable or file that must hold it. |

## What Changes

- `session.ProviderConfig` gets the settings of the mode `ldap`, validated with an
  allowlist at the boundary. `Validate` stops refusing the mode.
- `cmd/opendustpan` builds one `ldapauth.Method` for each enabled provider of the
  mode `ldap` and gives the set to `signinsvc`. It builds it again when a provider
  changes.
- A route takes a user name and a password for a provider, calls
  `signinsvc.CompletePassword` and issues the session cookies.
- The plain-connection setting is read on each connection, at the point of use.
  This part exists in `internal/ldapauth`; this change keeps it and tests it
  through the route.
- E2E steps against a test LDAP server: one sign-in over TLS, one over a plain
  connection with the opt-in on, and a refused plain connection with the opt-in
  off.
- Hostile tests for each layer: settings, connection, method, handler.
- The guide is updated, and the API description and the clients are regenerated.

Out of scope: the directory sync, the sync holds, the unlock routes
(ADR 321 decision 11, ADR 320 decision 21) and the LDAP lockout setting
`ldap.subjectFailureLimit`, unless "Open questions" says otherwise.

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
that a `*_FILE` key names." The bind password follows it. See open question 2.

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
password is wrong.\"" The route uses it. Decision 21 (owner, through RFC 21 P7):
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
each mode. Decision 20 (the setting `ldap.subjectFailureLimit`) and decision 21
(unlock routes) are not in this change. See open question 3. Decision 12 (group
mapping) is changed by ADR 332. See open question 4.

**ADR 321 decisions 2, 3, 4, 5, 7, 8, 9, 10 (decision 7 is owner, ADR 292;
decision 8 is owner, ADR 63).** Changed by: ADR 332 (decisions 19, 30, 33 and 38,
which are the SAML, OAuth 2.0 and header group decisions, not LDAP). Decisions 11
and 12 (sync and holds) are out of scope.

**ADR 332 (not owner-tagged).** Extends decisions 13, 14, 15 and 22 of ADR 321. It
only matters for the LDAP groups (open question 4).

**ADR 17 and ADR 73.** Every feature has a REST API with declared actions. The
sign-in route is on the explicit public list. The tool-neutral APIs may break
before 1.0; the console change merges before the next tag.

No conflict with an owner-tagged ADR was found. The owner decides nothing before
this change can start; open questions 1 to 4 are for the PM.

## Open questions

The change may not be implemented until each question has an answer in this
section and this section reads "none".

1. **The route.** What is the path and the body of the LDAP password sign-in?
   ADR 285 gives `POST /api/dustpan/v1/sign-in/password` for local users. ADR 320
   and ADR 321 do not give a route for a password method of a provider.
   Decided by: the PM. Not owner-tagged.
2. **The bind password source.** How does a provider name the environment
   variable or the `*_FILE` key that holds its bind password? ADR 321 decision 8
   and ADR 63 decision 4 require the source, and no ADR names the key.
   Decided by: the PM.
3. **The lockout setting.** Does `ldap.subjectFailureLimit` (ADR 320 decision 20)
   land in this change? The code has no such setting. `CompletePassword` needs a
   limit to lock a subject. Decided by: the PM.
4. **Groups.** Does this change apply LDAP groups? The default is `off`. ADR 332
   changes the group mapping rule, and `ApplyGroupClaims` exists. If groups are
   in scope, the mapping form must be accepted. Decided by: the PM, and the owner
   if the mapping form is not accepted.

Source of this change: the file `orchestration/specs/ldap-sign-in.md` on the
branch `pm/spec-driven` of `OpenDustpan/product` could not be read (the read was
refused). This change is written from issue #2943 and from the code on `main`.
Compare it with that file before the gate is closed.
