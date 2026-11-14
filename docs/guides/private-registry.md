# Publish a provider or a module to the private registry

This guide shows how a platform team publishes an OpenTofu provider or an
OpenTofu module to OpenDustpan, and how `tofu init` installs it. The registry
holds the providers and the modules that a team builds itself. It does not
copy them from a public registry.

Each version of a provider is signed. The server checks the signature when you
publish, and OpenTofu checks it again when it installs the provider. A module
has no signature: see [Publish a module](#publish-a-module).

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

The releases that the key signed stay in the record. The management list
`GET .../registry/providers/{name}/versions` shows them with `"served": false`.
The registry stops serving them, because the download document must name the
key that signed the release. The version list of the registry protocol does
not show them, so `tofu init` selects
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

## Publish a module

A module is one archive. The registry stores the archive as you send it, and
`tofu init` downloads it from OpenDustpan and unpacks it.

You need a token with the action `registry:publish`. A module has no
signature and no signing key, so the namespace needs no key. OpenTofu has no
signature for a module, and the server cannot see who built the archive. The
check of the publisher is the token. The audit entry of the publish names the
token and the SHA-256 of the archive.

The archive is a zip file or a tar.gz file. The server reads the first bytes
of the file to find the format, and not the name. Put the files of the module
at the root of the archive, as they are in the module directory.

The name of the module uses lowercase letters, digits and single dashes. The
system is the target system, for example `aws`. It uses lowercase letters and
digits only. The name of a namespace follows the rule for a provider.

First upload the archive. This is the same route as for a provider:

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @vpc.tar.gz \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/uploads
```

Then name the upload and the version. Send an `Idempotency-Key` header, so
that a retry after a timeout is safe:

```sh
curl -X PUT -H "Authorization: token $DUSTPAN_TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"upload": "<token>"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/modules/vpc/aws/versions/1.0.0
```

The server makes the file name of the stored archive from the name, the
system, the version and the format, for example `vpc-aws-1.0.0.tar.gz`. It
does not take a name from the request.

The server reads every entry of the archive before it stores the archive. It
refuses the publish with 422 and the code `registry.module-archive-refused`
when:

- an entry has an absolute path or a segment `..`
- a path has a character outside the allowed set. A path may hold ASCII
  letters, digits and the symbols `-`, `_`, `.`, `+`, `@`, `=` and `,`. The
  server refuses a space, a colon, a backslash, a control character and every
  byte above 0x7f, so a name with a letter such as `é` is refused in all its
  forms
- a segment of a path ends with a dot, is longer than 255 bytes, or has the
  name of a Windows device (`CON`, `PRN`, `AUX`, `NUL`, `COM1` to `COM9` and
  `LPT1` to `LPT9`, in any case and with any extension)
- two entries have one path, with the case of the letters ignored
- a symbolic link has an absolute target or points outside the archive
- the target of a symbolic link passes through another symbolic link or ends at
  one. This covers chains and loops of links
- an entry is below a symbolic link
- the archive holds a hard link, a device or another special file
- the archive holds more than 20,000 entries. For a zip file the server reads
  the count from the end of the file and refuses before it reads the directory
- the entries are more than 1 GiB when unpacked. The server counts the bytes
  that it reads, and it does not use the sizes in the headers.
- the file is not a zip file and not a tar.gz file

A tar.gz file from `git archive` has a pax global header. The server accepts
it as metadata, and it does not use any path or link in it.

The archive file is at most 64 MiB. A refused publish leaves nothing that
OpenTofu can read, and the server removes the uploaded file.

A published version never changes. Publish the same archive again and the
answer is the same, with no second audit entry. Publish another archive for
the same version and the server answers 409. Fix a mistake with a new version.

To use the module, write its address with the host of the server and the
namespace, and run `tofu init`:

```hcl
module "vpc" {
  source  = "dustpan.example.com/acme/vpc/aws"
  version = "~> 1.0"
}
```

OpenTofu asks for the versions of the module, selects the highest version that
fits, and downloads the archive from a signed address. The address is valid for
15 minutes. The token for `tofu init` needs the action `registry:read`, as for
a provider.

List the modules of a namespace, and the versions of one module:

```sh
curl -H "Authorization: token $DUSTPAN_TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/modules
curl -H "Authorization: token $DUSTPAN_TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/modules/vpc/aws/versions
```

## What the registry refuses

The server answers a request for a host other than the host of `--public-url`
with 404, as for a missing provider, and it writes an audit entry. It
compares the host exactly. It does not use a forwarded header. Without
`--public-url` the server does not serve the provider registry protocol.

Every refusal of an authorization check writes an audit entry, as the other
routes do. A publish and a key change write one entry each. The entry of a
publish names the provider, the version, the fingerprint of the key and the
hashes of the checksums file and the signature file. The entry of a module
publish names the module, the version and the hash of the archive.

## Limits

| Limit | Value |
| --- | --- |
| One file | 512 MiB |
| One module archive | 64 MiB |
| One module archive when unpacked | 1 GiB |
| Entries in one module archive | 20,000 |
| All files of one release | 2 GiB |
| Archives for one version | 64 |
| Trusted keys for one namespace | 16 |
| Validity of a signed file address | 15 minutes |

## Known limits of this release

- The registry serves modules that you upload. It does not read modules from
  git tags yet.
- A backup holds the records of the releases, the keys and the files of the
  releases.
- A signed file address is valid only on the server process that made it, because
  each server process makes its own signing key at start. Behind one address with
  several server processes, use one server process for the registry, or set one shared key
  with the option `WithRegistryURLKey` of the library.
- The registry does not copy a provider from a public registry. To use a
  provider that you do not build, keep using the public registry.
