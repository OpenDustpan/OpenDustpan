# Install OpenDustpan v0.1.0

This page is for operators who install v0.1.0. It shows how to pull the
v0.1.0 image and install it with Helm.

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
gh api repos/OpenDustpan/OpenDustpan/releases --jq '.[] | select(.tag_name=="<tag>") | .id'
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
