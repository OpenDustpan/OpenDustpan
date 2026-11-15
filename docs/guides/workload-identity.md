# Workload identity for CI

A CI job can act on OpenDustpan without a stored token. The job asks the
identity provider of its CI platform for a token, and sends that token to the
token exchange of a namespace. When a trust rule of the namespace accepts the
token, the server answers an access token of a service principal of the
namespace. The access token lives one hour. The job then runs `tofu` or
`pulumi` with it.

Use workload identity for machines before service tokens. A service token
lives long and can leak from a CI secret. A token of a workload exists for one
run only.

## How the exchange decides

The server takes the rules of the namespace whose issuer is exactly the `iss`
claim of the token. For each such rule, it checks, in this order:

1. The signature, with the keys of the rule: the keys from the discovery
   document of the issuer, or the static keys of the rule. The algorithm is
   one of `RS256`, `RS384`, `RS512`, `PS256`, `PS384`, `PS512`, `ES256`,
   `ES384` and `EdDSA`. The server refuses `none` and each HMAC algorithm, a
   key or a key URL in the header of the token, and a key whose type does not
   fit the algorithm.
2. The issuer, exactly.
3. The audience: the `aud` claim is exactly the audience of the rule, or a
   list that holds it exactly.
4. The time: `exp` is after the time of the server, `nbf` and `iat` are not
   after it, each with 60 seconds of tolerance. A token that expires more
   than 24 hours later fails.
5. Each condition of the rule. A value matches exactly, with no wildcard.

Exactly one rule must accept the token. When two rules accept it, the request
names the rule with `"rule"`. The server then checks that the principal of the
rule exists, is a service principal of the namespace and is not disabled, and
that the issuer is not an issuer of this server: not at or under its public
URL, and not an issuer URL of its own OIDC issuer.

A refused exchange answers 401 with `trust-rule.no-match`, and never says
which check failed. The audit log of the namespace holds an entry with the
action `trust-rule:exchange`, the outcome `denied` and the reason. Many
refusals in one minute write one entry with the count. A refusal of a token
that the keys of a rule verified has its own entry for each rule and `sub`,
so a stream of malformed tokens cannot hide it. A successful exchange
writes an entry with the principal, the issuer, the `sub` and the `jti` of the
token, the ID of the access token and its expiry. No entry holds a token.

The server does not keep the `jti` of an exchanged token. So the same token
can be exchanged again until its `exp`, and each exchange issues a new access
token. The audit entries of the exchanges hold the `jti`, so an operator can
find each exchange that used a token.

The server reads the discovery document and the keys of an issuer through its
outbound address rules: it never connects to a loopback, private, link-local
or cloud metadata address, and follows no redirect. It keeps the keys for 5
minutes, and reads them again at most once in 30 seconds for each issuer, also
for a token that names an unknown key.

## Limits

| Limit | Value |
| --- | --- |
| Size of the JWT | at most 16 KiB |
| Tolerance of `exp`, `nbf` and `iat` | 60 seconds |
| Time from now to `exp` | at most 24 hours |
| Lifetime of the access token | `exchangeLifetimeSeconds`, 60 to 86400, one hour by default, and at most the maximum lifetime of the token policy |
| Size of a discovery document or a JWKS that the server reads | at most 256 KiB |
| Time of one read of the keys of an issuer | at most 10 seconds |
| Keys of one key set | at most 100 |
| Issuers in the cache of keys | at most 256 |
| Reads of the keys of one issuer | at most one at a time, and at most one in 30 seconds |
| Keys that the server keeps after a failed read | the last good keys, until they are 5 minutes old |
| Trust rules of one namespace | at most 100 |
| Refusal entries in the audit log, for tokens that no rule verified | one for each namespace in each minute, with the count of the others |
| Refusal entries in the audit log, for tokens that the keys of a rule verified | one for each rule and `sub` in each minute, with the count of the others |
| Pairs of rule and `sub` with entries of their own | at most 64 in each namespace. The other pairs share one entry in each minute, with the count of the others. |
| Keys of all refusal entries of the installation | at most 4096. Beyond that, the refusals of new keys share one entry in each minute, and the audit log of the installation holds their count. |

## Set it up

1. Make a service principal in the namespace, and give it the role that the
   job needs, for example `maintainer` on the namespace:

   ```text
   POST /api/server/namespaces/acme/principals   {"subject":"ci-deployer"}
   POST /api/server/namespaces/acme/grants        {"subject":"principal:<id>","role":"maintainer"}
   ```

   The grant needs a console session with a recent sign-in. An access token,
   also the bootstrap admin token, gets 403 with the error code
   `mfa.fresh-step-required`. Sign in to the console first. See
   [Console sign-in](console-sign-in.md#tokens-from-a-console-session).

2. Make a trust rule for it. You need `namespace:manage-trust-rules` on the
   namespace and `principal:manage-tokens` on the principal. Only a person who
   signed in to the console, or the bootstrap admin token, can make a rule. Any
   other access token gets 403 with the error code
   `trust-rule.sign-in-required`. See
   [the trust rules API](../reference/trust-rules-api.md).

### GitHub Actions

```json
{
  "name": "infra main",
  "issuer": "https://token.actions.githubusercontent.com",
  "audience": "https://dustpan.example.com",
  "keySource": "discovery",
  "principal": "<id>",
  "conditions": [
    {"claim": "sub", "values": ["repo:acme/infra:ref:refs/heads/main"]},
    {"claim": "repository_owner", "values": ["acme"]}
  ]
}
```

The job needs the permission `id-token: write`. It asks for a token with the
audience of the rule, and exchanges it:

```yaml
permissions:
  id-token: write
  contents: read
steps:
  - name: Get an OpenDustpan token
    run: |
      jwt=$(curl -sS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
        "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=https://dustpan.example.com" | jq -r .value)
      token=$(curl -sS -X POST -d "{\"token\":\"$jwt\"}" \
        https://dustpan.example.com/api/dustpan/v1/namespaces/acme/trust-rules/exchange | jq -r .token)
      echo "::add-mask::$token"
      echo "PULUMI_ACCESS_TOKEN=$token" >> "$GITHUB_ENV"
      echo "TF_TOKEN_dustpan_example_com=$token" >> "$GITHUB_ENV"
```

### GitLab CI/CD

The issuer is the URL of the GitLab instance, for example `https://gitlab.com`.
The `sub` claim has the form `project_path:acme/infra:ref_type:branch:ref:main`.

```yaml
deploy:
  id_tokens:
    DUSTPAN_JWT:
      aud: https://dustpan.example.com
  script:
    - export TF_TOKEN_dustpan_example_com=$(curl -sS -X POST -d "{\"token\":\"$DUSTPAN_JWT\"}" https://dustpan.example.com/api/dustpan/v1/namespaces/acme/trust-rules/exchange | jq -r .token)
    - tofu apply -auto-approve
```

### An issuer with no discovery document

A JWT issuer that the server cannot reach, for example a Kubernetes cluster on
a private network, uses the key source `static`. The rule holds the public
keys of the issuer as a JWKS, and the server makes no outbound request. When
the issuer changes its keys, make a new rule with the new keys and delete the
old one.

## The lifetime of the access token

The access token of an exchange lives one hour. The token policy of the
installation holds the setting `exchangeLifetimeSeconds` (60 to 86400), and
its maximum lifetime bounds it too:

```text
PATCH /api/server/token-policy   {"exchangeLifetimeSeconds": 900}
```

The access token is a token of the service principal of the rule, with the
kind `trust`. It shows in the list of the tokens of the principal, and a
holder of `principal:manage-tokens` on the principal can revoke it. No
rotation renews it: the job asks for a new token. A delete of the rule does
not end the tokens that the rule issued earlier.

## Providers in a profile: login/oidc

Workload identity also works in the other direction: the server signs a token
for a job, and a remote system trusts the server. The issuer of the namespace
signs the token (see [The OIDC issuer of a namespace](oidc-issuer.md)). A
provider block with `use: login/oidc` asks the issuer for one token for the
audience that the block names, and gives the token as a secret value. The
server makes no exchange: the tool or the remote system that reads the value
checks the token itself.

```yaml
schemaVersion: 1
providers:
  vault:
    use: login/oidc
    with:
      audience: https://vault.example.com
      tokenLifetime: 15m
```

| Input | Required | Description |
| --- | --- | --- |
| `audience` | yes | The `aud` claim of the token: the value that the relying party expects. 1 to 255 bytes, with no control character. |
| `tokenLifetime` | no | How long the token lives, from `1m` to `60m`. The default is `15m`. |

| Output | Secret | Description |
| --- | --- | --- |
| `token` | yes | The signed token, a compact JWT. |
| `issuer` | no | The `iss` claim: the issuer URL of the namespace. |
| `audience` | no | The `aud` claim of the token. |
| `expiresAt` | no | When the token expires, in RFC 3339 form. |

The rules of a block:

- The server refuses an audience that names a host of this server, so that a
  token of a profile can never be used against the API of the server.
- A block has a deadline of 10 seconds by default, and at most 30 seconds.
  All the provider blocks of one open have at most 60 seconds together.
  When a deadline ends, the block fails and gives no value.
- An error of a block never holds the token or a secret input. The server
  replaces each such value with `[secret]`.
- When one block fails, the open gives no value of any block.
- Each token that the issuer signs for a block has an audit entry, also when
  the block fails after it got the token.

A `login/oidc` block is not active yet: the server does not run provider
blocks in this version, and no job step reads their values. Delivery of the
token to a job comes with runs.
