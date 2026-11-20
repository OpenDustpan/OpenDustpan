# Install with Helm

This guide shows how to install `opendustpan serve` on Kubernetes with the Helm chart in `deploy/helm/opendustpan/`. We designed the chart against the "restricted" profile of the Kubernetes Pod Security Standards. This guide makes no claim of compliance with any standard.

The chart always runs `opendustpan serve`. It uses an external PostgreSQL database and runs no database. It has no SQLite mode. To try OpenDustpan on a laptop, use `opendustpan dev` or the image.

## What you need

- A Kubernetes cluster and the `kubectl` and `helm` commands.
- A PostgreSQL database that the cluster can reach, and its URL.
- Storage for blobs: a PersistentVolumeClaim, or an S3-compatible bucket.

## Make the Secrets

The chart never holds a secret. Each secret comes from a Secret that exists before the install. The install fails when the name of one of the first three Secrets is empty.

1. Make the key-encryption key one time. Keep a copy outside the cluster. A lost key loses every secret.

   ```sh
   opendustpan key generate --out key-encryption-key
   ```

2. Make the three Secrets that every install needs.

   ```sh
   kubectl create secret generic opendustpan-database \
     --from-literal=database-url='postgres://opendustpan@db.example:5432/opendustpan?sslmode=verify-full'
   kubectl create secret generic opendustpan-key-encryption-key \
     --from-file=key-encryption-key=key-encryption-key
   kubectl create secret generic opendustpan-admin-token \
     --from-literal=admin-token="$(openssl rand -hex 32)"
   ```

The database Secret and the key Secret must not be the same Secret and key. The templates refuse that case. The server exits with `no admin token is set` without the admin token, so the chart refuses an empty `adminToken.existingSecret.name`.

Other Secrets are optional:

| Secret | Value | Use |
| --- | --- | --- |
| S3 credentials | `s3.credentials.existingSecret` | Access key and secret key. With no Secret, the server uses its default credential chain, for example a role that `serviceAccount.annotations` gives to the pod. |
| Built-in TLS | `server.tls.existingSecret` | A `kubernetes.io/tls` Secret. |
| Backup token | `backup.schedule.tokenSecret` | An access token that may start a backup. Do not use a bootstrap admin token. |
| Metrics token | `metrics.serviceMonitor.tokenSecret` | An access token that may read `/api/server/metrics`. Do not use a bootstrap admin token. |
| CA bundle | `backup.schedule.caSecret`, `metrics.serviceMonitor.tlsConfig.caSecret` | The PEM bundle of a private certificate authority, for built-in TLS. |

## Install

Make a values file. This file is the smallest install: blobs on a PersistentVolumeClaim, and an ingress or a load balancer in front that ends TLS. Set `server.publicURL` to its `https` URL. [Console, ingress and TLS](#console-ingress-and-tls) gives the other choices.

```yaml
database:
  existingSecret:
    name: opendustpan-database
keyEncryptionKey:
  existingSecret:
    name: opendustpan-key-encryption-key
adminToken:
  existingSecret:
    name: opendustpan-admin-token
server:
  publicURL: https://dustpan.example
```

```sh
helm install dustpan deploy/helm/opendustpan -f values.yaml
```

No release exists yet, so the chart is not published as an OCI artifact. Install from a checkout, as the command above shows. No image is published yet either: build one from the checkout (see the [install guide](install.md)) and set `image.repository` and `image.tag` to it.

For production, set `image.digest` to the digest of the image. The pod then uses `<repository>@<digest>` and ignores the tag. The chart refuses the tag `latest`.

The directory `deploy/helm/opendustpan/ci/` has five example values files:

- `minimal-values.yaml`: one instance, blobs on a claim.
- `s3-values.yaml`: one instance, blobs and backups in S3, behind an ingress.
- `tls-dir-backup-values.yaml`: built-in TLS with a private authority, blobs and backups on claims.
- `ha.yaml`: 3 replicas on S3 blobs and an S3 backup target, with the worker component in its own Deployment.
- `numeric-secret-key-values.yaml`: Secret names and keys that YAML reads as a number or a bool.

The subdirectory `ci/refused/` holds values files that the chart refuses on purpose.

## Storage

| Value | Result |
| --- | --- |
| `blobStore.type: fs` | Blobs go on a PersistentVolumeClaim at `/data/blobs`. The chart makes the claim, or you name one in `blobStore.fs.persistence.existingClaim`. |
| `blobStore.type: s3` | Blobs go in `blobStore.s3.bucket`. The endpoint and the region come from `s3`. |

The claim that the chart makes has the annotation `helm.sh/resource-policy: keep`, so `helm uninstall` does not delete your data. No database files go on a claim.

## Backups

`backup.target.type` is `none`, `dir` or `s3`. With `none`, `opendustpan doctor` warns. With `dir`, the chart mounts a second claim at `/backups`. With `s3`, use a bucket that is not the bucket of the blob store.

`backup.schedule.enabled: true` adds a CronJob. It runs `opendustpan backup` against the Service of the release, once a day by default, with the token from `backup.schedule.tokenSecret`. The CronJob does not mount the key-encryption key or the database URL, and it gets no S3 credentials. A restore is an API call. The chart has no restore Job. The backup guide shows the restore steps.

The CronJob calls the server through the Service. With built-in TLS, the certificate must cover `<fullname>.<namespace>.svc`, or the CronJob cannot connect. When a private certificate authority signed the certificate, put its PEM bundle in a Secret and set `backup.schedule.caSecret.name` and `.key`. The CronJob mounts the Secret and passes `--ca-file`.

Kubernetes stops a backup Job after `backup.schedule.activeDeadlineSeconds`, 6 hours by default. The CronJob container has its own `backup.schedule.resources`: requests of 10m CPU and 32Mi of memory, and a limit of 128Mi of memory.

## Metrics

`metrics.serviceMonitor.enabled: true` makes a ServiceMonitor of the Prometheus Operator for `GET /api/server/metrics`, with the token from `metrics.serviceMonitor.tokenSecret`. The Prometheus Operator CRDs must exist before the install. With built-in TLS and a private certificate authority, set `metrics.serviceMonitor.tlsConfig.caSecret` to the Secret with the PEM bundle. Prometheus reads the Secret from the namespace of the ServiceMonitor. The certificate must cover `<fullname>.<namespace>.svc`. If it covers another host name, set `metrics.serviceMonitor.tlsConfig.serverName` to that name. Add the namespace of Prometheus to `networkPolicy.ingressFrom`, or the NetworkPolicy blocks the scrape.

## Console, ingress and TLS

The default is plain HTTP inside the cluster and TLS at the ingress. Set `ingress.*` to make an Ingress. Set `server.publicURL` to the URL that users see, and `server.trustedProxies` to the addresses of the proxies in front of the server. OpenTofu needs TLS at the edge that the client sees.

The server listens on the pod address, which is not a loopback address, and `serve` refuses plain HTTP there. So the chart needs one of these values, and refuses the install without one:

- `server.publicURL` with `https`: the ingress or the load balancer ends TLS.
- `server.tls.existingSecret`: the server serves TLS itself.
- `server.allowPlainHTTP: true`: the server serves plain HTTP on the pod address. Tokens and state then go over the network with no encryption. Use it only in a trusted network. The server logs a warning at each start.

An Ingress in front of built-in TLS needs the annotation of your ingress controller that makes it connect to the backend with HTTPS. For the NGINX ingress controller, this is `nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"`. Without it, the controller sends plain HTTP to a TLS port.

With `server.tls.existingSecret`, the server serves TLS itself, and the probes use HTTPS. A certificate renewal needs a restart of the pods.

`console.enabled` is `true` by default. A non-empty `console.runtimeConfig` becomes `/console/config.json`. The browser reads this file, so put no secret in it.

## Settings that the chart does not name

Use `server.extraEnv` for other settings, for example the sign-in settings. A name that ends in `SECRET`, `TOKEN`, `PASSWORD`, `KEY`, `CREDENTIAL` or `CREDENTIALS` needs `valueFrom.secretKeyRef`. The schema refuses a plain `value` for it. The schema also refuses the variables that the chart sets, `DUSTPAN_DEV`, the `PG` variables, `GODEBUG`, and the AWS endpoint and region variables.

The chart cannot set a setting that has only a `_FILE` form, for example the audit chain signing key.

## The pod

The pod runs as user and group 65532, with a read-only root filesystem, no privilege escalation, no capabilities, and the `RuntimeDefault` seccomp profile. The pod gets no Kubernetes API token, because the server does not call that API.

The default requests are 100m CPU and 256Mi of memory, and the memory limit is 1Gi. The chart sets no CPU limit. The architecture guide gives values for larger installs.

The chart makes a NetworkPolicy by default. It lets every source reach the server port, unless you set `networkPolicy.ingressFrom`. When you set it, add the namespace of the ingress controller and the namespace of Prometheus. It allows DNS, TCP port 5432 and TCP port 443 to any address. For a webhook on another port, or for the metadata server, add a rule in `networkPolicy.extraEgress`. Set `networkPolicy.allowAllEgress: true` to allow all egress.

- With NodeLocal DNSCache, pods send DNS queries to a node address. Set `networkPolicy.dns` to match that address, or add a rule in `networkPolicy.extraEgress`.
- An S3 endpoint on a port other than 443 needs a rule in `networkPolicy.extraEgress`.

## One instance

By default the chart runs one instance. The Deployment uses the `Recreate` strategy, so each upgrade has a short time with no server. Two pods cannot share the `ReadWriteOnce` claims of the `fs` blob store and the `dir` backup target. A rolling update with one replica would start the new pod while the old pod still runs.

## Several instances

The chart can run several replicas of one installation. The [several instances guide](several-instances.md) describes the layout and the settings of the server. This section lists what the chart does when `replicas` is above 1.

- The Deployment uses `RollingUpdate` with `maxUnavailable` 0 and `maxSurge` 1. A new pod must be ready before Kubernetes stops an old pod.
- The chart makes a PodDisruptionBudget with `minAvailable` 1. Set `podDisruptionBudget.enabled: false` to leave it out.
- With `spreadAcrossNodes: true`, the chart adds a soft topology spread constraint over `kubernetes.io/hostname`. The scheduler prefers a different node for each pod, and it still schedules a pod when it cannot.
- A `preStop` hook waits `preStopSeconds` (15 by default) before the pod gets SIGTERM. At the start of termination the pod leaves the Service endpoints. The wait gives load balancers time to stop sending requests while the pod still answers. The wait is longer than the readiness period. After SIGTERM the server drains for 10 s and needs up to 30 s to finish its requests, so `terminationGracePeriodSeconds` must be at least `preStopSeconds` + 40. The default is 60.
- The chart refuses these settings with a message that names the setting:
  - `blobStore.type: fs`
  - `backup.target.type: dir`
  - a `file:` item in `DUSTPAN_AUDIT_SINK` or `DUSTPAN_LOG_SINK` in `server.extraEnv`
  - a `sqlite:` value in `server.extraEnv`

  The chart cannot read the database URL in its Secret. Put a PostgreSQL URL in it.
- The chart cannot read a sink variable that uses `valueFrom`, as it cannot read the Secret URL. Make sure that the Secret holds no `file:` sink.
- The refusals above are best effort. The chart reads only plain values in `server.extraEnv`, and it matches the prefixes `file:` and `sqlite:` in lowercase only. A value with other letter case, such as `FILE:` or `SQLite:`, passes the check. Do not rely on the chart to refuse these values.
- `podDisruptionBudget.minAvailable` must be less than `replicas`. The chart refuses the install otherwise.

Use an S3-compatible store for the blobs and for the backup target, and one PostgreSQL database. You run the database and the object store, and you keep them available.

To run the background work in separate pods, set `worker.enabled: true`. The chart then makes a second Deployment that runs `--components worker`, with its own `worker.replicas`. The server Deployment runs `--components api`. The worker pods get no Service and no client traffic.

The `ci/ha.yaml` profile of the chart shows a values file with 3 replicas.

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
   helm upgrade dustpan deploy/helm/opendustpan -f values.yaml
   ```

## Roll back

`helm rollback` to a revision with the same app version changes values only. It is safe. A rollback to an older app version works while the minimum binary version of the schema is not above the old binary. Otherwise the old pod refuses to start and names both versions. With the `Recreate` strategy (one replica), the server is then down until you roll forward again.

The project has no down migration. To go back past a migration that cannot be undone, restore the backup from before the upgrade into an empty database. Then install the older chart. The [backup guide](backup-restore-upgrade.md) shows the restore steps.

## Check the chart

`make chart-lint` runs `helm lint --strict` and `helm template` through `kubeconform`, for the oldest and the newest supported Kubernetes version. It needs `helm` and `kubeconform`, and it does not run in `make check`. The tests in `deploy/helm/charttest` compare the output of `helm template` with golden files, and they check that the [values reference](../reference/helm-values.md) names each key of `values.yaml`.

`Chart.yaml` sets `kubeVersion` to `>=1.35.0-0`, the oldest minor version that the Kubernetes project supports. The values reference lists every key.
