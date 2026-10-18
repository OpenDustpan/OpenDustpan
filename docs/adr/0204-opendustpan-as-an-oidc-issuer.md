# 204. OpenDustpan as an OIDC issuer

Status: accepted · 2026-10-18 · part of ADR 200

## Context

The project owner decided on 18 October 2026 that OpenDustpan becomes an
OIDC issuer, with a discovery document, a JWKS, key storage and rotation,
an audience for each target and short lifetimes (owner). The login
providers, the generic OIDC token and the external adapter all use it
(owner).

ADR 54 is the other direction: the server trusts the tokens of other
issuers. ADR 133 and ADR 164 each ask, in an open question, for a token
issuer in the server.

The research note `docs/research/profiles-esc-parity-and-oidc-claims.md`
gives what the relying parties need:

- Azure accepts only `RS256`. AWS, Google Cloud, Vault and Keycloak accept
  it too. Google Cloud also accepts `ES256`, and Snowflake refuses the `PS`
  algorithms.
- AWS reads the JWKS from the URL of the issuer. It allows at most 100 RSA
  keys, and needs no thumbprint when the host has a certificate from a
  public authority. Google Cloud can also take an uploaded JWKS of at most
  8 keys. Vault and Kubernetes can take fixed keys.
- Snowflake caches keys for up to 60 minutes. Each relying party caches the
  JWKS for a time of its own, so a new key must be in the JWKS before the
  first token that it signs.
- The discovery document needs `issuer`, `jwks_uri`,
  `response_types_supported`, `subject_types_supported` and
  `id_token_signing_alg_values_supported`.
- Google Cloud refuses a token whose `exp - iat` is more than 24 hours. The
  other products of the research issue tokens for 5 to 60 minutes.

## Decision

1. **One issuer for each namespace (one-way door).** The issuer of a
   namespace is `https://<issuer host>/oidc/<namespace ID>`, for example
   `https://dustpan.example.com/oidc/ns-k3v9q2m7x4t8p1w6z5r0`. Each namespace
   has its own signing keys.
   - The path holds the stable ID of the namespace, not its name. A rename
     of the namespace keeps the issuer. A namespace that is purged and made
     again with the same name gets a new ID, so it never gets the cloud
     trust of the old namespace.
   - A trust policy that names the issuer of one namespace can never match
     a token of another namespace, also when its `sub` pattern is too wide.
     This matters most on a shared installation, for example OpenDustpan
     Cloud.
   - A namespace can rotate or revoke its keys with no effect on another
     namespace.
   - The issuer host comes from the installation setting `issuerBaseURL`.
     When it is empty, the server uses the installation setting
     `publicURL`. An operator can set `issuerBaseURL` to a host that stays
     the same when the API host changes, for example
     `https://issuer.example.com`. A server with neither setting issues no
     token, and an open of a login block fails with
     `profile.ErrIssuerNotConfigured`, which names the two settings.
   - When a namespace sets `issuerURL` (decision 3), each token that it
     signs carries that URL as `iss`, and the discovery document of
     decision 2 answers it as `issuer`. Else `iss` is the URL of this
     decision.

   **One-way door:** clouds hold the issuer URL in their trust
   configuration. A new form of the URL breaks each trust. The issuer host
   is part of this door: a change of `issuerBaseURL`, or of `publicURL`
   when `issuerBaseURL` is empty, breaks each cloud trust of each
   namespace that has no `issuerURL`. A namespace that moves to another
   installation or region keeps its trust only when the new installation
   answers the same issuer URL, or when the namespace sets `issuerURL`. The
   console shows this before a change of either setting.
2. **The discovery document.** `GET /oidc/{nsid}/.well-known/openid-configuration`
   answers:

   ```json
   {
     "issuer": "https://dustpan.example.com/oidc/ns-k3v9q2m7x4t8p1w6z5r0",
     "jwks_uri": "https://dustpan.example.com/oidc/ns-k3v9q2m7x4t8p1w6z5r0/jwks",
     "response_types_supported": ["id_token"],
     "subject_types_supported": ["public"],
     "id_token_signing_alg_values_supported": ["RS256"],
     "scopes_supported": ["openid"],
     "claims_supported": ["sub", "aud", "exp", "iat", "iss", "jti", "nbf", "namespace", "..."]
   }
   ```

   `claims_supported` lists each claim of ADR 205. `GET /oidc/{nsid}/jwks`
   answers the public keys of decision 4. Both routes need no sign-in, are
   on the public list of the route test, and send `Cache-Control:
   max-age=300`. An unknown namespace ID answers `404`. A namespace with no
   key yet answers its discovery document and an empty JWKS.
3. **The issuer can live at another URL.** A homelab server is often not
   reachable from the internet, and AWS and Azure must read the JWKS. So a
   namespace can set `issuerURL` to an `https` URL that it hosts, for
   example a bucket of static files.
   - `GET /api/issuer/{ns}/export` answers the two files, the discovery
     document and the JWKS, for that URL. The person copies them to it.
   - The JWKS of the export holds each key of the states `next`, `active`
     and `retired`, the same keys as `GET /oidc/{nsid}/jwks`. So a token
     that a key signed just before a promotion still verifies after the
     next copy. A copy after each rotation event keeps the trust. The
     notification `issuer.key-next` (decision 11) tells the team when to
     copy.
   - Before the first copy, a person makes the keys with `POST
     /api/issuer/{ns}/keys` (decision 13). The export also makes them when
     the namespace has no key. So the first login after a copy works, and
     the first open of a login block does not make a key that no file
     holds.
   - For a namespace with `issuerURL`, the server promotes a `next` key
     (decision 5) only after it read the JWKS at `issuerURL` through the
     egress guard and found the `kid` of the `next` key. When the read
     fails or the `kid` is not there, the active key keeps signing after
     its 90 days, the server sends `issuer.key-copy-missing` (decision 11)
     each day, and it tries again the next day. An active key never signs
     for more than 180 days. At that limit the open of a login block fails
     with `profile.ErrIssuerKeyExpired`, which names the export route.
   - Google Cloud, Vault and Kubernetes can take the keys with no URL. The
     docs show each one.
4. **Keys.** A key is RSA 2048 with `RS256`, with a `kid` of the prefix
   `oik-` and 20 random characters. It has one of four states:

   | State | In the JWKS | Signs |
   | --- | --- | --- |
   | `next` | yes | no |
   | `active` | yes | yes |
   | `retired` | yes | no |
   | `deleted` | no | no |

   - The server makes the keys. The `Wrapper` of the secret package wraps
     each private key with a data key of the namespace scope `issuer`
     (ADR 23), and the store holds only the wrapped key. The key-encryption
     key never signs anything.
   - The keys are namespace data. The backup of the namespace holds the
     wrapped keys, so a restore keeps the trust of each cloud.
   - A restore into another installation is a copy unless it is a move of
     the namespace (the source stops serving it). A copy must not sign with
     the same keys as the source, or two installations sign for one
     issuer. So a restore that is not a move restores no key. The
     namespace gets new keys at the next open, and its clouds must trust
     the new issuer URL.
   - Only `profile.Issuer` reads a private key, in the memory of the
     instance that signs, for at most one minute. No route answers a
     private key.
5. **Rotation.** The schedule `issuer.rotate` runs each day:
   - A namespace with no key gets an `active` key and a `next` key at the
     first open of a login block, at `POST /api/issuer/{ns}/keys`, or at
     the first export (decision 3). A namespace with `issuerURL` and no
     key refuses the open with `profile.ErrIssuerKeyMissing`, which names
     the route that makes the keys, so the server never signs with a key
     that no copied file holds.
   - Each active key signs for 90 days. Seven days before the end, the
     schedule makes a new `next` key if none exists. At the end, the `next`
     key becomes `active` and the old key becomes `retired`. For a
     namespace with `issuerURL`, the check of decision 3 comes first.
   - A `retired` key stays in the JWKS for 7 days, then becomes `deleted`.
   - A person with `namespace:manage-issuer` can rotate now. A normal
     rotation makes the `next` key active and makes a new `next` key.
   - An **emergency rotation** makes a fresh `active` key and a fresh
     `next` key, and deletes each earlier key at once, also the earlier
     `next` key. A compromise of the store or of the wrapper may expose the
     `next` key too, so the server never promotes it in an emergency.
     Tokens that an earlier key signed stop working at each relying party
     when it reads the JWKS again. A relying party drops a key only when it
     refreshes its cache, which can take minutes to hours. A cloud session
     that a token already got, for example an STS session of up to 12
     hours, stays valid until it ends. The docs say this, and say how to
     revoke such sessions at each cloud. The console says that a copy of
     the files at an `issuerURL` must follow at once.
   - The JWKS holds at most 4 keys: one `active`, one `next` and at most 2
     `retired`. When a manual rotation would make a third `retired` key,
     the oldest `retired` key becomes `deleted` before its 7 days end. A
     token that it signed is at most 60 minutes old (decision 6), so this
     breaks no token unless rotations come less than 60 minutes apart. The
     answer of the rotation names each key that it deleted.
6. **Lifetimes.** `exp - iat` is 5 minutes by default for a token that the
   server uses at once in an exchange. A login block can set `tokenLifetime`
   from 1 to 60 minutes. A token in a file for a tool (the login mode
   `token`, ADR 207 decision 2) lives 15 minutes, and the launcher asks for
   a new one before it ends (ADR 203 decision 7). No token lives more than
   60 minutes. The lifetime of a cloud session is a different input of the
   block, for example `duration` of `login/aws`.
7. **Audiences.** `aud` is one string. Each login adapter has a default
   audience, for example `sts.amazonaws.com` for AWS and
   `api://AzureADTokenExchange` for Azure (ADR 207). A block can set
   `audience` to any string of 1 to 255 bytes, with these refusals at save
   and at open (`profile.ErrInvalid`):
   - the `publicURL` of the installation and each URL under it, so that a
     token of the issuer never authenticates to OpenDustpan,
   - an empty string and a string with a control character.

   The trust rules of ADR 54 also refuse each issuer of this installation,
   and each `issuerURL` that a namespace sets (decision 3).
   So a profile token never becomes an access token of OpenDustpan.
8. **One signer.** `profile.Issuer` is the only code that signs. It has two
   methods:

   ```go
   // Issuer signs tokens for one namespace.
   type Issuer interface {
       // Login signs a token with purpose "login" for one block of one
       // open. ErrIssuerNotConfigured, ErrIssuerKeyMissing,
       // ErrIssuerKeyExpired (decision 3), ErrInvalid for an audience of
       // decision 7, ErrSubjectTooLong (ADR 205 decision 6).
       Login(ctx context.Context, o OpenContext, block string, aud string, lifetime time.Duration) (Token, error)
       // Adapter signs a token with purpose "adapter" and the hash of
       // body for one call of an external adapter (ADR 208).
       // ErrIssuerNotConfigured, ErrIssuerKeyMissing, ErrIssuerKeyExpired,
       // ErrInvalid.
       Adapter(ctx context.Context, o OpenContext, block string, aud string, body []byte) (Token, error)
   }
   ```

   `OpenContext` holds the facts of ADR 205, from the server. No route
   takes claims from a caller. The JWT header is `{"alg": "RS256", "typ":
   "JWT", "kid": "<kid>"}`. The code uses the standard library
   (`crypto/rsa`, `crypto/sha256`, `encoding/json`) and adds no dependency.
   `profiletest` checks the signature with an independent verifier, each
   claim, each refused audience, and a key of each state.
9. **Settings of the issuer.** A namespace has these settings, which need
   `namespace:manage-issuer`:
   - `issuerURL` (decision 3), empty by default,
   - the subject template of the namespace (ADR 205 decision 6),
   - `allowBlockSubjects`: whether a login block can name its own
     template, `true` by default,
   - `includeActorName` (ADR 205 decision 8), `false` by default.

   Each change writes an audit entry. A change of the template or of
   `issuerURL` breaks the trust of each cloud that matched the earlier
   form, so the console shows the subjects before and after.
10. **Audit.** Each token that the server signs writes one audit entry of
    the kind `access`, in the transaction that records the open: the
    profile, the block, the audience, the subject, `jti`, `exp` and the
    `kid`. The entry never holds the token. A team can find each cloud
    session of an incident from the `jti` in the logs of the cloud.
11. **Notifications.** The catalog of ADR 145 decision 5 gets
    `issuer.key-next` (a new next key, with the time when it becomes
    active), `issuer.key-revoked` (an emergency rotation) and
    `issuer.key-copy-missing` (the JWKS at `issuerURL` has no `next` key,
    decision 3). A notice
    holds the namespace, the `kid` and the times, never a key.
12. **A preview of the claims.** `POST /api/issuer/{ns}/claims-preview`
    takes a profile, a block and a context: a unit, an action, a phase and
    a ref. It answers the claims and the subject that a token would carry.
    It signs nothing, mints no token and calls no adapter. It needs
    `profile:read` on the profile. The console shows the subject of each
    login block above the editor, so a person can copy it into a trust
    policy. This is the claims debugger of the parity list.
13. **Actions and routes.**

    | Method and path | Action | Answer |
    | --- | --- | --- |
    | `GET /oidc/{nsid}/.well-known/openid-configuration` | none (public) | The discovery document. |
    | `GET /oidc/{nsid}/jwks` | none (public) | The public keys of the states `next`, `active` and `retired`. |
    | `GET /api/issuer/{ns}` | `namespace:read` | The settings, the issuer URL and the metadata of each key. |
    | `PUT /api/issuer/{ns}` | `namespace:manage-issuer` | The settings of decision 9. |
    | `POST /api/issuer/{ns}/keys` | `namespace:manage-issuer` | Makes the `active` and `next` keys of a namespace that has no key. `409` when keys exist. |
    | `POST /api/issuer/{ns}/keys/rotate` | `namespace:manage-issuer` | The keys after the rotation. The body sets `emergency` and requires `reason`. |
    | `GET /api/issuer/{ns}/export` | `namespace:manage-issuer` | The two files of decision 3. Makes the keys when the namespace has none. |
    | `POST /api/issuer/{ns}/claims-preview` | `profile:read` on the profile | The claims of decision 12. |

    The built-in role that holds `namespace:admin` gets
    `namespace:manage-issuer`.

## Consequences

- Clouds trust a namespace of OpenDustpan as they trust a CI product: one
  identity provider for each issuer URL, and a role for each subject.
- A namespace that never opens a login block has no key, and its JWKS is
  empty (decision 2).
- The store gets a table of keys for each dialect, with the state, the
  times and the wrapped key, and `profiletest` gets its conformance cases:
  the states, the one-active-key rule and the rotation at each boundary.
- This ADR answers the open question of ADR 133 on a token issuer in part:
  the issuer exists. Whether a job session or a sweep gets a token of its
  own stays open there and in ADR 164.
