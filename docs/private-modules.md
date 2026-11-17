# Private modules

This page shows how a platform team publishes an OpenTofu module to
OpenDustpan and how `tofu init` installs it.

## Publish a module

A module is one archive. The registry stores the archive as you send it,
and `tofu init` downloads it from OpenDustpan and unpacks it.

You need a token with the action `registry:publish`. A module has no
signature and no signing key.

First upload the archive:

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @vpc.tar.gz \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/uploads
```

Then name the upload and the version:

```sh
curl -X PUT -H "Authorization: token $DUSTPAN_TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"upload": "<token>"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/modules/vpc/aws/versions/1.0.0
```

## Use the module

Write its address with the host of the server and the namespace, and run
`tofu init`:

```hcl
module "vpc" {
  source  = "dustpan.example.com/acme/vpc/aws"
  version = "~> 1.0"
}
```

List the modules of a namespace, and the versions of one module:

```sh
curl -H "Authorization: token $DUSTPAN_TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/modules
curl -H "Authorization: token $DUSTPAN_TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/registry/modules/vpc/aws/versions
```
