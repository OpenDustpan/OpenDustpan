---
version: 1
---

Issue: #2172.

## Problem

A developer who signs in with an ECDSA, sk-ECDSA, or RSA key tries to complete a step-up, but internal/sshsig verifies only ed25519 keys and refuses each other key with stepup.ErrInvalid, so the step-up fails for those persons.

## Evidence

- internal/sshsig exists and refuses key types outside its allowlist with stepup.ErrInvalid. Proof: internal/sshsig/verifier.go:69: return PublicKey{}, fmt.Errorf("%w: SSH key type %q not allowed", stepup.ErrInvalid, printable(k.Type))
- internal/sshsig documents that only some factor, type, or key values are allowed. Proof: internal/sshsig/verifier.go:58:// stepup.ErrInvalid for a factor, a type or a key outside the allowlist.
- The vector generator path exists for new ssh-keygen vectors. Proof: internal/sshsig/testdata/gen/generate.sh
- The stepup verifier suite defines a SmallRSA vector for an RSA key below 3072 bits. Proof: internal/stepup/stepuptest/verifier.go:41: // SmallRSA is a valid signature by an RSA key below 3072 bits.

## Fix boundary

- internal/sshsig verifies ecdsa-sha2-nistp256, sk-ecdsa-sha2-nistp256 with UP required and only UP and UV allowed, and ssh-rsa keys of 3072 to 8192 bits with rsa-sha2-256 or rsa-sha2-512.
- Short RSA keys, ssh-rsa SHA-1 signatures, oversize RSA keys or exponents, and each type outside the allowlist stay refused with stepup.ErrInvalid.
- Test vectors from real ssh-keygen, hostile-input tables per layer, extended fuzz seeds, and stepuptest SmallRSA refusal cover the change.

Out of scope:

- New step-up methods beyond the five listed key types.
- Console or docs changes beyond the key-type list.
- Changes to stepup grant or session behavior.

## Check

- Add failing ECDSA P-256 vectors and hostile-input table in NEW file internal/sshsig/verify_ecdsa_test.go. Check: `go test ./internal/sshsig/ -run TestVerifyECDSA -count=1` fails on main, passes after.
- Add failing sk-ECDSA vectors and flag-rule hostile-input table in NEW file internal/sshsig/verify_skecdsa_test.go. Check: `go test ./internal/sshsig/ -run TestVerifySkECDSA -count=1` fails on main, passes after.
- Add failing RSA vectors and hostile-input table for signature type and short modulus in NEW file internal/sshsig/verify_rsa_test.go. Check: `go test ./internal/sshsig/ -run TestVerifyRSA -count=1` fails on main, passes after.
- Implement ECDSA, sk-ECDSA, and RSA verification with size and exponent bounds in internal/sshsig/verifier.go:63, with wire limits and size checks before any RSA verify call. Check: `go test ./internal/sshsig/... ./internal/stepup/... -count=1` passes, including stepuptest SmallRSA refusal.
- Extend testdata generator internal/sshsig/testdata/gen/generate.sh with real ssh-keygen vectors and skdummy.c ECDSA support. Check: `ls internal/sshsig/testdata` holds the NEW ECDSA, sk-ECDSA, and RSA vectors made by generate.sh.
- Add the NEW ECDSA, sk-ECDSA, and RSA vectors as seeds to the fuzz test in NEW file internal/sshsig/verify_fuzz_test.go. Check: `go test ./internal/sshsig/ -run FuzzVerify -count=1` passes with the new seeds.
- Measure one 8192-bit RSA Verify within the 500 ms quality target in NEW file internal/sshsig/verify_rsa_cost_test.go. Check: `go test ./internal/sshsig/ -run TestVerifyMaxRSA -count=1` passes within 500 ms.
- Run affected suites and the stepuptest verifier suite in internal/stepup/stepuptest/verifier.go:41. Check: `go test ./internal/sshsig/... ./internal/stepup/... -count=1` passes.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- internal/sshsig verifies only ssh-ed25519 and sk-ssh-ed25519 and refuses each other type fail closed with stepup.ErrInvalid. (`internal/sshsig/verifier.go:69 (func checkKey)`)
- stepup.ErrInvalid is the sentinel for an invalid step-up value. (`internal/stepup/stepup.go:78 (var ErrInvalid)`)
- The stepup verifier suite carries a SmallRSA vector for an RSA key below 3072 bits. (`internal/stepup/stepuptest/verifier.go:42 (var SmallRSA)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which SSH key and signature types must internal/sshsig accept? The issue lists the full allowlist: ssh-ed25519, sk-ssh-ed25519, ecdsa-sha2-nistp256, sk-ecdsa-sha2-nistp256, and ssh-rsa of 3072 bits or more with rsa-sha2-256 or rsa-sha2-512 only. Each other type stays refused with stepup.ErrInvalid.
- Does the change need a new dependency? No new dependency. The issue says standard library only: crypto/ecdsa, crypto/rsa, crypto/sha256.
- What proves short RSA keys still fail and what are the RSA bounds? The pack names internal/stepup/stepuptest/verifier.go SmallRSA vector. The suite must keep refusing SmallRSA with ErrInvalid. Fixed bounds: RSA modulus 3072 to 8192 bits, exponent odd with 3 <= e <= 4294967295, checked before any RSA verify call.
- Does any ADR decide the step-up key allowlist? No ADR or RFC in the pack names a step-up key allowlist. The pack adrs and rfcs lists are empty, so there is no decision text to quote. The allowlist comes from the issue text alone.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
