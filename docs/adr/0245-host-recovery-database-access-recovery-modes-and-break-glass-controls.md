# 245. Host recovery: database access, recovery modes and break-glass controls

Status: accepted · 2026-10-19. Supersedes in part ADR 39 decisions 1 and 4, for the command `host create-temp-admin` only. Refines ADR 57 decision 2 and ADR 70 decisions 26 and 27

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
   - At the expiry or the end of the temporary administrator, a
     second-person review of its audit entries opens (`pkg/breakglass`,
     ADR 142), with the kind `temp-admin`.

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
7. **One transaction.** The command makes the person, its
   site-administrator grant on the installation, its record and its token
   in one store transaction, with the four audit entries. A crash leaves
   all of them or none.
8. **The store checks the first token.** The store inserts the first token
   only when all of these hold at the time of the token. The principal is a
   live, enabled person of the installation, made at most five minutes
   before and not after. A record names the principal and works past the
   time and to the expiry of the token. The principal holds no token of any
   status. Else the store returns `ErrNotOwnToken`. Of two concurrent issues
   for one person, at most one succeeds.
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
    than `cmd/opendustpan` names the host issuer.

## Not decided here

- The store of break-glass reviews. `pkg/breakglass` has no store adapter
  in either dialect yet. The server opens the review of decision 3 when
  that adapter exists. Until then, the expiry writes its audit entry and
  opens no review.
- Whether the server also reads `DUSTPAN_RECOVERY_MODE`, and how the mode
  where the server keeps the mode that the operator chose at first boot
  (ADR 57, "Not decided here").

## Consequences

- The client gate test of ADR 39 decision 4 allows `host
  create-temp-admin` to open a store. The command opens it only after the
  off switch and the mode allow it, and a test proves that.
- The host recovery guide names the reason, the modes, the off switch and
  the four audit entries of a create.
- An expired temporary administrator keeps its name: the principal stays,
  disabled, until a site administrator deletes it.
