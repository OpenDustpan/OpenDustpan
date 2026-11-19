# Install OpenDustpan from the container image

No release exists yet. The repository has no tag, so no image and no chart
are published. This page shows how to build the image from a checkout and
install it with the Helm chart in the checkout.

## Build the image

Build the image from the checkout. The script tags it `opendustpan:local`:

```sh
./scripts/build-image-local.sh
```

The image holds a one-page placeholder console, because no console release
exists yet. Push the image to a registry that your cluster can reach, and
name it in the values below.

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

Install from the checkout. Set `image.repository` and `image.tag` to the
image that you pushed:

```sh
helm install opendustpan deploy/helm/opendustpan \
  --set image.repository=<registry>/opendustpan \
  --set image.tag=<tag> \
  --set database.existingSecret.name=opendustpan-database \
  --set keyEncryptionKey.existingSecret.name=opendustpan-key-encryption-key
```

The [Helm guide](kubernetes.md) shows the other Secrets, storage, upgrade
and rollback steps.

## After the first release

The release workflow publishes the image to the forge container registry and
to Docker Hub, with one digest. Use a digest when the image of a deployment
must stay the same. Read the digest from the summary of the release workflow
run for the tag, or from the output of
`docker buildx imagetools inspect <image>:<tag>`.

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
