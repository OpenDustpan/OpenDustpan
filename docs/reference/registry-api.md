# Registry API

The private registry holds the providers and the modules that a team
publishes. This page describes the routes that publish a provider version or a
module version, list versions and manage the trusted signing keys, and the
routes of the provider registry protocol and the module registry protocol that
OpenTofu reads.

## Registry API

The paths of the API start with `/api/dustpan/v1`. Each path in this table is
relative to `/api/dustpan/v1/namespaces/{namespace}/registry`. Every action has
the namespace as its object.

| Method and path | Action | Idempotency-Key | Answer |
| --- | --- | --- | --- |
| `POST /uploads` | `registry:publish` | refused with 400 | 201 and the token, the SHA-256 and the size of the staged file |
| `PUT /providers/{name}/versions/{version}` | `registry:publish` | taken | 200 and the release |
| `GET /providers` | `registry:read` | not applicable | One page of the providers of the namespace that have a release, ordered by name |
| `GET /providers/{name}/versions` | `registry:read` | not applicable | The releases of the provider, from the lowest version to the highest |
| `GET /providers/{name}/versions/{version}` | `registry:read` | not applicable | The release |
| `PUT /modules/{name}/{system}/versions/{version}` | `registry:publish` | taken | 200 and the module release |
| `GET /modules` | `registry:read` | not applicable | One page of the modules of the namespace that have a release, ordered by name and system |
| `GET /modules/{name}/{system}/versions` | `registry:read` | not applicable | The releases of the module, from the lowest version to the highest |
| `GET /modules/{name}/{system}/versions/{version}` | `registry:read` | not applicable | The module release |
| `POST /keys` | `registry:manage-keys` | taken | 201 and the key |
| `GET /keys` | `registry:manage-keys` | not applicable | The keys of the namespace, ordered by fingerprint |
| `DELETE /keys/{fingerprint}` | `registry:manage-keys` | taken | 204 and no body |

A route that does not take the `Idempotency-Key` header answers 400 when the
request has the header. The server does not ignore the header.

The role `admin` holds all three actions. No other built-in role holds
`registry:publish` or `registry:manage-keys`.

### Upload

The body of `POST /uploads` is the bytes of one file. Send the header
`Content-Type: application/octet-stream`. Another media type, or none, answers
415. The answer is:

```json
{"upload": "<token>", "sha256": "<hex>", "size": 12345}
```

The token has 32 lowercase letters and digits. The SHA-256 and the size are the
values that the server computed while it wrote the file. A body above the limit
of one file answers 413. An empty body answers 400.

### List providers

`GET /providers` answers `{"providers": [{"namespace", "name"}], "nextPageToken"}`.
`pageSize` is a number from 1 to 1000, and the default is 100. When more providers
remain, `nextPageToken` holds the value to send as `pageToken` in the next request.
It is empty on the last page. A namespace with no provider answers an empty
list. A `pageSize` or a `pageToken` that is not valid answers 400.

### Publish

The body of `PUT /providers/{name}/versions/{version}` is:

```json
{
  "protocols": ["5.0", "6.0"],
  "files": [
    {"role": "sums", "name": "terraform-provider-payments_1.0.0_SHA256SUMS", "upload": "<token>"},
    {"role": "signature", "name": "terraform-provider-payments_1.0.0_SHA256SUMS.sig", "upload": "<token>"},
    {"role": "archive", "name": "terraform-provider-payments_1.0.0_linux_amd64.zip", "upload": "<token>"}
  ]
}
```

`protocols` lists the plugin protocol versions of the provider, from the lowest
to the highest, with no repeat. It has one to four entries in the form `5.0`.
`files` has one `sums`, one `signature` and one to 64 `archive` entries. A token
names a file that was staged in the same namespace. Each token is used once.

The answer is the release:

```json
{
  "namespace": "acme",
  "name": "payments",
  "version": "1.0.0",
  "protocols": ["5.0"],
  "publishedAt": "2026-10-21T09:00:00Z",
  "publishedBy": "ada",
  "keyFingerprint": "276D23ED2E9E5E63...",
  "served": true,
  "files": [
    {"role": "sums", "name": "...", "sha256": "..."},
    {"role": "signature", "name": "...", "sha256": "..."},
    {"role": "archive", "name": "...", "sha256": "...", "platform": "linux_amd64", "size": 12345}
  ]
}
```

`served` reports whether the provider registry protocol serves the release
now. A release whose signing key the namespace removed stays in the list of
`GET /providers/{name}/versions` with `"served": false`. The version list of
the protocol leaves it out, so `tofu init` selects an older release.

### Modules

The body of `PUT /modules/{name}/{system}/versions/{version}` is
`{"upload": "<token>"}`. The token names one file that `POST /uploads` staged
in the same namespace. The file is a zip file or a tar.gz file. A module has no
signature, so the publish needs no signing key. The server reads every entry
of the archive and refuses an archive with 422 and the code
`registry.module-archive-refused`. The server makes the file name of the stored
archive. The answer is the module release:

```json
{
  "namespace": "acme",
  "name": "vpc",
  "system": "aws",
  "version": "1.0.0",
  "publishedAt": "2026-10-21T09:00:00Z",
  "publishedBy": "ada",
  "archive": {"name": "vpc-aws-1.0.0.tar.gz", "sha256": "...", "size": 12345}
}
```

`GET /modules` answers `{"modules": [{"namespace", "name", "system"}], "nextPageToken"}`
and pages as `GET /providers` does. `GET /modules/{name}/{system}/versions`
answers `{"releases": [...]}`.

### Keys

The body of `POST /keys` is `{"armoredPublicKey": "<ASCII armor>"}`. The answer
holds `fingerprint`, `addedAt`, `addedBy` and `armoredPublicKey`. The server
stores the canonical form of the key, not the bytes that you sent.

### Errors

The error body is `{"code": 422, "message": "...", "errorCode": "..."}`. The
text names the file or the field that fails and holds no byte of a key. The
API also answers the shared codes of the tool-neutral APIs (see
[the error codes of the tool-neutral APIs](tool-neutral-error-codes.md)), for
example `server.internal` for a 500.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `registry.invalid` | A name, a version, a protocol or a request field is not valid |
| 400 | `registry.key-invalid` | The key cannot be registered |
| 403 | `access.denied`, `access.ip-not-allowed`, `session.csrf`, `session.other-site` | See the error codes of a 403 answer |
| 404 | `registry.not-found` | No such release, provider, version or key |
| 404 | `registry.not-staged` | A token names a file that does not exist: upload the file again |
| 409 | `registry.version-exists` | The version exists with other files |
| 409 | `registry.key-exists` | A key with this fingerprint exists with other bytes |
| 413 | `registry.too-large` | A file or a release is above the limit |
| 422 | `registry.no-keys` | The namespace trusts no signing key |
| 422 | `registry.signature-invalid` | The signature is not valid for the checksums file |
| 422 | `registry.signature-untrusted-key` | No key of the namespace made the signature |
| 422 | `registry.archive-set-refused` | The checksums file, a hash or a file name fails a check |
| 422 | `registry.module-archive-refused` | The module archive has an unsafe path or link, passes a limit, or is not a zip file or a tar.gz file |
| 422 | `registry.key-limit` | The namespace holds 16 keys |

## Provider registry protocol

OpenTofu reads these routes. The service discovery document at
`/.well-known/terraform.json` names them as `providers.v1`, and only when the
server has a public URL and a registry. A provider address is
`<host>/<namespace>/<name>`, where the host is the host of the public URL and
the namespace is the OpenDustpan namespace.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /v1/providers/{namespace}/{type}/versions` | `registry:read` | The versions, with their plugin protocols and platforms |
| `GET /v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}` | `registry:read` | The download document |
| `GET /v1/providers/{namespace}/{type}/{version}/files/{sha256}` | the signed address | The bytes of one file of the release |

The first two routes take the token in the header `Authorization: Bearer`. The
download document holds the signed addresses of the archive, of the checksums
file and of the signature file, the SHA-256 of the archive, and the public key
that signed the release. An address is valid for 15 minutes, for one file of
one release. The file route serves the file only for a valid signature, and
only for a file that the release names.

An error of a protocol route answers with the registry error document. The
media type is `application/json`, and the document has three members. The
`errors` member is a list of strings with the text for a person. The `code`
member holds the code of the error catalog, for example `registry.not-found`,
and a client reads the cause from it, not from the text. The `meta` member is
an object with the member `args`. It is present only for a code that has
arguments: a code without arguments has no `meta` member. A path under
`/v1/providers/` that no route serves answers in the same document, with the
code `server.route-not-found`.

The list of versions leaves out a release whose key the namespace removed, so
a client selects an older release that it can download. A signed address stays
valid for its 15 minutes after the removal of the key.

A request for a host other than the host of the public URL, a name outside the
rule, a missing namespace, provider or version, and a release whose key the
namespace removed all answer 404. The answer does not tell the causes apart.

## Module registry protocol

OpenTofu reads these routes. The service discovery document names them as
`modules.v1`, and only when the server has a public URL and a registry. A
module address is `<host>/<namespace>/<name>/<system>`, where the host is the
host of the public URL and the namespace is the OpenDustpan namespace.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /v1/modules/{namespace}/{name}/{system}/versions` | `registry:read` | `{"modules": [{"versions": [{"version": "1.0.0"}]}]}` |
| `GET /v1/modules/{namespace}/{name}/{system}/{version}/download` | `registry:read` | 204 with the signed address of the archive in the header `X-Terraform-Get` |
| `GET /v1/modules/{namespace}/{name}/{system}/{version}/files/{sha256}` | the signed address | The bytes of the archive |

The first two routes take the token in the header `Authorization: Bearer`. The
signed address is valid for 15 minutes, for the archive of one release. It has
the member `archive` with the value `zip` or `tar.gz`, so that OpenTofu knows
how to unpack the file. A signature of a module address does not open a file of
a provider, and a signature of a provider address does not open a module.
The checks of the host, the errors and the 404 answers are the same as for the
provider registry protocol.

## Limits

| Limit | Value |
| --- | --- |
| One file | 512 MiB |
| One module archive | 64 MiB |
| One module archive when unpacked | 1 GiB |
| Entries in one module archive | 20,000 |
| All files of one release | 2 GiB |
| Archives for one version | 64 |
| Plugin protocols for one version | 4 |
| Trusted keys for one namespace | 16 |
| One key | 1 MiB |
| Validity of a signed file address | 15 minutes |
| Age of a staged file that no publish moved | 24 hours |
