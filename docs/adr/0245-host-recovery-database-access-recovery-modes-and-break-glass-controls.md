# 245. Host recovery: database access, recovery modes and break-glass controls

Status: accepted · 2026-10-19. Supersedes in part ADR 39 decisions 1 and 4, for the command `host create-temp-admin` only. Refines ADR 57 decision 2 and ADR 70 decisions 26 and 27. Changed by [ADR 295](0295-break-glass-recovery-mode-2-over-the-network.md)

## Context

ADR 70 decision 27 says that a self-hosted operator recovers with a host
token. The token makes a temporary administrator for one named person, with
a fixed expiry, and the path works without the identity provider, without
DNS and without the public listener. The command
`opendustpan host create-temp-admin` does this.

A security review of the command found these problems:

- The command opens the database. ADR 39 decisions 1 and 4 say that only
  `serve` opens the database, and that a test fails when another command
  opens a store.
- ADR 57 gives three recovery modes. Mode 3 is "no break-glass": the
  operator restores from a backup. The command ran in each mode.
- ADR 70 decision 26 asks for a reason, an audit entry and a review after
  each break-glass use. The command asked for no reason and opened no
  review.
- A temporary administrator could sign in to the console through the
  identity provider, when the provider sent its subject.
- The person, the grant, the record and the token were four transactions.
  A crash between two of them could leave a site administrator with no
  record, so no expiry ended it.
- Two concurrent first-token issues for one person could both pass the
  check that the person holds no token.
- The decorator found the record by subject. A new person with the subject
  of a deleted temporary administrator found the old record, and the
  decorator denied the new person.

A second security review of the change found more problems:

- The decision and the help of `--reason` said that a review opens at the
  expiry, but no store keeps break-glass reviews yet, so no review opens.
- The command applied pending migrations, so a recovery run could change
  the schema of a database that a server of another version uses.
- The expiry ended only the site-administrator grant and the tokens with
  the name `host-recovery`. A temporary administrator could give itself
  another grant, join a group or make a second token, and keep that access
  after the expiry.
- The port had a method that issued the first token on its own. No
  production code called it, and it was one more exception to the
  own-token rule.

A decision of the product manager has the label "(product manager)" and a
"Benchmark:" line. The product manager decided on behalf of the project
owner. The other decisions are the fixes of the review.

## Decision

### Decisions of the product manager

1. **The host command opens the database (product manager).** The command
   `host create-temp-admin` opens the database that `--database` names, as
   `serve` does. This supersedes ADR 39 decisions 1 and 4 in part, for
   this command only. Every other command stays a client of the REST API.
   The reason: shell access to the host already controls the
   key-encryption key and the database, so the command gives the operator
   no access that the operator does not have.

   Benchmark: Keycloak `kc.sh bootstrap-admin`, and HashiCorp Vault
   `operator generate-root`. Both recover an administrator from the host,
   with no running administrator account.
2. **The command respects the recovery mode (product manager).** The
   command refuses when the installation chose mode 3 of ADR 57 ("no
   break-glass"). The error names the mode and the setting to change. The
   command runs in mode 1 and in mode 2. The reason: the operator chose no
   break-glass explicitly, and the command must not override that choice.
   To use the command, the operator changes the mode.

   Benchmark: the operator choice of ADR 57 decision 2. A product that
   lets an operator turn off a recovery path keeps it off, for example
   Vault with no unseal or recovery keys outside its seal.
3. **The break-glass controls apply (product manager).** The controls of
   ADR 70 decision 26 apply to each temporary administrator:

   - The command needs a reason: `--reason`, or a line that the operator
     types at a prompt on a terminal. With no reason, the command refuses.
   - The audit entry of the create of the record holds the reason.
   - The second-person review of its audit entries (`pkg/breakglass`, ADR
     142, with the kind `temp-admin`) opens only when `pkg/breakglass` has
     a store adapter. Until then no review opens: see "Not decided here".
     The command writes one line to standard error that says so, and the
     server writes one warning when it ends a record with no review.

   A temporary administrator is not a "break-glass account" of ADR 70
   decisions 23 to 25: its principal has no `break_glass` flag, it is not
   one of the two accounts that the guard counts, and it does not sign in
   with a passkey. It is a site administrator with a fixed expiry, so the
   access limits and the gates that apply to a site administrator apply to
   it too.

   Benchmark: PCI DSS v4.0.1 requirement 8.2.2 (a documented reason for
   emergency access, and each action attributable to a person) and NIST SP
   800-53 Rev. 5 AC-2(2) (an emergency account ends at a set time).
4. **No sign-in through an identity provider (product manager).** A
   temporary administrator cannot start a console session through an
   identity provider. `SignInPrincipal` refuses it with
   `ErrNotSignInTarget`. It uses only the token that the command printed.

   Benchmark: the temporary administrator of Keycloak
   `kc.sh bootstrap-admin` is a local account only.

### Fixes of the review

5. **The off switch fails closed.** `DUSTPAN_DISABLE_HOST_RECOVERY` turns
   the command off with `true` or `1`. `false`, `0`, an empty value and no
   value keep it on. The command refuses any other value, so a typo never
   turns the command on. The command reads the switch and the mode before
   it opens the database.
6. **The recovery mode setting.** No setting held the mode of ADR 57 yet.
   The setting `--recovery-mode` (`DUSTPAN_RECOVERY_MODE`) holds it:
   `bootstrap` (mode 1, the default), `signed-request` (mode 2) or `none`
   (mode 3). The command refuses `none` and an unknown value.
   `--recovery-mode` must match the first-boot choice of ADR 57. When that
   choice is stored, a later change makes the stored choice win over the
   setting. The command reads the variable through the same environment
   lookup as the off switch.
7. **One transaction.** The command makes the person, its
   site-administrator grant on the installation, its record and its token
   in one store transaction, with the four audit entries. A crash leaves
   all of them or none.
8. **The store checks the first token.** In the transaction of the
   recovery, the store inserts the token only when all of these hold at the
   time of the token. The principal is a live, enabled person of the
   installation, made at most five minutes before and not after. A record
   names the principal and works past the time and to the expiry of the
   token. The principal holds no token of any status. Else the store
   returns `ErrNotOwnToken`. Of two concurrent recoveries for one person, at
   most one succeeds. SQLite holds the write lock of the database for the
   transaction. PostgreSQL pins READ COMMITTED and takes the advisory lock
   of the server audit scope first, so the second recovery reads the rows
   of the first in a new statement snapshot.
9. **The record belongs to one principal.** The decorator compares the
   principal of the record with the principal of the caller. The delete of
   a principal deletes its record in the same transaction. A new person
   with the subject of a deleted temporary administrator is not locked out.
10. **One record lifetime.** A record and its token end at the same time:
    24 hours after the create, or the maximum of the token policy when
    that is shorter.
11. **The domain package has no HTTP code.** `pkg/tempadmin` is in the
    depguard list of domain packages. The HTTP adapter reads the client
    address and the request ID and gives them to the decorator.
12. **Only the host command reaches the host issuer.** A test lists each
    package of the module with `go list` and fails when a package other
    than `cmd/opendustpan` names the host issuer. It also fails when a
    package other than `pkg/accesstoken` and its conformance suite calls
    `CreateHostRecovery`.

### Fixes of the second review

13. **The expiry ends all access of the principal (product manager).** In
    one transaction, the expiry marks the record ended, disables the
    principal, ends every grant that the principal holds and every grant on
    it, ends every group membership of the principal and ends every live
    token of the principal, whatever its name. An operator then reads the
    audit entries of the principal from the create to the expiry, and
    checks the principals, the grants and the tokens that it made for
    others: the expiry does not end those.

    Benchmark: ADR 70. The access of a break-glass account ends fully at
    the end of its window.
14. **The host command applies no migration (product manager).** The
    command opens the store without migrating. When the database has a
    pending migration, it refuses and names the command that applies them:
    `opendustpan serve` of the same binary. The server stays the one
    process that changes the schema.

    Benchmark: the administration tools of Keycloak and GitLab refuse a
    schema that does not match their version.
15. **The port issues no token on its own.** `HostStore` keeps only
    `CreateHostRecovery`. The change deletes `IssueFirstToken` and
    `CreateFirstHostToken`: no production code called them, and each was
    one more exception to the own-token rule.
16. **The record keeps its end.** The record has an end time. The sweep
    skips a record that ended, and a failed expiry of one record does not
    stop the sweep: the server logs the principal, with no driver text, and
    continues with the next record. The decorator logs a failed expiry in
    the same way. The expiry entries of a sweep that the host command runs
    have the subject `@host`. The expiry entries of the server have the
    subject `@server`.
17. **No hidden characters.** The reason and the creator refuse control
    characters (Unicode Cc) and format characters (Unicode Cf), such as a
    direction override, so the text in the audit log shows what the operator typed.
18. **The banner follows the principal ID.** The API marks a principal as
    a temporary administrator by the principal ID of the record, not by
    its subject.
19. **The window uses the process clock.** The window of the first token
    and the expiry use the clock of the process that writes. A clock skew
    between instances moves the expiry by the skew. The project accepts this.

## Not decided here

- The store of break-glass reviews. `pkg/breakglass` has no store adapter
  in either dialect yet. The server opens the review of decision 3 when
  that adapter exists, for each record that ends, by the sweep or by a use
  after the expiry. Until then, the expiry writes its audit entry and
  opens no review.
- A stored recovery mode. No store keeps the mode yet, and `serve` does not
  read `--recovery-mode`. The host command reads only its own flag and
  environment, so the mode `none` holds only where the operator account sets
  `DUSTPAN_RECOVERY_MODE=none`.

## Consequences

- The client gate test of ADR 39 decision 4 allows `host
  create-temp-admin` to open a store. The command opens it only after the
  off switch and the mode allow it, and a test proves that.
- The host recovery guide names the reason, the modes, the off switch, the
  four audit entries of a create, the refusal of a pending migration, and
  what the expiry ends and what an operator checks after it.
- An expired temporary administrator keeps its name: the principal stays,
  disabled, until a site administrator deletes it.
