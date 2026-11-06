# Sign-in with a trusted header

A trusted header lets a proxy in front of the server sign in a person. The
proxy authenticates the person. Then it sends the identity of the person to
the server in a request header. The server reads that header only from a
proxy that you name, and only with a second proof that the proxy sent the
request.

The server has the adapter of this sign-in method. A setting that turns a
header provider on is not available yet. This guide tells what the method
checks, so that you can prepare your proxy.

## The two layers

The server accepts a header only when both layers pass. Each layer stops a
forged header alone.

1. **The peer.** The TCP peer of the connection must be in the proxy list of
   the provider. The server reads the peer from the connection, never from
   `X-Forwarded-For` or `Forwarded`.
2. **The proxy proof.** In the `jwt` mode, the header holds a JWT that the
   keys of the issuer sign. In the `plain` mode, the request also carries a
   shared secret header.

### The proxy list of the provider

Each header provider has its own list of proxy addresses and CIDR ranges.
This list is not `--trusted-proxies`. `--trusted-proxies` only tells the
server how to read the client address. A load balancer that forwards the
client address is not always the proxy that authenticates a person.

> **Warning:** Put only the proxy that authenticates persons in the list. A
> peer in the list can send any identity in the plain mode, when it also
> has the shared secret. Do not use a wide range such as a whole network of
> a cluster. The server refuses a range of `/0`.

An empty list turns the provider off.

### Removal of the headers

The server removes the user header, the groups header and the secret
header from each request whose peer is not in the proxy list. It does this
before a handler reads the request. A client cannot send these headers
through a path that does not go through the proxy.

## The jwt mode

The proxy sends a signed JWT. The server checks these items:

- The signature, with the keys of the issuer. The server reads the keys
  from the key URL through the egress rules, and keeps them for at most 15
  minutes. A token with an unknown key ID reads the key URL again at most
  once in 30 seconds.
- The algorithm. The presets accept ES256 only. A custom issuer accepts
  ES256, ES384, EdDSA, and RS256, RS384, RS512, PS256, PS384 and PS512.
  The server refuses `none` and each HMAC algorithm.
- The issuer (`iss`) and the audience (`aud`).
- The time. `exp` must be in the future and `iat` must not be in the
  future, with 60 seconds of clock skew. The server refuses a token that
  lives more than 24 hours.
- A header that names or holds a key (`jku`, `jwk`, `x5u`, `x5c`). The
  server refuses it. The key always comes from the key URL.

The subject is the `sub` claim. The email is the `email` claim.

### Google Cloud IAP

| Setting | Value |
| --- | --- |
| Header | `X-Goog-Iap-Jwt-Assertion` |
| Key URL | `https://www.gstatic.com/iap/verify/public_key-jwk` |
| Issuer | `https://cloud.google.com/iap` |
| Audience | You set it: `/projects/<project number>/global/backendServices/<service ID>` |

### AWS Application Load Balancer

| Setting | Value |
| --- | --- |
| Header | `X-Amzn-Oidc-Data` |
| Key URL | `https://public-keys.auth.elb.<region>.amazonaws.com/<key ID>` |
| Issuer | You set it: the issuer of the identity provider of the load balancer |
| Signer | You set it: the ARN of the load balancer |

The load balancer sends no audience. The `signer` field of the token must
equal the ARN that you set. The key ID must match `^[A-Za-z0-9-]{1,128}$`
before the server puts it into the key URL.

### A custom issuer

You set the header, the JWKS URL, the issuer and the audience. The JWKS URL
must be an `https` URL. The egress rules apply to it.

## The plain mode

The proxy sends the subject in the user header and a shared secret in a
second header. The server compares the secret in constant time. The secret
has 32 to 1024 printable ASCII bytes.

The subject has 1 to 255 characters from this list: letters `A` to `Z` and
`a` to `z`, digits, and `.`, `_`, `@`, `+`, `:` and `-`. The server refuses
another character. It never changes the value to make it valid.

## Groups

Groups are off by default. In the jwt mode, you name a claim that holds a
list of strings. In the plain mode, you name a header that holds a
comma-separated list.

- An empty header or an empty list is an empty list of groups.
- A missing header or claim refuses the sign-in. A proxy that drops the
  header must not remove each membership of the person.
- More than 1000 groups refuse the sign-in.

## Fresh authentication

A proxy does not prove the time of the authentication. A step that needs a
fresh authentication asks the person for a passkey, a TOTP code or an SSH
signature that the person enrolled with the server.

## Errors

| Error | Cause |
| --- | --- |
| `request does not come from a trusted proxy` | The peer is not in the proxy list, or the shared secret is missing or wrong. |
| `sign-in input is not valid` | A header has the wrong form, comes twice, or has a character outside the allowlist. |
| `identity provider answer failed a check` | The JWT failed a check of its signature, algorithm, issuer, audience or time. |
| `identity provider is not available` | The server could not read the keys of the issuer. |
| `sign-in answer has no groups` | Groups are on and the request has no groups header or claim. |
| `sign-in provider is disabled` | The provider is off, or its proxy list is empty. |
