# Research: how the server talks to a KMS (SDK, net/http or adapter)

Question (issue #955): For each key management service (KMS), what does it
cost to talk to it with the vendor SDK, with a hand-written `net/http` client,
or through an adapter outside the server? What does the cost mean for the
port and for the cache of data keys (issue #954, ADR 217)?

Date: 2026-10-26

This note gives facts, numbers and a recommendation as input. It decides
nothing. The choice of client is open in RFC 76 section 6. It becomes policy
only when the project owner accepts it in an ADR.

## Summary

- **Local envelope cost is small.** One wrap with the built-in key takes
  about 240 ns. One unwrap takes about 120 ns. One `Encrypt` call for one
  64-byte value takes about 1.6 µs.
- **Today every `Encrypt` and `Decrypt` call unwraps the data key again.**
  `Service.dataKey` has no cache (`pkg/secret/secret.go`). With a built-in
  key this costs 120 ns. With a KMS it costs one network call for each
  request. A cache is a requirement, not an option.
- **A cache decides cost and quota, not the client.** For a workload of 100
  requests a second to one namespace, vendor prices give about $778 a month
  with no cache and about $0.05 before the free tier (so $0) with the refresh of ADR 217
  decision 12 (section 6).
- **SDK size varies by 5.8 times.** Added to a `net/http` baseline, the
  Vault client adds 2.0 MB, the AWS SDK 4.2 MB and 15 modules, and the Google
  Cloud SDK 11.6 MB and 32 modules. The four SDKs together add 18.0 MB and 71 modules. The server
  binary is 35.2 MB today (section 3).
- **PKCS#11 needs cgo.** `github.com/miekg/pkcs11` does not compile with
  `CGO_ENABLED=0`, and the container image builds with `CGO_ENABLED=0`. A
  PKCS#11 client in the server breaks that build. Only an adapter process
  avoids this.
- **Not measured:** the latency of a real KMS call, and a live run of any
  client. This run used no cloud credential (section 8).
- **Recommendation (input to #954, section 7):** keep the port neutral as
  ADR 217 decision 7 has it. Put the cache above the port. Use one
  in-process client, `net/http` for Vault Transit. Use an adapter process for
  Google Cloud KMS, Azure Key Vault and PKCS#11. For AWS KMS, the owner
  chooses between an adapter and one SDK (section 7).

## 1. Method

- The date of every measurement is 2026-10-26. The machine is a 4-core Intel
  Xeon at 2.10 GHz, Linux, Go 1.27.3.
- **Local cost:** `go test -bench` with a temporary black-box benchmark file
  in `pkg/secret`, three runs of 2 seconds each. The median is in the table.
  The file is in the appendix. It is not part of this change.
- **Size, modules, licenses:** one scratch module for each client option,
  outside the repository, so that `go.mod` of the project did not change.
  Each module is one `main` that builds a client and makes one wrap call.
  Build: `go build -trimpath -ldflags="-s -w"`, `CGO_ENABLED=0` (cgo for
  PKCS#11). The baseline is a `main` with `net/http`, `crypto/aes` and
  `crypto/cipher`: 5,898,400 bytes. The project already links `net/http`.
  So the baseline shows the cost that is already paid.
- **Modules:** `go list -deps` counts the modules compiled into the binary,
  not the main module. The numbers are for the versions in the table in
  section 3.
- **Licenses:** `trivy fs --scanners license` on the scratch `go.mod`. The
  count includes indirect modules. It did not run through
  `scripts/trivy-license-gate.py`. That gate passes LOW (MIT, BSD,
  Apache-2.0) and MEDIUM (MPL-2.0).
- **Vulnerability history:** the Go vulnerability database index
  (`https://vuln.go.dev/index/modules.json`, retrieved 2026-10-26), and
  `govulncheck` v1.8.0 (the version the Makefile pins) against the database
  of 2026-10-21.
- **Limits and prices:** the vendor pages in section 4, retrieved 2026-10-26.
  Prices change. Check the page before you rely on a number.
- **Not done:** any call to a cloud API, and any use of a credential.

## 2. What the code does today

- The `Wrapper` port in `pkg/secret/secret.go` has `ID`, `Wrap(ctx, key,
  aad)` and `Unwrap(ctx, ciphertext, aad)`. It has no key reference, no
  `Rewrap`, no `Status` and no error classes. ADR 217 decision 7 gives the
  target shape. So the port changes in #954 whatever client wins.
- `BuiltinWrapper` seals a 32-byte key with AES-256-GCM, a random 12-byte
  nonce and the associated data. The port already puts all context in the
  associated data, so a KMS adapter can pass it as encryption context.
- `Service.dataKey` calls `GetKey` on the store, then `Unwrap`, then builds an
  AEAD, for each `Encrypt` and `Decrypt` call. It clears the key after the
  call. The service keeps nothing between calls.
- The release image builds with `CGO_ENABLED=0` (`Dockerfile`).
- `pkg/blob/s3/sigv4.go` (502 lines) has an AWS Signature Version 4 signer.
  It uses the service name `s3` and static credentials with an
  optional session token. It has no role assumption, no web identity and no
  instance metadata. A KMS client needs the service name as a parameter and
  these credential sources.

### Local numbers

`go test -bench`, `BuiltinWrapper`, median of three runs:

| Benchmark | Time | Allocations |
| --- | --- | --- |
| `BuiltinWrapper.Wrap`, 32-byte key | 238 ns | 80 B, 2 |
| `BuiltinWrapper.Unwrap`, 32-byte key | 123 ns | 32 B, 1 |
| `Service.Encrypt`, one 64-byte value (includes `GetKey` and `Unwrap`) | 1,624 ns | 1,745 B, 12 |
| `Service.Decrypt`, one 64-byte value (same) | 1,385 ns | 1,520 B, 8 |
| `Service.Encrypt`, 100 values of 64 bytes (one `Unwrap`) | 52.7 µs | 37,772 B, 606 |
| Seal of one 64-byte value with an open AEAD (cache hit) | 250 ns | 96 B, 1 |

The in-memory key store of the test fixture is in the numbers. A real SQL
read adds to the first four rows.

What the numbers show:

- The cryptography is not the cost. 120 ns for an unwrap is the lower bound
  for any wrapper. A KMS call crosses a network, so it is orders of magnitude
  larger. This note did not measure it (section 8).
- A cache hit saves the `GetKey`, the `Unwrap` and the AEAD build. It does
  not change the seal.
- A batch of 100 values costs 0.53 µs for each value, with one `Unwrap`. The
  Pulumi CLI sends config values in batches (`DefaultMaxBatchBytes` splits by
  size). So the KMS call count for a Pulumi request is one for each batch,
  not one for each value.

## 3. Cost of each client option

All sizes are the stripped binary of a scratch `main` in bytes. "Added" is
the size minus the baseline of 5,898,400 bytes. The server binary is
35,176,608 bytes (`CGO_ENABLED=0`, `-trimpath -ldflags="-s -w"`, 55 modules in
`go list -m all`).

| Option | Versions | Binary | Added | Modules added | Licenses (scratch `go.mod`) |
| --- | --- | --- | --- | --- | --- |
| Baseline (`net/http`) | none | 5,898,400 | 0 | 0 | none |
| AWS SDK for Go v2 | `config` v1.33.10, `service/kms` v1.61.6, core v1.47.4 | 10,059,936 | +4,161,536 | 15 | 15 Apache-2.0 |
| Google Cloud KMS SDK | `cloud.google.com/go/kms` v1.36.0, `grpc` v1.83.2 | 17,522,848 | +11,624,448 | 32 | 21 Apache-2.0, 15 BSD-3-Clause, 2 MIT |
| Azure SDK for Go | `azidentity` v1.15.0, `azkeys` v1.5.0 | 9,351,328 | +3,452,928 | 14 | 7 MIT, 6 BSD-3-Clause, 1 Apache-2.0, 1 BSD-2-Clause |
| Vault API client | `github.com/hashicorp/vault/api` v1.23.0 | 7,905,440 | +2,007,040 | 17 | 10 MPL-2.0, 4 MIT, 4 BSD-3-Clause, 1 Apache-2.0 |
| All four SDKs in one binary | the four rows above | 23,871,648 | +17,973,248 | 71 | no new license kind |
| Adapter protocol on gRPC (Kubernetes KMS v2 API) | `k8s.io/kms` v0.37.1, `grpc` v1.84.0 | 10,858,656 | +4,960,256 | 7 | 4 BSD-3-Clause, 3 Apache-2.0 |
| Adapter protocol on HTTP/JSON over a Unix socket | none | 5,898,400 | about 0 | 0 | none |
| PKCS#11 client, cgo | `github.com/miekg/pkcs11` v1.1.2 | 1,591,496 | +84,008 over a `fmt` hello (1,507,488) | 1 | 1 BSD-3-Clause |

Notes on the table:

- **The hand-written `net/http` option adds no module.** It adds code that
  we keep. A SigV4 signer on the standard library is 56 lines in the scratch
  module (`sigv4/main.go`). It gives the published signature for the AWS
  test case `get-vanilla`
  (`5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31`). This
  line count is the signer only. It has no credential sources, no retry and
  no error mapping.
- **Vault's client brings MPL-2.0 modules.** Ten of the 19 licensed modules
  are MPL-2.0 (HashiCorp helper libraries). The gate accepts MPL-2.0, and
  `docs/research/licenses.md` accepts it. Each MPL-2.0 module needs its
  notice in the release archive. The `vault` repository itself is under the
  Business Source License since 1.16. The `api` subdirectory is MPL-2.0
  (the scan shows MPL-2.0 for `vault/api`). The Vault server is not linked.
- **A gRPC adapter protocol is not free.** It adds 5.0 MB and 7 modules
  to the program that links it. If the server links the gRPC client, the
  server pays this cost. If only the adapter process links it, the server
  pays nothing. A protocol on HTTP/JSON over a Unix socket costs the server
  no module. ADR 208 already defines an HTTP/JSON adapter protocol.
- **PKCS#11 changes the build.** With `CGO_ENABLED=0`, `pkcs11.New` is
  undefined, because the package has only cgo files. With cgo, the program
  links `libc` dynamically and loads the vendor library at run time. The
  release image builds static binaries with `CGO_ENABLED=0`. A PKCS#11 client
  in the server needs a second, cgo build and a different base image.
- **The scratch modules select old transitive modules.** `govulncheck`
  v1.8.0 reports findings in modules that the scratch modules select, not in
  the SDK code. The table is for the scratch modules, not for the server:

  | Scratch module | Findings | Module and version found | Fixed in |
  | --- | --- | --- | --- |
  | AWS | none | | |
  | Google Cloud | 5 (called) | `golang.org/x/net` v0.58.0 | v0.60.0 |
  | Azure | 5 (called) | `golang.org/x/net` v0.58.0 | v0.60.0 |
  | Vault | 9 (called), from 3 modules | `golang.org/x/net` v0.47.0, `go-jose/v4` v4.1.1, `golang.org/x/text` v0.31.0 | `x/net` v0.60.0 |
  | gRPC adapter protocol | 4 (called) | `golang.org/x/net` v0.57.0 | v0.60.0 |
  | All four | 5 (called) | `golang.org/x/net` v0.58.0 | v0.60.0 |

  The server does not have these findings. Its module graph already selects
  `golang.org/x/net` v0.60.0 (the fixed version), `golang.org/x/text`
  v0.43.0 and `go-jose/v4` v4.1.6, because the Go tool takes the highest
  version in the graph. `go.mod` also lists `x/crypto`, `x/exp`, `x/oauth2`,
  `x/sync` and `x/sys`. To check this, I added all four SDKs to a clean
  worktree of commit 9190fbd, ran `go mod tidy`, and ran `govulncheck` on a
  `main` that calls all four. It reported no vulnerability. The same step
  raised the `go list -m all` count from 55 to 285 lines, so the module graph
  grows more than the 71 compiled modules suggest. A future advisory for one
  of these modules would still fail `make vuln` until we bump the module.

### Vulnerability history of the modules

Source: the Go vulnerability database index. "Entries" are the entries for
the module path in the index. Entries for another module path (for example
an older major version) are separate.

| Module | Entries | What the entries are |
| --- | --- | --- |
| `github.com/aws/aws-sdk-go-v2` (core, `config`, `credentials`, `service/kms`) | 0 | |
| `github.com/aws/aws-sdk-go` (v1, not used) | 3 | one fixed in 1.34.0 (GO-2022-0391, plaintext hash exposure), two with no fixed version |
| `cloud.google.com/go/kms`, `cloud.google.com/go/auth` | 0 | |
| `golang.org/x/oauth2` (used by Google clients) | 1 | GO-2025-3488, memory use in token parsing, fixed in 0.27.0 |
| `google.golang.org/grpc` | 7 | 5 published in 2026: GO-2026-4762 (authorization bypass, 2026-03-27), GO-2026-6061, GO-2026-6348 (heap exhaustion), GO-2026-6441, GO-2026-6443 |
| `github.com/Azure/azure-sdk-for-go/sdk/azidentity` | 1 | GO-2024-2918, CVE-2024-35255, elevation of privilege, fixed in 1.6.0 |
| `azcore`, `azkeys` | 0 | |
| `github.com/hashicorp/vault/api` | 0 | |
| `github.com/hashicorp/go-retryablehttp` (used by `vault/api`) | 1 | GO-2024-2947, CVE-2024-6104, log leak, fixed in 0.7.7 |
| `github.com/hashicorp/vault` (the server module, not linked by the client) | 55, and 6 changed in the last 12 months | Server issues. The client does not link them. |
| `github.com/miekg/pkcs11` | 0 | |
| `k8s.io/kms` | 0 | |

An entry count is not a measure of quality. The database lists only reports
that the Go team curated. The number for `grpc` shows that a large transport
module needs frequent bumps. A search of GitHub security advisories was not
done.

### Authentication coverage

ADR 217 decision 9 needs one identity for each namespace at the KMS, and the
server assumes or signs it. The columns show what each client gives. "Code"
means code that we write and keep.

| KMS | SDK | Hand-written `net/http` | Adapter process |
| --- | --- | --- | --- |
| AWS KMS | Default chain: environment, shared config, web identity, container credentials, IMDS, SSO, assume role ([AWS SDKs and Tools: credential providers](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html)). The role for each namespace is `AssumeRole` with the SDK's STS provider. | Static keys: the existing signer. Instance roles: code for IMDSv2. Web identity and one role for each namespace: code for the STS calls ([AssumeRoleWithWebIdentity](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithWebIdentity.html)) and for credential renewal. | The adapter uses the SDK. The server sends the namespace identity to choose the role. |
| Google Cloud KMS | Application Default Credentials: environment file, `gcloud` file, attached service account from the metadata server, Workload Identity Federation file ([ADC](https://cloud.google.com/docs/authentication/application-default-credentials), [Workload Identity Federation](https://cloud.google.com/iam/docs/workload-identity-federation)). Service account impersonation is in the auth library. | Code for the OAuth token (metadata server, federation token exchange, impersonation call). The KMS REST call is simple ([`encrypt`](https://cloud.google.com/kms/docs/reference/rest/v1/projects.locations.keyRings.cryptoKeys/encrypt)). | As AWS. |
| Azure Key Vault | `DefaultAzureCredential` chain with environment, workload identity, managed identity, CLI ([credential chains](https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/credential-chains)). A federated credential with a token that the server signs needs the client-assertion credential. | Code for the token request to Microsoft Entra ID with a client assertion ([workload identity federation](https://learn.microsoft.com/en-us/entra/workload-id/workload-identity-federation)). The REST call is simple ([`wrapkey`](https://learn.microsoft.com/en-us/rest/api/keyvault/keys/wrap-key/wrap-key)). | As AWS. |
| Vault Transit | `vault/api` has the HTTP client. A login with a JWT is one write to `auth/<mount>/login` ([JWT/OIDC auth](https://developer.hashicorp.com/vault/docs/auth/jwt)). | The same two JSON calls with `net/http`. The token lease needs a renewal loop. | As AWS. |
| PKCS#11 | cgo library only. | Not possible: the interface is a C shared library ([PKCS #11 base specification v3.0](https://docs.oasis-open.org/pkcs11/pkcs11-base/v3.0/os/pkcs11-base-v3.0-os.html)). | The adapter process links cgo. |

Notes:

- The SDKs cover the common credential chains. They do not remove the need to
  build one client for each namespace, or to share a client and pass the
  identity at each call. The server holds as many identities as it has
  namespaces with a customer key.
- A hand-written client must also renew credentials before they expire and
  must map each KMS error to an error class (ADR 217 decisions 13 and 14).
- OpenBao is an open-source fork of Vault. This note did not read its
  Transit documentation. A check of its API against Vault's belongs in a
  later run before "Vault-compatible" is a claim.

## 4. Limits and prices from the vendors

All figures are from the pages named in the last column, retrieved 2026-10-26.
"Symmetric" means the operation that wraps a 32-byte key with a symmetric
key, or with an RSA key for Azure.

| KMS | Payload per wrap call | Request limit | Price of a wrap or unwrap | Price of a key | Source |
| --- | --- | --- | --- | --- | --- |
| AWS KMS | 4,096 bytes for a symmetric key. The key supports encryption context. | Shared across Encrypt, Decrypt, ReEncrypt and others: 10,000 requests a second by default, 20,000 in seven Regions, and 100,000 in us-east-1, us-west-2 and eu-west-1. RSA keys: 1,000. | $0.03 for 10,000 requests, after a free tier of 20,000 requests a month | $1.00 a month for a key | [Request quotas](https://docs.aws.amazon.com/kms/latest/developerguide/requests-per-second.html), [Encrypt](https://docs.aws.amazon.com/kms/latest/APIReference/API_Encrypt.html), [Pricing](https://aws.amazon.com/kms/pricing/) |
| Google Cloud KMS | 64 KiB of plaintext and of additional authenticated data for software keys. 8 KiB combined for HSM keys. | Metric `software_usage`: 6,000,000 tokens a minute, soft limit. Metric `hsm_usage`: 3,000,000 tokens a minute, soft limit. A software operation costs 100 tokens, and a symmetric HSM encrypt or decrypt costs 100 tokens. So the limits are 60,000 software operations a minute (1,000 a second) and 30,000 HSM operations a minute (500 a second). The page says the quotas are regional. The metrics apply since 2026-02-16. The old metric `crypto_requests` (60,000 a minute) stops after 2026-08-31, when Google retires it. | $0.03 for 10,000 operations | $0.06 a month for a software key version (hourly price $0.000082192) | [Quotas](https://cloud.google.com/kms/quotas), [`encrypt`](https://cloud.google.com/kms/docs/reference/rest/v1/projects.locations.keyRings.cryptoKeys/encrypt), [Pricing](https://cloud.google.com/kms/pricing) |
| Azure Key Vault | `wrapkey` with `RSA-OAEP-256`: the key size limits the payload. A 2,048-bit key allows 190 bytes (256 − 2 × 32 − 2, [RFC 8017 section 7.1.1](https://www.rfc-editor.org/rfc/rfc8017#section-7.1.1)). No associated data. | RSA 2,048-bit: 4,000 transactions in 10 seconds for a software key and 2,000 for an HSM key, for each vault and Region. 3,072-bit: 1,000 and 500. 4,096-bit: 500 and 250. | $0.03 for 10,000 operations (Standard, RSA 2,048-bit). $0.15 for "advanced" key operations. | $1 a month for a premium HSM RSA 2,048-bit key | [Service limits](https://learn.microsoft.com/en-us/azure/key-vault/general/service-limits), [`wrapkey`](https://learn.microsoft.com/en-us/rest/api/keyvault/keys/wrap-key/wrap-key), [Retail Prices API](https://learn.microsoft.com/en-us/rest/api/cost-management/retail-prices/azure-retail-prices) (East US, `serviceName eq 'Key Vault'`) |
| HashiCorp Vault Transit | No limit found in the API page. `batch_input` sends many items in one request. A key with derivation needs a `context` for each call. | Set by the operator's cluster. No fixed vendor figure. | Self-run: the cost of the cluster. A hosted service has its own price list, not read. | not applicable | [Transit API](https://developer.hashicorp.com/vault/api-docs/secret/transit) |
| PKCS#11 HSM | Set by the device and mechanism. | Set by the device. | The cost of the device or service. | not applicable | [PKCS #11 base specification v3.0](https://docs.oasis-open.org/pkcs11/pkcs11-base/v3.0/os/pkcs11-base-v3.0-os.html) |

Notes:

- The wrap of a 32-byte data key fits every payload limit above.
- **Latency:** the pages above state no latency figure for a wrap call. This
  run did not look for one elsewhere. Kubernetes gives the KMS plugin a
  default timeout of 3 seconds
  ([KMS provider](https://kubernetes.io/docs/tasks/administer-cluster/kms-provider/)).
  Use a measured p50 and p99 from a live account before you set
  `keys.namespaceKeyCacheSeconds` limits (section 8).
- The Azure limit is a weighted sum with every other key operation in the
  same vault. A customer vault that also serves other applications shares
  the limit.
- Azure `wrapkey` takes no associated data. The server binds the namespace ID
  and the key ID with the AES-256-GCM seal (ADR 217 decision 3).
- Google Cloud KMS: re-encryption with a new key version counts as a
  cryptographic operation. Rotation itself is free (Pricing page).

## 5. The external adapter protocol

The Kubernetes KMS v2 API is the precedent named in the issue. It has three
calls: `Status`, `Encrypt` and `Decrypt`, over gRPC on a Unix socket. It has
no `Rewrap`. `Encrypt` returns a `key_id`, and `Decrypt` takes it back
([API definition](https://pkg.go.dev/k8s.io/kms/apis/v2)). The documentation
says the kube-apiserver caches data keys in the clear after the first unwrap
([KMS provider](https://kubernetes.io/docs/tasks/administer-cluster/kms-provider/)).

For OpenDustpan this means:

- The four calls of ADR 217 decision 7 are `Wrap`, `Unwrap`, `Rewrap` and
  `Status`. The Kubernetes API has the same shape without `Rewrap`. A
  `Rewrap` as `Unwrap` then `Wrap` costs two round trips, and the plaintext
  key passes the socket. AWS `ReEncrypt` and Vault `rewrap` do it inside the
  KMS. A port that offers `Rewrap` as a capability keeps that advantage.
- The adapter needs the identity for each call: ADR 217 decision 9 uses one
  identity for each namespace, and the server signs a JWT for it. The
  Kubernetes API has no such field. A protocol for OpenDustpan must carry
  the key reference and the identity proof in each request. ADR 208 does this
  for profile providers with a signed JWT in `Authorization`.
- A Unix socket gives the adapter the identity of the local process (file
  mode and peer credentials). It does not give the identity of a namespace.
  The signed token does.
- An adapter process adds one more process to run, upgrade and monitor. The
  Helm chart can run it as a sidecar. This note did not build a chart.
- Each KMS call passes one more local hop. On a Unix socket this is a
  fraction of the cost of a network call. This note did not measure it.

## 6. Caching data keys

### Facts

- ADR 217 decision 12 already sets a cache: open namespace key versions stay
  in memory for 300 seconds by default (60 to 3600), with a background
  refresh at half the time. Data keys and item keys leave memory with them.
  ADR 217 decisions 13 and 14 set what happens when the KMS cannot answer
  and when the customer revokes the key.
- Today no cache exists in `pkg/secret`. Every call unwraps (section 2).
- Vendors say the same: the AWS Encryption SDK page states that data key
  caching "can improve performance, reduce cost, and help you stay within
  service limits"
  ([data key caching](https://docs.aws.amazon.com/encryption-sdk/latest/developer-guide/data-key-caching.html)).
  Kubernetes caches all data keys in clear after the first unwrap in KMS v2
  ([KMS provider](https://kubernetes.io/docs/tasks/administer-cluster/kms-provider/)).

### Arithmetic

The numbers below use the vendor prices of section 4. They use an assumed
workload, not a measured one: one namespace, 100 requests a second for 30
days (2,592,000 seconds), each request needs one unwrap.

| Case | KMS calls a month | AWS KMS price at $0.03 per 10,000 |
| --- | --- | --- |
| No cache, one unwrap for each request | 259,200,000 | $777.60 |
| Cache with a refresh every 150 seconds (half of 300), for one instance | 17,280 | $0.05 before the free tier of 20,000 requests a month, so $0 |

- The no-cache case also uses 100 of the 10,000 requests a second of the AWS
  default quota for the whole Region and account, so one busy namespace can
  take 1% of a shared quota. It uses 100 of the 1,000 operations a second of the Google
  Cloud software limit (6,000,000 tokens a minute at 100 tokens each), which
  is 10%.
- On Azure the same load takes 100 of 400 requests a second (4,000 in 10
  seconds) for a software RSA 2,048-bit key. That is 25% of the vault's
  limit, shared with every other key operation in the vault.
- With the cache, the call rate does not depend on the request rate. It
  depends on the number of instances, namespaces and key versions.

### Recommendations for the cache (input to #954)

1. **Put the cache above the port**, in the service that uses the
   `Wrapper`, not in each adapter. Then an SDK client, a `net/http` client
   and an adapter process all share the same cache and the same rules. The
   conformance suite tests the port. A second suite tests the cache.
2. **Key the cache by what the KMS call needs:** the namespace key ID, the
   version, and the wrapper ID. Never key it by the plaintext.
3. **Collapse parallel misses.** One cache miss starts one KMS call, and the
   other requests for the same key wait for it (`singleflight`). Without
   this, a cold start with 100 requests makes 100 calls.
4. **Refresh in the background** at half the time, as ADR 217 decision 12
   says, so no request waits for a KMS call while the cache is warm.
5. **Do not cache a failed KMS call.** ADR 217 decision 14 stores the state
   `revoked` in SQL, which every instance reads. For `ErrUnavailable`, retry
   with a backoff and keep the open key until its cache time ends.
6. **Bound the cache and clear keys on eviction.** The existing code calls
   `clear(key)` after use. The cache must clear a key when it drops it.
7. **Show it.** Expose the effective cache time, the worst case of revoke
   time (cache plus grace) and the count of KMS calls in the API and in
   `dustpan_kms_requests_total` (ADR 217 decisions 14 and 27).

## 7. Recommendation for the port and for each KMS

This is input. #954 or an ADR decides.

### Port shape

ADR 217 decision 7 fits all three client kinds. The measurements add these
points for the port definition:

1. **Keep the port free of cloud types.** The scratch builds show why: the
   cloud types come with 15 to 32 modules each.
2. **Make `Rewrap` a capability, not a required method.** AWS `ReEncrypt`
   and Vault `rewrap` keep the plaintext inside the KMS. Google Cloud KMS,
   Azure Key Vault, PKCS#11 and the Kubernetes API have no such call. A
   helper that does `Unwrap` then `Wrap` serves them. A caller can ask the
   adapter whether it has the native call.
3. **Carry the identity of the namespace in each call** (or in the client
   that the call picks). ADR 217 decision 9 needs it for all four clouds, and
   an adapter process cannot guess it.
4. **Let the caller set the timeout.** The adapter does not choose it.
   Kubernetes defaults to 3 seconds.
5. **Return the KMS key version** that answered, as decision 7 says. The
   formats differ: Vault gives a `vault:v<n>:` prefix on the ciphertext, and
   Google Cloud KMS gives a `name` with the key version. The adapter maps
   them to one string.
6. **Cap the payload at 4,096 bytes**, the lowest limit of the four clouds
   for a symmetric key. For Azure, 190 bytes with a 2,048-bit RSA key. A
   32-byte data key fits, and the conformance suite can test the cap.
7. **Test the error classes with a fake KMS** for each adapter, as decision 7
   says: timeouts, 5xx, rate limits, disabled keys and denied access. The
   AWS SDK, the Google Cloud SDK and the Azure SDK each return their own
   error types. A hand-written client sees HTTP statuses.

### Client for each KMS

| KMS | Recommendation | Reason from the numbers |
| --- | --- | --- |
| Vault Transit | `net/http` in the server | The API is two JSON calls. The client adds 17 modules, 10 of them MPL-2.0, and 2.0 MB. A login with a JWT, which ADR 217 decision 9 needs, is one write. |
| Google Cloud KMS | Adapter process | The SDK adds 11.6 MB and 32 modules, 21 of them Apache-2.0. It is the largest, and it brings gRPC. The Go vulnerability database has 7 entries for `grpc`, 5 of them in 2026. |
| Azure Key Vault | Adapter process | The SDK adds 3.5 MB and 14 modules. ADR 217 decision 9 needs a client assertion and a federated credential. The `azidentity` package has a credential type for this. This run did not read its page. A hand-written client needs the token flow. |
| PKCS#11 | Adapter process | cgo breaks the static `CGO_ENABLED=0` image. No other option works. |
| AWS KMS | The owner chooses: an adapter process for the same shape as the others, or one SDK in the server | The AWS SDK adds 4.2 MB and 15 modules, all Apache-2.0. It is the one cloud SDK with 0 entries in the Go database and 0 `govulncheck` findings. A hand-written client is possible. The signer in `pkg/blob/s3` (502 lines) is a base. It needs the service name as a parameter, `AssumeRole`, web identity and IMDS. |

Reasons for the split:

- It gives the server one in-process client with a small API (Vault Transit)
  and no cloud SDK. The binary stays at 35.2 MB. The module list and the
  `make trivy` result do not change for the three largest SDKs.
- The vulnerability work for the SDKs moves to the adapter images. The
  images are still scanned by Trivy, and the adapter can be updated without a
  server release.
- A customer who needs BYOK on a cloud runs one more process. This is the
  cost of the choice. The Helm chart could ship the adapter as a sidecar.
- If the owner wants "no extra process" for AWS, the cheapest in-process
  path is one SDK. The second cheapest is code that we keep.
- **Protocol for the adapter process:** HTTP/JSON over a Unix socket with the
  signed token of ADR 208, not gRPC. gRPC adds 5.0 MB and 7 modules to
  whatever links it, and 5 `grpc` database entries in 2026.

Questions for the owner or #954:

- Does an adapter process meet the "self-hostable" aim for a team on AWS
  that wants BYOK, or must AWS KMS work in the server binary?
- Does the owner allow one cloud SDK in the server (AWS), which `CLAUDE.md`
  asks before adding?
- Which API does an adapter process offer for the identity of a namespace?
  ADR 217 decision 9 names the identities. The protocol must carry them.

## 8. What this note did not measure

- **Latency of a real KMS call.** The p50 and p99 of 1,000 calls against a
  real service, as RFC 76 section 6 asks, need a cloud account and
  credentials. This run used none, as the task required.
- **Conformance.** No one wrote a client for the port, so no `Wrapper`
  suite or error-class suite ran.
- **The size of a real change.** The numbers come from scratch programs. The
  real change shares modules with the server (for example `x/sys`, `x/text`),
  so the real added size may be lower than the "Added" column.
- **GitHub security advisories.** Only the Go vulnerability database and
  `govulncheck` gave the data.
- **OpenBao's Transit API,** the hosted Vault price lists, and the prices of
  an HSM service.
- **The cost of a hand-written client in full.** Only the SigV4 signer was
  written and checked.
- **The cost of an adapter in operation:** memory, restarts, upgrade steps.

## Sources

Vendor and standard pages (all retrieved 2026-10-26):

- AWS KMS request quotas: https://docs.aws.amazon.com/kms/latest/developerguide/requests-per-second.html
- AWS KMS `Encrypt`: https://docs.aws.amazon.com/kms/latest/APIReference/API_Encrypt.html
- AWS KMS pricing: https://aws.amazon.com/kms/pricing/
- AWS Encryption SDK, data key caching: https://docs.aws.amazon.com/encryption-sdk/latest/developer-guide/data-key-caching.html
- AWS credential providers: https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html
- AWS STS `AssumeRoleWithWebIdentity`: https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithWebIdentity.html
- Google Cloud KMS quotas: https://cloud.google.com/kms/quotas
- Google Cloud KMS pricing: https://cloud.google.com/kms/pricing
- Google Cloud KMS `encrypt`: https://cloud.google.com/kms/docs/reference/rest/v1/projects.locations.keyRings.cryptoKeys/encrypt
- Google Application Default Credentials: https://cloud.google.com/docs/authentication/application-default-credentials
- Google Workload Identity Federation: https://cloud.google.com/iam/docs/workload-identity-federation
- Azure Key Vault service limits: https://learn.microsoft.com/en-us/azure/key-vault/general/service-limits
- Azure Key Vault `wrapkey`: https://learn.microsoft.com/en-us/rest/api/keyvault/keys/wrap-key/wrap-key
- Azure Go credential chains: https://learn.microsoft.com/en-us/azure/developer/go/sdk/authentication/credential-chains
- Microsoft Entra workload identity federation: https://learn.microsoft.com/en-us/entra/workload-id/workload-identity-federation
- Azure Retail Prices API: https://learn.microsoft.com/en-us/rest/api/cost-management/retail-prices/azure-retail-prices
- Vault Transit API: https://developer.hashicorp.com/vault/api-docs/secret/transit
- Vault JWT/OIDC auth: https://developer.hashicorp.com/vault/docs/auth/jwt
- Kubernetes KMS provider: https://kubernetes.io/docs/tasks/administer-cluster/kms-provider/
- Kubernetes KMS v2 API: https://pkg.go.dev/k8s.io/kms/apis/v2
- PKCS #11 base specification v3.0: https://docs.oasis-open.org/pkcs11/pkcs11-base/v3.0/os/pkcs11-base-v3.0-os.html
- RFC 8017 (PKCS #1 v2.2): https://www.rfc-editor.org/rfc/rfc8017
- Go vulnerability database: https://vuln.go.dev/ (entries GO-2022-0391, GO-2024-2918, GO-2024-2947, GO-2025-3488, GO-2026-4762, GO-2026-6061, GO-2026-6348, GO-2026-6441, GO-2026-6443)
- `miekg/pkcs11`: https://pkg.go.dev/github.com/miekg/pkcs11

Project documents: ADR 23, ADR 31, ADR 151, ADR 208, ADR 217, RFC 33, RFC 76
section 6, `docs/research/licenses.md`.

## Appendix: the benchmark file

Put this file in `pkg/secret` as `zz_kmsbench_test.go`, run
`go test ./pkg/secret -run '^$' -bench . -benchtime=2s -count=3`, and remove
it. It reuses `memKeys` and `oneNamespace` from `secret_test.go`.

```go
package secret_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

func benchService(b *testing.B) (*secret.Service, *secret.BuiltinWrapper, secret.Scope) {
	b.Helper()

	kek := make([]byte, 32)
	_, _ = rand.Read(kek)

	w, err := secret.NewBuiltinWrapper(kek)
	if err != nil {
		b.Fatal(err)
	}

	ns, err := namespace.NewService(namespace.Config{Store: oneNamespace{}, Now: nil, Region: "local"})
	if err != nil {
		b.Fatal(err)
	}

	svc, err := secret.NewService(secret.Config{
		Keys:    &memKeys{keys: map[secret.Scope]secret.WrappedKey{}, mu: sync.Mutex{}},
		Wrapper: w, Namespaces: ns, Now: nil, MaxBatchBytes: 4 << 20,
	})
	if err != nil {
		b.Fatal(err)
	}

	name, _ := namespace.ParseName("acme")

	scope, err := secret.NewScope(name, "stack-1")
	if err != nil {
		b.Fatal(err)
	}

	return svc, w, scope
}

func BenchmarkWrapBuiltin(b *testing.B) {
	_, w, _ := benchService(b)
	key := make([]byte, 32)
	aad := []byte("opendustpan/secret/v1\x00acme\x00stack-1")

	b.ReportAllocs()

	for b.Loop() {
		if _, err := w.Wrap(context.Background(), key, aad); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnwrapBuiltin(b *testing.B) {
	_, w, _ := benchService(b)
	key := make([]byte, 32)
	aad := []byte("opendustpan/secret/v1\x00acme\x00stack-1")
	c, _ := w.Wrap(context.Background(), key, aad)

	b.ReportAllocs()

	for b.Loop() {
		if _, err := w.Unwrap(context.Background(), c, aad); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkServiceEncryptOne64B(b *testing.B) {
	svc, _, scope := benchService(b)
	in := [][]byte{make([]byte, 64)}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := svc.Encrypt(context.Background(), scope, in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkServiceDecryptOne64B(b *testing.B) {
	svc, _, scope := benchService(b)
	enc, _ := svc.Encrypt(context.Background(), scope, [][]byte{make([]byte, 64)})

	b.ReportAllocs()

	for b.Loop() {
		if _, err := svc.Decrypt(context.Background(), scope, enc); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkServiceEncryptBatch100x64B(b *testing.B) {
	svc, _, scope := benchService(b)
	in := make([][]byte, 100)

	for i := range in {
		in[i] = make([]byte, 64)
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := svc.Encrypt(context.Background(), scope, in); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSealCachedKey64B is the cost of one value when the data key is
// already open: AES-256-GCM seal with a built AEAD.
func BenchmarkSealCachedKey64B(b *testing.B) {
	blk, _ := aes.NewCipher(make([]byte, 32))
	aead, _ := cipher.NewGCM(blk)
	p := make([]byte, 64)
	aad := []byte("opendustpan/secret/v1\x00acme\x00stack-1")

	b.ReportAllocs()

	for b.Loop() {
		nonce := make([]byte, 12, 1+12+64+16)
		_, _ = rand.Read(nonce)
		_ = aead.Seal(nonce, nonce, p, aad)
	}
}
```
