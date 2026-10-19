# Helm chart values

This page lists every key of `values.yaml` in the chart in `deploy/helm/opendustpan/`, with the default value. `values.schema.json` checks the values, and an unknown key fails the install. The [Helm guide](../guides/kubernetes.md) shows how to use them.

A test compares this page with `values.yaml`. The test fails when a key is not on this page.

## `image`

| Key | Default | Description |
| --- | --- | --- |
| `image.repository` | `ghcr.io/opendustpan/opendustpan` | The image of the server. The image holds the console files in `/console`. |
| `image.tag` | `""` | Empty means the appVersion of the chart. The schema refuses `latest`. |
| `image.digest` | `""` | A digest, for example `sha256:<64 hex digits>`. When it is set, the pod uses `<repository>@<digest>` and ignores tag. Set it for production. |
| `image.pullPolicy` | `IfNotPresent` | The image pull policy of the pod. |

## `imagePullSecrets`

| Key | Default | Description |
| --- | --- | --- |
| `imagePullSecrets` | `[]` | Names of Secrets of type kubernetes.io/dockerconfigjson for the image pull. |

## `replicas`

| Key | Default | Description |
| --- | --- | --- |
| `replicas` | `1` | The number of server instances. The schema accepts 1 only in this release, because the server work for several instances is not complete. |

## `database`

The external PostgreSQL database. The chart runs no database.

| Key | Default | Description |
| --- | --- | --- |
| `database.existingSecret.name` | `""` | Required. The name of a Secret in the namespace of the release. |
| `database.existingSecret.key` | `database-url` | The key in the Secret that holds the database URL, for example `postgres://opendustpan@db.example:5432/opendustpan?sslmode=verify-full`. The URL holds the password, so it never goes in a value of this file. |

## `keyEncryptionKey`

The key-encryption key. The server stops at start without one. Make it once with `opendustpan key generate --out <file>`, put it in a Secret, and keep a copy outside the cluster. A lost key loses every secret. The pod gets the key in `DUSTPAN_KEY_ENCRYPTION_KEY` from a `secretKeyRef`, because a Secret volume gives the file a group mode that `serve` refuses.

| Key | Default | Description |
| --- | --- | --- |
| `keyEncryptionKey.existingSecret.name` | `""` | Required. The name of a Secret in the namespace of the release. |
| `keyEncryptionKey.existingSecret.key` | `key-encryption-key` | The key in the Secret that holds the base64 key. |

## `adminToken`

Optional bootstrap admin tokens, comma-separated, from a Secret. The pod gets them in `DUSTPAN_ADMIN_TOKEN`. Empty name means no bootstrap admin token.

| Key | Default | Description |
| --- | --- | --- |
| `adminToken.existingSecret.name` | `""` | The name of a Secret with the bootstrap admin tokens. Empty means no bootstrap admin token. |
| `adminToken.existingSecret.key` | `admin-token` | The key in the Secret that holds the tokens. |

## `s3`

The connection to S3-compatible storage. The blob store and an s3 backup target use the same endpoint, region and credentials.

| Key | Default | Description |
| --- | --- | --- |
| `s3.endpoint` | `""` | Empty means AWS. |
| `s3.region` | `""` | Empty means us-east-1. |
| `s3.credentials.existingSecret.name` | `""` | The name of a Secret with the S3 access key and secret key. Empty means the default credential chain of the server, for example a role that `serviceAccount.annotations` gives to the pod. |
| `s3.credentials.existingSecret.accessKeyIDKey` | `AWS_ACCESS_KEY_ID` | The key in the Secret that holds the access key ID. |
| `s3.credentials.existingSecret.secretAccessKeyKey` | `AWS_SECRET_ACCESS_KEY` | The key in the Secret that holds the secret access key. |

## `blobStore`

Where the server keeps state content.

| Key | Default | Description |
| --- | --- | --- |
| `blobStore.type` | `fs` | `fs` keeps it on a PersistentVolumeClaim that one pod mounts. `s3` keeps it in a bucket. |
| `blobStore.fs.persistence.existingClaim` | `""` | The name of a PersistentVolumeClaim that exists. Empty means the chart makes a claim. |
| `blobStore.fs.persistence.storageClassName` | `""` | Empty means the default storage class of the cluster. |
| `blobStore.fs.persistence.size` | `10Gi` | The size of the claim that the chart makes. |
| `blobStore.fs.persistence.accessMode` | `ReadWriteOnce` | The access mode of the claim that the chart makes. |
| `blobStore.s3.bucket` | `""` | Required when type is `s3`. |
| `blobStore.s3.prefix` | `""` | A key prefix in the bucket. |

## `backup`

Backups of the whole installation.

| Key | Default | Description |
| --- | --- | --- |
| `backup.target.type` | `none` | `none`: no backup target. `opendustpan doctor` warns about it. `dir`: a PersistentVolumeClaim that one pod mounts. `s3`: a bucket, with the connection in s3 above. |
| `backup.target.dir.persistence.existingClaim` | `""` | The name of a PersistentVolumeClaim that exists. Empty means the chart makes a claim. |
| `backup.target.dir.persistence.storageClassName` | `""` | Empty means the default storage class of the cluster. |
| `backup.target.dir.persistence.size` | `20Gi` | The size of the claim that the chart makes. |
| `backup.target.dir.persistence.accessMode` | `ReadWriteOnce` | The access mode of the claim that the chart makes. |
| `backup.target.s3.bucket` | `""` | Required when type is `s3`. Use a bucket that is not the bucket of the blob store. |
| `backup.target.s3.prefix` | `""` | A key prefix in the bucket. |
| `backup.schedule.enabled` | `false` | `true` adds the backup CronJob. It needs a backup target that is not `none` and `backup.schedule.tokenSecret`. |
| `backup.schedule.cron` | `"17 2 * * *"` | Cron syntax, in the time zone of timeZone. |
| `backup.schedule.timeZone` | `Etc/UTC` | The time zone of `cron`. |
| `backup.schedule.tokenSecret.name` | `""` | Required when `enabled` is `true`. The name of the Secret with the backup token. |
| `backup.schedule.tokenSecret.key` | `token` | The key in the Secret that holds the token. |
| `backup.schedule.caSecret.name` | `""` | The name of a Secret with the PEM bundle of the certificate authority. Empty means the system trust store of the image. |
| `backup.schedule.caSecret.key` | `ca.crt` | The key in the Secret that holds the bundle. The CronJob mounts it at `/ca/<key>`. |
| `backup.schedule.successfulJobsHistoryLimit` | `3` | The number of finished backup Jobs to keep. |
| `backup.schedule.failedJobsHistoryLimit` | `3` | The number of failed backup Jobs to keep. |
| `backup.schedule.activeDeadlineSeconds` | `21600` | The time in seconds after which Kubernetes stops a backup Job that is still running. The default is 6 hours. |
| `backup.schedule.resources.requests.cpu` | `10m` | The CPU request of the CronJob container. |
| `backup.schedule.resources.requests.memory` | `32Mi` | The memory request of the CronJob container. |
| `backup.schedule.resources.limits.memory` | `128Mi` | The memory limit of the CronJob container. |

## `console`

The web console. The image holds its files in `/console`.

| Key | Default | Description |
| --- | --- | --- |
| `console.enabled` | `true` | `true` serves the console files from `/console`. |
| `console.runtimeConfig` | `{}` | The runtime configuration of the console, served as /config.json. The browser reads this file, so it holds no secret. Empty means the file of the console release. |

## `server`

Settings of `opendustpan serve`.

| Key | Default | Description |
| --- | --- | --- |
| `server.publicURL` | `""` | The external base URL, for example https://dustpan.example. Set it when an ingress or a load balancer is in front of the server. Console sign-in needs it. |
| `server.trustedProxies` | `[]` | The addresses (CIDRs) of the proxies that may set the client address header, for example the pod CIDR of the ingress controller. |
| `server.trustedProxyHeader` | `x-forwarded-for` | The header that holds the client address. It applies when `trustedProxies` is not empty. |
| `server.region` | `""` | Empty means the server default. |
| `server.logLevel` | `info` | The log level of the server. |
| `server.logFormat` | `json` | The log format of the server. |
| `server.tls.existingSecret` | `""` | The name of a Secret of type `kubernetes.io/tls`. Empty means plain HTTP in the cluster. |
| `server.extraEnv` | `[]` | More environment variables for settings that this file does not name. The schema refuses the variables that the chart sets, `DUSTPAN_DEV`, the `PG` variables, `GODEBUG`, and the AWS endpoint and region variables. It also refuses a plain `value` for a name that holds a secret: use valueFrom with a secretKeyRef for those. valueFrom holds only a secretKeyRef. |

## `service`

| Key | Default | Description |
| --- | --- | --- |
| `service.type` | `ClusterIP` | The type of the Service. |
| `service.port` | `0` | 0 means 80 with plain HTTP and 443 with built-in TLS. |
| `service.annotations` | `{}` | Annotations of the Service. |

## `ingress`

| Key | Default | Description |
| --- | --- | --- |
| `ingress.enabled` | `false` | `true` makes an Ingress. |
| `ingress.className` | `""` | The ingress class. Empty means the default class of the cluster. |
| `ingress.annotations` | `{}` | Annotations of the Ingress. |
| `ingress.hosts` | `[]` | Hosts and paths. Each item has `host` and `paths`, and each path has `path` and `pathType`. |
| `ingress.tls` | `[]` | TLS entries. Each item has `secretName` and `hosts`. |

## `probes`

The liveness and startup probes use `GET /health/live`. The readiness probe uses `GET /health/ready`. The server runs its start-up steps, the schema migrations included, before it listens. The startup probe gives these steps `periodSeconds * failureThreshold` seconds (10 minutes by default).

| Key | Default | Description |
| --- | --- | --- |
| `probes.startup.periodSeconds` | `10` | The time in seconds between startup probes. |
| `probes.startup.timeoutSeconds` | `2` | The timeout in seconds of a startup probe. |
| `probes.startup.failureThreshold` | `60` | The number of failed startup probes after which Kubernetes restarts the container. |
| `probes.liveness.periodSeconds` | `10` | The time in seconds between liveness probes. |
| `probes.liveness.timeoutSeconds` | `2` | The timeout in seconds of a liveness probe. |
| `probes.liveness.failureThreshold` | `3` | The number of failed liveness probes after which Kubernetes restarts the container. |
| `probes.readiness.periodSeconds` | `10` | The time in seconds between readiness probes. |
| `probes.readiness.timeoutSeconds` | `3` | The timeout in seconds of a readiness probe. |
| `probes.readiness.failureThreshold` | `3` | The number of failed readiness probes after which Kubernetes marks the pod not ready. |

## `resources`

| Key | Default | Description |
| --- | --- | --- |
| `resources.requests.cpu` | `100m` | The CPU request of the server container. |
| `resources.requests.memory` | `256Mi` | The memory request of the server container. |
| `resources.limits.memory` | `1Gi` | The memory limit of the server container. The chart sets no CPU limit. |

## `podSecurityContext`

The pod security context. The schema refuses root and a root group.

| Key | Default | Description |
| --- | --- | --- |
| `podSecurityContext.runAsNonRoot` | `true` | The schema holds this value at `true`. |
| `podSecurityContext.runAsUser` | `65532` | The user ID. The schema refuses 0. |
| `podSecurityContext.runAsGroup` | `65532` | The group ID. The schema refuses 0. |
| `podSecurityContext.fsGroup` | `65532` | The group ID of the volumes. The schema refuses 0. |
| `podSecurityContext.fsGroupChangePolicy` | `OnRootMismatch` | When Kubernetes changes the group of a volume. |
| `podSecurityContext.seccompProfile.type` | `RuntimeDefault` | The seccomp profile type. |

## `containerSecurityContext`

The container security context. The schema holds each value fixed. The root filesystem is read-only, and the chart mounts an emptyDir on /tmp.

| Key | Default | Description |
| --- | --- | --- |
| `containerSecurityContext.runAsNonRoot` | `true` | The schema holds this value at `true`. |
| `containerSecurityContext.allowPrivilegeEscalation` | `false` | The schema holds this value at `false`. |
| `containerSecurityContext.privileged` | `false` | The schema holds this value at `false`. |
| `containerSecurityContext.readOnlyRootFilesystem` | `true` | The schema holds this value at `true`. |
| `containerSecurityContext.capabilities.drop` | `[ALL]` | The capabilities to drop. The schema holds the list at `[ALL]`. |
| `containerSecurityContext.seccompProfile.type` | `RuntimeDefault` | The schema holds this value at `RuntimeDefault`. |

## `serviceAccount`

| Key | Default | Description |
| --- | --- | --- |
| `serviceAccount.create` | `true` | `true` makes a ServiceAccount. |
| `serviceAccount.name` | `""` | Empty means the full name of the release. |
| `serviceAccount.annotations` | `{}` | For example the role annotation of a cloud provider for S3 or KMS. |

## `automountServiceAccountToken`

| Key | Default | Description |
| --- | --- | --- |
| `automountServiceAccountToken` | `false` | The server does not call the Kubernetes API, so the pod gets no token. The schema holds this value at false. |

## `networkPolicy`

| Key | Default | Description |
| --- | --- | --- |
| `networkPolicy.enabled` | `true` | `true` makes a NetworkPolicy. |
| `networkPolicy.ingressFrom` | `[]` | Sources that may reach the server port. Empty means every source. Each item is a NetworkPolicyPeer with at least one key. For example, use a `namespaceSelector` for the namespace of the ingress controller, or for the namespace of Prometheus. When the list is not empty, the chart adds a rule for the pods of the backup CronJob. |
| `networkPolicy.allowAllEgress` | `false` | false allows DNS, TCP 5432 and TCP 443 to any address, and the rules in extraEgress. The server reaches the database, S3 and sign-in providers at addresses that the chart does not know, so the chart limits the ports. true allows all egress. |
| `networkPolicy.dns.namespaceSelector.matchLabels` | `kubernetes.io/metadata.name: kube-system` | The labels of the namespace of the DNS server. |
| `networkPolicy.dns.podSelector.matchLabels` | `k8s-app: kube-dns` | The labels of the pods of the DNS server. |
| `networkPolicy.extraEgress` | `[]` | More egress rules, for other ports. Each item is a NetworkPolicyEgressRule. For example a webhook on port 8443, or the metadata server: |

## `metrics`

| Key | Default | Description |
| --- | --- | --- |
| `metrics.serviceMonitor.enabled` | `false` | `true` makes a ServiceMonitor. The Prometheus Operator CRDs must exist. |
| `metrics.serviceMonitor.interval` | `30s` | The scrape interval. |
| `metrics.serviceMonitor.scrapeTimeout` | `10s` | The scrape timeout. |
| `metrics.serviceMonitor.labels` | `{}` | More labels of the ServiceMonitor, for the selector of your Prometheus. |
| `metrics.serviceMonitor.tokenSecret.name` | `""` | Required when `enabled` is `true`. The name of the Secret with the metrics token. |
| `metrics.serviceMonitor.tokenSecret.key` | `token` | The key in the Secret that holds the token. |
| `metrics.serviceMonitor.tlsConfig.caSecret.name` | `""` | The name of a Secret with the PEM bundle of the certificate authority. Empty means the defaults of Prometheus. |
| `metrics.serviceMonitor.tlsConfig.caSecret.key` | `ca.crt` | The key in the Secret that holds the bundle. |
| `metrics.serviceMonitor.tlsConfig.serverName` | `""` | The host name that Prometheus checks in the certificate. Empty means the host name of the target. |

## `podAnnotations`

| Key | Default | Description |
| --- | --- | --- |
| `podAnnotations` | `{}` | The schema refuses the AppArmor and seccomp annotations here. |

## `podLabels`

| Key | Default | Description |
| --- | --- | --- |
| `podLabels` | `{}` | More labels of the server pod. |

## `nodeSelector`

| Key | Default | Description |
| --- | --- | --- |
| `nodeSelector` | `{}` | The node selector of the server pod. |

## `tolerations`

| Key | Default | Description |
| --- | --- | --- |
| `tolerations` | `[]` | The tolerations of the server pod. |

## `affinity`

| Key | Default | Description |
| --- | --- | --- |
| `affinity` | `{}` | The affinity of the server pod. |

## `topologySpreadConstraints`

| Key | Default | Description |
| --- | --- | --- |
| `topologySpreadConstraints` | `[]` | The topology spread constraints of the server pod. |

## `priorityClassName`

| Key | Default | Description |
| --- | --- | --- |
| `priorityClassName` | `""` | The priority class of the server pod. Empty means none. |

## `terminationGracePeriodSeconds`

| Key | Default | Description |
| --- | --- | --- |
| `terminationGracePeriodSeconds` | `30` | The time in seconds that the pod has to stop. |

## Notes on nested keys

- `s3.credentials.existingSecret`: an empty name means the default credential chain of the server, for example a role that `serviceAccount.annotations` gives to the pod.
- `backup.schedule`: a CronJob that runs the client command `opendustpan backup` against the Service of the release. It needs a backup target that is not `none`. Do not use a bootstrap admin token for `tokenSecret`.
- `backup.schedule.caSecret`: the CronJob mounts the Secret and passes `--ca-file`. The certificate of the server must cover `<fullname>.<namespace>.svc`.
- `server.tls`: with a Secret, the server serves TLS itself and the probes use HTTPS. OpenTofu needs TLS at the client edge.
- `networkPolicy.dns`: the DNS server that the egress rule for port 53 allows.
- `metrics.serviceMonitor`: scrapes `GET /api/server/metrics`. Do not use a bootstrap admin token for `tokenSecret`.
- `metrics.serviceMonitor.tlsConfig`: the certificate must cover `<fullname>.<namespace>.svc`, or `serverName` must name a host that it covers.
