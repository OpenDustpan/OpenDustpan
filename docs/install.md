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

## Install with Helm

Install with the chart default registry:

```sh
helm install opendustpan oci://ghcr.io/opendustpan/charts/opendustpan --version 0.1.0
```

Or switch to Docker Hub with one value:

```sh
helm install opendustpan oci://ghcr.io/opendustpan/charts/opendustpan --version 0.1.0 \
  --set image.repository=docker.io/opendustpan/opendustpan
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
