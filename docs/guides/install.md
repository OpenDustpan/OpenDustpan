# Install OpenDustpan from the container image

No release exists yet. The repository has no tag, so no image and no chart
are published. This page shows how to build the image from a checkout and
install it with the Helm chart in the checkout.
The page also shows what to use in place of a binary archive.

## Build the image

Build the image from the checkout. The script tags it `opendustpan:local`:

```sh
./scripts/build-image-local.sh
```

The image holds a one-page placeholder console, because no console release
exists yet. Push the image to a registry that your cluster can reach, and
name it in the values below.

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

Make the three Secrets first. The chart refuses an install without all
three names: `database.existingSecret.name` holds the PostgreSQL URL,
`keyEncryptionKey.existingSecret.name` holds the key from
`opendustpan key generate`, and `adminToken.existingSecret.name` holds the
bootstrap admin token. The server exits with `no admin token is set` without
the token.

```sh
kubectl create secret generic opendustpan-database \
  --from-literal=database-url='postgres://opendustpan@db.example:5432/opendustpan?sslmode=verify-full'
kubectl create secret generic opendustpan-key-encryption-key \
  --from-file=key-encryption-key=<file from opendustpan key generate --out>
kubectl create secret generic opendustpan-admin-token \
  --from-literal=admin-token="$(openssl rand -hex 32)"
```

Install from the checkout. Set `image.repository` and `image.tag` to the
image that you pushed:

```sh
helm install opendustpan deploy/helm/opendustpan \
  --set image.repository=<registry>/opendustpan \
  --set image.tag=<tag> \
  --set database.existingSecret.name=opendustpan-database \
  --set keyEncryptionKey.existingSecret.name=opendustpan-key-encryption-key \
  --set adminToken.existingSecret.name=opendustpan-admin-token
```

The [Helm guide](kubernetes.md) shows the other Secrets, storage, upgrade
and rollback steps.

## After the first release

The release workflow publishes the image to the forge container registry and
to Docker Hub, with one digest. Use a digest when the image of a deployment
must stay the same. Read the digest from the summary of the release workflow
run for the tag, or from the output of
`docker buildx imagetools inspect <image>:<tag>`.

### Check the version of the image

The server in the image prints its version. The version is the tag of the
release:

```sh
docker run --rm ghcr.io/opendustpan/opendustpan:v0.1.0 --version
```

The output is `v0.1.0`.

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

## Maintainer: publish the release archives

This section is for maintainers who publish releases. Operators who only
install the chart skip it.

### What the run does

A push of a tag `vX.Y.Z` by a person with release permission starts the
release run. It is the only start. The run does these steps in order:

1. It builds the four archives and `SHA256SUMS`, and checks them.
2. It pushes the image and the Helm chart.
3. It creates a draft release.

The workflow never publishes. A person publishes the draft.

The draft holds five files:

- `opendustpan_<tag>_linux_amd64.tar.gz`
- `opendustpan_<tag>_linux_arm64.tar.gz`
- `opendustpan_<tag>_darwin_amd64.tar.gz`
- `opendustpan_<tag>_darwin_arm64.tar.gz`
- `SHA256SUMS`

The release page shows no file to operators until a person publishes the draft.
The chart is not on the release page. It is an OCI artifact in the registry.

### Check and publish the draft

Find the draft with `gh release list --repo OpenDustpan/OpenDustpan`, or on
the Releases page. Only people with write access see a draft.

Before you publish, read the five file names. Read `SHA256SUMS`: each line is
`<64 hex>  <name>`, with one line for each archive. Then publish:

```sh
gh release edit <tag> --draft=false --repo OpenDustpan/OpenDustpan
```

SHA256SUMS is not a signature. It shows that a file is the same as the file
that the run built. It does not show who built it.

### When the `release-draft` job fails

The failed job can leave a draft with some files. Delete the draft if it
exists:

```sh
gh release delete <tag> --repo OpenDustpan/OpenDustpan
```

If this command does not find a draft by tag, delete it by release id. Get the
id first:

```sh
gh api repos/OpenDustpan/OpenDustpan/releases --paginate --jq '.[] | select(.tag_name=="<tag>") | .id'
gh api -X DELETE repos/OpenDustpan/OpenDustpan/releases/<id>
```

Then use "Re-run failed jobs". Do not use "Re-run all jobs". The image and
chart steps refuse a second push of a released tag, so a full re-run stops
before the draft job.

The upload script creates nothing when a release for the tag exists, draft or
published. Its message names `gh release list`. Find the release, then delete
it or publish it.

### What to read in the first real run

No test shows these before the first tag. Read them in the run log:

- Does the `--verify-tag` flag work?
- Does the delete of a draft by tag work?
- Did the archive files move from the build job to the check job?
