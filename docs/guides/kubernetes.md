# Install with Helm

This guide shows how to install `opendustpan serve` on Kubernetes with the Helm chart in `deploy/helm/opendustpan/`. We designed the chart against the "restricted" profile of the Kubernetes Pod Security Standards. This guide makes no claim of compliance with any standard.

The chart always runs `opendustpan serve`. It uses an external PostgreSQL database and runs no database. It has no SQLite mode. To try OpenDustpan on a laptop, use `opendustpan dev` or the image.

## What you need

- A Kubernetes cluster and the `kubectl` and `helm` commands.
- A PostgreSQL database that the cluster can reach, and its URL.
- Storage for blobs: a PersistentVolumeClaim, or an S3-compatible bucket.

## Make the Secrets

The chart never holds a secret. Each secret comes from a Secret that exists before the install. The install fails when the names of the first two Secrets are empty.

1. Make the key-encryption key one time. Keep a copy outside the cluster. A lost key loses every secret.

   ```sh
   opendustpan key generate --out key-encryption-key
   ```

2. Make the two Secrets that every install needs.

   ```sh
   kubectl create secret generic opendustpan-database \
     --from-literal=database-url='postgres://opendustpan@db.example:5432/opendustpan?sslmode=verify-full'
   kubectl create secret generic opendustpan-key-encryption-key \
     --from-file=key-encryption-key=key-encryption-key
   ```

The two Secrets must not be the same Secret and key. The templates refuse that case.

Other Secrets are optional:

| Secret | Value | Use |
| --- | --- | --- |
| Bootstrap admin tokens | `adminToken.existingSecret` | The first administrator tokens, comma-separated. |
| S3 credentials | `s3.credentials.existingSecret` | Access key and secret key. With no Secret, the server uses its default credential chain, for example a role that `serviceAccount.annotations` gives to the pod. |
| Built-in TLS | `server.tls.existingSecret` | A `kubernetes.io/tls` Secret. |
| Backup token | `backup.schedule.tokenSecret` | An access token that may start a backup. Do not use a bootstrap admin token. |
| Metrics token | `metrics.serviceMonitor.tokenSecret` | An access token that may read `/metrics`. Do not use a bootstrap admin token. |

## Install

Make a values file. This file is the smallest install: blobs on a PersistentVolumeClaim.

```yaml
database:
  existingSecret:
    name: opendustpan-database
keyEncryptionKey:
  existingSecret:
    name: opendustpan-key-encryption-key
```

```sh
helm install dustpan oci://<registry>/opendustpan --version <version> -f values.yaml
```

We plan to publish the chart as an OCI artifact in the registry of the image. The registry is open. Until it exists, install from a checkout: `helm install dustpan deploy/helm/opendustpan -f values.yaml`.

For production, set `image.digest` to the digest of the image. The pod then uses `<repository>@<digest>` and ignores the tag. The chart refuses the tag `latest`.

The directory `deploy/helm/opendustpan/ci/` has three example values files:

- Blobs on a claim.
- Blobs in S3, with a backup schedule, behind an ingress.
- Built-in TLS, with a backup claim.

## Storage

| Value | Result |
| --- | --- |
| `blobStore.type: fs` | Blobs go on a PersistentVolumeClaim at `/data/blobs`. The chart makes the claim, or you name one in `blobStore.fs.persistence.existingClaim`. |
| `blobStore.type: s3` | Blobs go in `blobStore.s3.bucket`. The endpoint and the region come from `s3`. |

The claim that the chart makes has the annotation `helm.sh/resource-policy: keep`, so `helm uninstall` does not delete your data. No database files go on a claim.

## Backups

`backup.target.type` is `none`, `dir` or `s3`. With `none`, `opendustpan doctor` warns. With `dir`, the chart mounts a second claim at `/backups`. With `s3`, use a bucket that is not the bucket of the blob store.

`backup.schedule.enabled: true` adds a CronJob. It runs `opendustpan backup` against the Service of the release, once a day by default, with the token from `backup.schedule.tokenSecret`. The CronJob does not mount the key-encryption key or the database URL, and it gets no S3 credentials. A restore is an API call. The chart has no restore Job. The backup guide shows the restore steps.

The CronJob calls the server through the Service. With built-in TLS, the certificate must be valid for the Service name, or the CronJob cannot connect.

## Console, ingress and TLS

The default is plain HTTP inside the cluster and TLS at the ingress. Set `ingress.*` to make an Ingress. Set `server.publicURL` to the URL that users see, and `server.trustedProxies` to the addresses of the proxies in front of the server. OpenTofu needs TLS at the edge that the client sees.

With `server.tls.existingSecret`, the server serves TLS itself, and the probes use HTTPS. A certificate renewal needs a restart of the pods.

`console.enabled` is `true` by default. A non-empty `console.runtimeConfig` becomes `/console/config.json`. The browser reads this file, so put no secret in it.

## Settings that the chart does not name

Use `server.extraEnv` for other settings, for example the sign-in settings. A name that ends in `SECRET`, `TOKEN`, `PASSWORD`, `KEY`, `CREDENTIAL` or `CREDENTIALS` needs `valueFrom.secretKeyRef`. The schema refuses a plain `value` for it. The schema also refuses the variables that the chart sets, `DUSTPAN_DEV`, the `PG` variables, `GODEBUG`, and the AWS endpoint and region variables.

The chart cannot set a setting that has only a `_FILE` form, for example the audit chain signing key.

## The pod

The pod runs as user and group 65532, with a read-only root filesystem, no privilege escalation, no capabilities, and the `RuntimeDefault` seccomp profile. The pod gets no Kubernetes API token, because the server does not call that API.

The default requests are 100m CPU and 256Mi of memory, and the memory limit is 1Gi. The chart sets no CPU limit. The architecture guide gives values for larger installs.

The chart makes a NetworkPolicy by default. It lets every source reach the server port, unless you set `networkPolicy.ingressFrom`. It allows DNS, TCP port 5432 and TCP port 443 to any address. For a webhook on another port, or for the metadata server, add a rule in `networkPolicy.extraEgress`. Set `networkPolicy.allowAllEgress: true` to allow all egress.

## One instance

The chart runs one instance. The schema refuses `replicas` above 1, because the server work for several instances is not complete. The [several instances guide](several-instances.md) gives the current status. The Deployment uses the `Recreate` strategy, so each upgrade has a short time with no server.

## Probes

The startup probe and the liveness probe send `GET /health/live`. The readiness probe sends `GET /health/ready`. The server applies pending schema migrations before it listens. The startup probe gives this work 10 minutes by default. For a long migration, raise `probes.startup.failureThreshold`. A startup probe that kills the pod during a migration rolls the migration back.

## Upgrade

The server applies migrations when it starts. The chart has no migration Job and no hook.

1. Read the release notes.
2. Run the upgrade check:

   ```sh
   opendustpan upgrade check --server https://dustpan.example
   ```

3. Make a backup:

   ```sh
   opendustpan backup --server https://dustpan.example
   ```

4. Upgrade:

   ```sh
   helm upgrade dustpan oci://<registry>/opendustpan --version <version> -f values.yaml
   ```

## Roll back

`helm rollback` to a revision with the same app version changes values only. It is safe. A rollback to an older app version works while the minimum binary version of the schema is not above the old binary. Otherwise the old pod refuses to start and names both versions. With the `Recreate` strategy, the server is then down until you roll forward again.

The project has no down migration. To go back past a migration that cannot be undone, restore the backup from before the upgrade into an empty database. Then install the older chart. The [backup guide](backup-restore-upgrade.md) shows the restore steps.

## Check the chart

`make helm-lint` runs `helm lint --strict` and `helm template` through `kubeconform`. It needs `helm` and `kubeconform`, and it does not run in `make check`.
