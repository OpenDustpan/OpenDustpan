# Install OpenDustpan v0.1.0

This page is for operators who install v0.1.0. It shows how to pull the
v0.1.0 image, what to use in place of a binary archive, and how to install it
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

The output is `v0.1.0`.

## Install from a binary archive

Binary archives and the file `SHA256SUMS` are not available yet. No release
exists, and the release workflow does not upload an archive. Do not look for a
download link: none works.

Use one of these ways today:

- Build the server from source. The [quickstart](quickstart.md) shows the
  commands, with a key-encryption key, a database and a first start.
- Build the container image from your checkout with
  `./scripts/build-image-local.sh`. The quickstart shows how to run it.

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
