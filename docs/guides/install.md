# Install OpenDustpan v0.1.0

This page is for operators who install v0.1.0. It shows how to pull the
v0.1.0 image, how to install from a binary archive, and how to install it
with Helm.

## Pull the image

Pull the pinned tag from the forge container registry:

```sh
docker pull ghcr.io/opendustpan/opendustpan:v0.1.0
```

Or pull the same digest from Docker Hub:

```sh
docker pull docker.io/opendustpan/opendustpan:v0.1.0
```

Both references hold the same digest. Compare them with:

```sh
docker buildx imagetools inspect ghcr.io/opendustpan/opendustpan:v0.1.0
docker buildx imagetools inspect docker.io/opendustpan/opendustpan:v0.1.0
```

## Pull by digest

A tag can name another image later. A digest cannot. Name the digest when
the image of a deployment must stay the same:

```sh
docker pull ghcr.io/opendustpan/opendustpan@sha256:<digest>
```

Read the digest of a release in two places:

- The summary of the release workflow run for the tag, on the GitHub
  Actions page of the repository.
- The output of `docker buildx imagetools inspect ghcr.io/opendustpan/opendustpan:<tag>`.

## Check the version of the image

The server in the image prints its version. The version is the tag of the
release:

```sh
docker run --rm ghcr.io/opendustpan/opendustpan:v0.1.0 --version
```

The output is `v0.1.0`. The binary in the archive of the same release prints
the same value (see the next section).

## Install from a binary archive

Use the archive when you run the server with no container. A release has one
archive for each platform: `linux` and `darwin`, on `amd64` and `arm64`. It
also has the file `SHA256SUMS`, with one line for each archive.

The archive holds the server binary, `LICENSE`, `README.md` and the licenses
of the linked code. It holds no console. For the console, use the image, or
start the server with `--console-dir` and a console release tarball.

`SHA256SUMS` is no signature. The check below finds a download that is
damaged. It does not prove who made the file. Get `SHA256SUMS` and the archive
from the same release page.

Download the archive and `SHA256SUMS` for your platform. Replace `<version>`
with the release tag, `<os>` with `linux` or `darwin`, and `<arch>` with
`amd64` or `arm64`:

```sh
curl -fsSLO https://github.com/OpenDustpan/OpenDustpan/releases/download/<version>/opendustpan_<version>_<os>_<arch>.tar.gz
curl -fsSLO https://github.com/OpenDustpan/OpenDustpan/releases/download/<version>/SHA256SUMS
```

Check the archive against its line in `SHA256SUMS`. The command exits with a
non-zero status when the checksum does not match. Then download the archive
again:

```sh
grep ' opendustpan_<version>_<os>_<arch>.tar.gz$' SHA256SUMS | sha256sum -c -
```

On macOS, use `shasum -a 256 -c -` in place of `sha256sum -c -`.

Unpack the archive into its own directory:

```sh
mkdir opendustpan-<version>
tar -xzf opendustpan_<version>_<os>_<arch>.tar.gz -C opendustpan-<version>
```

Go into the directory and read the version. It is the tag of the release:

```sh
cd opendustpan-<version>
./opendustpan --version
```

Make the secrets and start the server. `serve` has no default key and no
default database, so it stops if you set none:

```sh
export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
export DUSTPAN_KEY_ENCRYPTION_KEY=$(openssl rand -base64 32)
./opendustpan serve --listen 127.0.0.1:8080 --database sqlite:dustpan.db
```

Save both values in a password manager. A lost key-encryption key loses every
secret and every OpenTofu state. In a second shell, check that the server is
ready:

```sh
curl -fsS http://127.0.0.1:8080/health/ready
```

For the next steps, with TLS, see the [quickstart](quickstart.md).

## Install with Helm

Make the two Secrets first. The chart refuses an install without both
names: `database.existingSecret.name` holds the PostgreSQL URL and
`keyEncryptionKey.existingSecret.name` holds the key from
`opendustpan key generate`.

```sh
kubectl create secret generic opendustpan-database \
  --from-literal=database-url='postgres://opendustpan@db.example:5432/opendustpan?sslmode=verify-full'
kubectl create secret generic opendustpan-key-encryption-key \
  --from-file=key-encryption-key=<file from opendustpan key generate --out>
```

Install with the chart default registry:

```sh
helm install opendustpan oci://ghcr.io/opendustpan/charts/opendustpan --version 0.1.0 \
  --set database.existingSecret.name=opendustpan-database \
  --set keyEncryptionKey.existingSecret.name=opendustpan-key-encryption-key
```

Or switch to Docker Hub with one more value:

```sh
helm install opendustpan oci://ghcr.io/opendustpan/charts/opendustpan --version 0.1.0 \
  --set image.repository=docker.io/opendustpan/opendustpan \
  --set database.existingSecret.name=opendustpan-database \
  --set keyEncryptionKey.existingSecret.name=opendustpan-key-encryption-key
```

## Maintainer setup: Docker Hub secrets

This section is for maintainers who publish releases. Operators who only
install the chart skip it.

The release workflow pushes to Docker Hub with two push-only repository
secrets, `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`. Before the first tag
run, a maintainer adds them with:

```sh
gh secret set DOCKERHUB_USERNAME --repo OpenDustpan/OpenDustpan
gh secret set DOCKERHUB_TOKEN --repo OpenDustpan/OpenDustpan
```

When a secret is missing, the push step fails with an error that names the
setup step above. When the token is expired or has no push scope, the login
step fails with an error that names the rotation step:
`gh secret set DOCKERHUB_TOKEN --repo OpenDustpan/OpenDustpan`.
