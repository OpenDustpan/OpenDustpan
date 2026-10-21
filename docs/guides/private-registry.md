# Publish a provider to the private registry

This guide shows how a platform team publishes an OpenTofu provider to
OpenDustpan, and how `tofu init` installs it. The registry holds the providers
that a team builds itself. It does not copy providers from a public registry.

Each version of a provider is signed. The server checks the signature when you
publish, and OpenTofu checks it again when it installs the provider.

The paths of the API in this guide are under `/api/dustpan/v1`. See
`docs/reference/registry-api.md` for the list of paths, the actions and the
limits.

## What you need

- The server runs with `--public-url`, for example
  `https://dustpan.example.com`. The registry protocol of OpenTofu works only
  with a public URL. OpenTofu needs an HTTPS address.
- A namespace, for example `acme`.
- A token with the actions `registry:manage-keys` and `registry:publish` for
  the publisher, and a token with `registry:read` for each person or job that
  runs `tofu init`. The role `admin` holds all three actions. No other
  built-in role holds `registry:publish` or `registry:manage-keys`.
- A provider that your build tool packed as an archive set: one zip archive for
  each platform, the checksums file and the detached signature of the
  checksums file. The names are:

  ```text
  terraform-provider-<name>_<version>_<os>_<arch>.zip
  terraform-provider-<name>_<version>_SHA256SUMS
  terraform-provider-<name>_<version>_SHA256SUMS.sig
  ```

The name of the provider uses lowercase letters, digits and single dashes. It
does not start or end with a dash, and it has at most 64 characters. The
name of a namespace follows the same rule. A namespace whose name is outside
this rule cannot publish. The registry does not change the case of a name, so
`Payments` and `payments` are two names, and only the second one can publish.

## Step 1: register the public signing key

The namespace trusts only the keys that you register. Export the public key
of the signing key pair in ASCII armor, and send it:

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -d "{\"armoredPublicKey\": $(jq -Rs . < release-key.asc)}" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/keys
```

The server reads the key and takes the fingerprint from the key. It refuses
these keys, and the error says why:

- a key that holds private key material
- a key that is expired or revoked now, or that cannot sign
- a key that uses DSA or ElGamal, or an RSA key of less than 2048 bits
- text that is not exactly one public key

A namespace holds at most 16 keys. Send the same key again and the server
changes nothing.

## Step 2: publish a version

A publish has two parts. First you upload each file, then you name the files
in one request.

Upload each file. The answer holds a token and the SHA-256 and the size that
the server measured:

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @terraform-provider-payments_1.0.0_linux_amd64.zip \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/uploads
```

An uploaded file is not readable. It waits under a staging key until a
publish moves it. The server removes a file that no publish moved after 24
hours.

Name the files and the plugin protocol versions of the provider. Send an
`Idempotency-Key` header, so that a retry after a timeout is safe:

```sh
curl -X PUT -H "Authorization: token $DUSTPAN_TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{
    "protocols": ["5.0"],
    "files": [
      {"role": "sums", "name": "terraform-provider-payments_1.0.0_SHA256SUMS", "upload": "<token>"},
      {"role": "signature", "name": "terraform-provider-payments_1.0.0_SHA256SUMS.sig", "upload": "<token>"},
      {"role": "archive", "name": "terraform-provider-payments_1.0.0_linux_amd64.zip", "upload": "<token>"}
    ]
  }' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/providers/payments/versions/1.0.0
```

The server checks the release in this order, and it refuses the publish at the
first check that fails:

1. The signature is valid for the exact bytes of the checksums file, and a
   key of the namespace made it. The server checks the signature before it
   reads one line of the checksums file. The error names the signature file.
2. The checksums file has one line for each archive, in the form that your
   build tool writes: a lowercase SHA-256, two spaces and a file name. A line
   with a path, a repeated name, or a name that is not in the upload is
   refused.
3. The SHA-256 that the server computed for each archive is the SHA-256 in
   the checksums file. The error names the archive.
4. The name of each archive gives the provider name, the version and a
   platform. They must match the request.

A file becomes readable only after all four checks pass. A refused publish
leaves nothing that OpenTofu can read, and the server removes the uploaded
files.

A published version never changes. Publish the same files again and the
answer is the same, with no second audit entry. Publish other files for the
same version and the server answers 409. Fix a mistake with a new version.

## Step 3: install the provider

Write the address of the provider with the host of the server and the
namespace:

```hcl
terraform {
  required_providers {
    payments = {
      source  = "dustpan.example.com/acme/payments"
      version = "1.0.0"
    }
  }
}
```

Give OpenTofu a token with `registry:read` in the CLI configuration file:

```hcl
credentials "dustpan.example.com" {
  token = "<token>"
}
```

Run `tofu init`. OpenTofu reads the list of versions, reads the download
document, downloads the archive, and checks the hash and the signature:

```text
- Installed dustpan.example.com/acme/payments v1.0.0 (signed, key ID 276D23ED2E9E5E63)
```

The lock file holds the `zh:` hash of each archive that the server serves.
The server does not unpack an archive, so it serves no `h1:` hash.

OpenTofu downloads the files of a release without the token. The download
document holds signed addresses for them. An address is valid for 15 minutes
and for one file of one release.

## List the providers of a namespace

A token with `registry:read` lists the providers of a namespace that have at
least one release. The list is in the order of the provider name:

```sh
curl -sS -H "Authorization: token <token>" \
  "https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/providers?pageSize=50"
```

```json
{"providers": [{"namespace": "acme", "name": "payments"}], "nextPageToken": ""}
```

`pageSize` is a number from 1 to 1000. The default is 100. When more providers
remain, `nextPageToken` holds a value. Send it as `pageToken` in the next request.
`nextPageToken` is empty on the last page. A namespace with no provider answers
an empty list. A `pageSize` or a `pageToken` that is not valid answers 400.

## Remove a key

Remove a key when it is no longer safe:

```sh
curl -X DELETE -H "Authorization: token $DUSTPAN_TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/keys/<fingerprint>
```

The releases that the key signed stay in the record. The registry stops
serving them, because the download document must name the key that signed
the release. The list of versions does not show them, so `tofu init` selects
an older version that a trusted key signed. If you pin one of these versions,
`tofu init` fails. Register the key again to serve them again.

A signed address that the server gave out before the removal stays valid for
its 15 minutes. After that time the registry serves no file of these releases.

Always remove a revoked key. Do not update it: the registry has no route to
replace the bytes of a key, and it refuses a key with the fingerprint of a
key that it holds when the bytes are different. The registry checks a
signature against the key bytes that the namespace registered. A key that is
revoked or compromised keeps checking successfully until you remove it from
the namespace, so remove it at once.

A publish of a release that only a removed key signed is refused with 422,
also when you published the same files before the removal.

## What the registry refuses

The server answers a request for a host other than the host of `--public-url`
with 404, as for a missing provider, and it writes an audit entry. It
compares the host exactly. It does not use a forwarded header. Without
`--public-url` the server does not serve the provider registry protocol.

Every refusal of an authorization check writes an audit entry, as the other
routes do. A publish and a key change write one entry each. The entry of a
publish names the provider, the version, the fingerprint of the key and the
hashes of the checksums file and the signature file.

## Limits

| Limit | Value |
| --- | --- |
| One file | 512 MiB |
| All files of one release | 2 GiB |
| Archives for one version | 64 |
| Trusted keys for one namespace | 16 |
| Validity of a signed file address | 15 minutes |

## Known limits of this release

- The registry serves providers. It does not serve modules yet.
- A backup holds the records of the releases and the keys. It does not copy
  the files of the releases yet. Copy the blob store with your own tools until
  a backup does.
- A signed file address is valid only on the instance that made it, because
  each instance makes its own signing key at start. Behind one address with
  several instances, use one instance for the registry, or set one shared key
  with the option `WithRegistryURLKey` of the library.
- The registry does not copy a provider from a public registry. To use a
  provider that you do not build, keep using the public registry.
