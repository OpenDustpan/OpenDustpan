# 246. A Helm chart for `opendustpan serve` with an external PostgreSQL

Status: accepted · 2026-10-19. Decides the Helm chart part of RFC 35. Decides the Helm chart item of ADR 27 decision 8. Correction 2026-10-19: the metrics path is GET /api/server/metrics.

## Context

The project owner decided on 2026-10-17 (label "(owner)"): start the Helm
chart now. The chart can ship with `v0.1.0`. The chart uses an external
PostgreSQL database and does not run a database.

RFC 35 keeps the Helm chart as a draft. ADR 27 decision 8 puts the chart in
"later, not decided". ADR 21 asks whether the chart ships with the first
release. This ADR is the design of the chart. It holds no templates. The
same pull request adds `Chart.yaml`, `values.yaml` and `values.schema.json`
in `deploy/helm/opendustpan/`, and two values files for CI in `ci/`.

A decision with the label "(product manager)" has a "Benchmark:" line. The
product manager decided under the owner's rule: follow what Google, AWS,
PCI DSS, SOC 2 and ISO 27001 would do. A benchmark names what another chart
does, as we read its chart in 2026. It is not a claim about that project.

Facts that this design depends on:

- `serve` needs an explicit key-encryption key and an explicit database,
  and stops without them (ADR 238 decisions 1 and 2). The image fails
  closed in the same way (ADR 238 decision 13).
- `serve` claims the installation kind `production`, and refuses dev data
  and `DUSTPAN_DEV` (ADR 237, ADR 238 decision 5).
- Only `serve` opens the database, the blob store, a backup target or the
  key-encryption key. Every other command is a client of the REST API
  (ADR 39 decisions 1 and 2).
- `serve` applies pending migrations at start. On PostgreSQL the version
  check runs under the migration lock (ADR 33 decisions 1 and 5, ADR 39
  decision 5). No `migrate` command exists. No down migration exists
  (ADR 33 decision 6).
- Several instances on one PostgreSQL database coordinate only in the
  database: a lease with a fencing token for each operation, schedule rows,
  a write-path installation check, and expand and contract with K = 2
  (ADR 72 decisions 2, 3, 4 and 11). OpenTofu locks and the namespace fence
  live in the database (ADR 44). The server has no peer protocol and no
  instance registry (ADR 72 decisions 2 and 9).
- The guide for several instances says that the layout is not supported
  yet. The server work of ADR 72 is not complete.
- The probes are `GET /health/live` and `GET /health/ready` (ADR 34).
  Readiness pings the store and the blob store with a limit of 2 seconds
  (ADR 32 decision 3). `Server.Run` runs the start-up steps before it
  listens (ADR 238 decision 14).
- Metrics are at `GET /metrics` with a bearer token and the action of
  ADR 32 decision 4 (ADR 210).
- The image is distroless static `nonroot` (user ID 65532), with `/data`
  and the console files in `/console` (ADR 27 decision 5, ADR 237
  decision 7). The console reads `/config.json` at load time (ADR 5).
- A backup target is `dir:/path` or `s3://<bucket>[/<prefix>]` (ADR 30
  decision 10). A backup and a restore run through the API (ADR 38,
  ADR 39). The portable format is in ADR 42.
- The KMS setting for the key-encryption key does not exist yet. ADR 217
  names it, and RFC 76 holds the choice of the KMS client.
- ADR 136 gives the Kubernetes coordinator its own Helm chart.

## Decision

### Where the chart lives and how it ships

1. **A directory in this repository (product manager).** The chart lives in
   `deploy/helm/opendustpan/`. The tag workflow that builds the binaries and
   the image also packages the chart. The chart changes in the same pull
   request as a setting or a probe that it uses.
   Benchmark: the cert-manager and Cilium charts live in the repository of
   their application. The Grafana, Keycloak (codecentric) and Bitnami charts
   live in separate chart repositories, and their chart versions then move
   apart from the application.
2. **One version for the chart and the app (product manager).** The chart
   version and `appVersion` are the release tag without its `v`. Tag
   `v0.1.0` gives chart `0.1.0` with app `0.1.0`. The tag workflow sets both
   with `helm package --version --app-version`. `Chart.yaml` on `main` holds
   the version of the next minor release. A fix to the chart only is a
   patch release (ADR 75 decision 2).
   Benchmark: cert-manager and Cilium release their chart with the same
   version as the application.
3. **An OCI artifact (product manager).** The tag workflow pushes the chart
   as an OCI artifact to the registry of the image. It publishes no
   `index.yaml` on a web page. The registry and signing are not decided
   (ADR 27 decision 8). See "Open", item 2.
   Benchmark: Bitnami and Harbor publish charts as OCI artifacts.

### What the chart runs

4. **Always `serve`.** The container runs `/opendustpan serve` with
   arguments that the chart sets. No value selects a command, and no value
   selects `dev`. The schema refuses `DUSTPAN_DEV`, `DUSTPAN_DATA_DIR` and
   `DUSTPAN_CONFIG` in `server.extraEnv`. A new database gets the
   installation kind `production` at the first start (ADR 237).
5. **An external PostgreSQL database only (owner).** The chart runs no
   database, has no database subchart and offers no SQLite. The URL comes
   only from an existing Secret: `database.existingSecret.name` and `.key`.
   The chart sets `DUSTPAN_DATABASE` with `valueFrom.secretKeyRef`. The
   schema has no key for a URL value.
   Benchmark: the Temporal and Harbor charts take an external database. The
   Bitnami Keycloak chart includes a PostgreSQL subchart by default.
6. **The image by tag or digest, never `latest` (product manager).**
   `image.tag` is empty by default and then means `appVersion`. The schema
   refuses `latest`. When `image.digest` has a value, the pod uses
   `<repository>@<digest>` and ignores the tag. The guide tells production
   installs to set the digest.
   Benchmark: Bitnami charts have `image.digest`. The Grafana chart has
   `image.sha`.

### Secrets

7. **Each secret from an existing Secret (product manager).** A value of
   the chart names a Secret and a key. It never holds the secret. So the
   release record of Helm holds no secret either.

   | Secret | Values | How the pod gets it |
   | --- | --- | --- |
   | database URL | `database.existingSecret` (required) | `DUSTPAN_DATABASE` from `secretKeyRef` |
   | key-encryption key | `keyEncryptionKey.existingSecret` (required) | `DUSTPAN_KEY_ENCRYPTION_KEY` from `secretKeyRef` |
   | bootstrap admin tokens | `adminToken.existingSecret` (optional) | `DUSTPAN_ADMIN_TOKEN` from `secretKeyRef` |
   | S3 credentials | `s3.credentials.existingSecret` (optional) | `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` from `secretKeyRef` |
   | built-in TLS | `server.tls.existingSecret` (optional) | a `kubernetes.io/tls` Secret volume, at the paths in `DUSTPAN_TLS_CERT` and `DUSTPAN_TLS_KEY` |
   | backup schedule token | `backup.schedule.tokenSecret` | the CronJob of decision 19 |
   | metrics token | `metrics.serviceMonitor.tokenSecret` | the `authorization` of the ServiceMonitor |

   A Secret volume gives the file the group of `fsGroup` and mode 0440, and
   `serve` refuses a key file that the group can read. So the chart gives
   the key in the environment.
   With no S3 Secret, the server uses the default credential chain, for
   example a role that `serviceAccount.annotations` gives to the pod.
   Benchmark: the Keycloak (codecentric), Harbor and Bitnami charts take an
   `existingSecret` for each password.
8. **The KMS key-encryption key comes with its setting.** When the server
   gets the KMS setting of ADR 217, the same pull request adds
   `keyEncryptionKey.kms` to the values and the schema. The schema then
   needs exactly one of `existingSecret` and `kms`. Before that, the schema
   has no `kms` key, and `additionalProperties: false` refuses one. The
   chart has no value for a setting that the server does not have.
9. **The schema refuses a plain value for a secret name.** In `server.extraEnv`, a
   name that ends in `SECRET`, `TOKEN`, `PASSWORD`, `KEY`, `CREDENTIAL` or `CREDENTIALS`
   needs `valueFrom.secretKeyRef`, and the schema refuses `value`. The
   schema also refuses each variable that the chart sets itself.
   The schema also refuses these names, because they could change a
   setting that the chart sets from another place: `PG` variables (they
   could change the database connection that the Secret sets), `GODEBUG`
   (it changes the behavior of the Go runtime, for example of TLS),
   `AWS_ENDPOINT_URL`, `AWS_ENDPOINT_URL_S3`, `AWS_REGION` and
   `AWS_DEFAULT_REGION` (they could point the S3 client at an endpoint or a
   region other than `s3.endpoint` and `s3.region`). In `server.extraEnv`,
   `valueFrom` holds only `secretKeyRef`, with a name and a key.

### Storage

10. **The blob store: `fs` on a claim, or `s3`.** `blobStore.type: fs` puts
    the blobs on a PersistentVolumeClaim, made by the chart or named in
    `existingClaim`, mounted at `/data/blobs`. `blobStore.type: s3` gives
    `--blob-store s3://<bucket>[/<prefix>]`, with the endpoint and region in
    `s3`. The blob store and an `s3` backup target share `s3`, because the
    server has one S3 endpoint setting and one region setting.
11. **No database files on a claim.** With PostgreSQL, the pod has no
    database file. With `fs`, the claim holds only blobs.

### Instances and rollout

12. **One instance in `v0.1.0` (product manager).** The schema caps
    `replicas` at 1: `"minimum": 1` and `"maximum": 1`. The server work of
    ADR 72 is not complete, and the guide says that several instances are
    not supported yet. The release that completes that work and passes the
    smoke test with 2 instances removes the cap. In the same pull request,
    the schema gets back the rule that more than 1 instance needs
    `blobStore.type: s3` and a backup target that is not `dir`, because an
    `fs` claim and a `dir` target keep data on the disk of one pod. The
    workload is a Deployment, not a StatefulSet: the instances have no
    identity, no peer protocol and no disk of their own (ADR 72 decisions 2
    and 9). The chart has no leader election, because the leases, the
    schedule rows and the fence are in the database.
    Benchmark: the Vault and Keycloak charts ship single-instance defaults
    until clustering is ready. The Harbor core and the Grafana chart use a
    Deployment for instances that share a database. The Vault chart uses a
    StatefulSet because its Raft storage keeps data on each pod.
13. **The update strategy is `Recreate` for each blob store.** Until the
    release that lifts the cap on `replicas`, the strategy is `Recreate` for
    each blob store. The old pod stops before the new pod migrates. For
    `fs`, a ReadWriteOnce claim also attaches to one node, and one instance
    holds the blob directory. When the cap goes, `blobStore.type: s3` gets
    the strategy `RollingUpdate` with `maxSurge: 1` and
    `maxUnavailable: 0`, and `fs` keeps `Recreate`. A rolling update runs
    two releases on one schema for a short time. Expand and contract with
    K = 2 makes that safe (ADR 72 decision 11).
14. **No PodDisruptionBudget in `v0.1.0`.** With 1 instance, a budget
    would block each node drain, so the chart makes none. When the cap goes,
    the chart makes a PodDisruptionBudget with `maxUnavailable: 1` only when
    `replicas` is more than 1. The values and the schema get
    `podDisruptionBudget` in that release, because the chart has no value
    for a setting that it does not use.
    Benchmark: the Grafana and Keycloak (codecentric) charts make the budget
    optional.

### Upgrades and migrations

15. **The server migrates at start (product manager).** The chart has no
    pre-upgrade Job and no Helm hook. The new pod applies the pending
    migrations when `serve` starts. On PostgreSQL the migration lock lets
    one pod migrate while the other new pods wait. The startup probe gives
    the start-up steps 10 minutes by default (decision 17). The reasons:
    - Only `serve` may open the database (ADR 39 decision 1). A Job would
      need a `migrate` command, which does not exist (ADR 33, RFC 7).
    - A hook Job is a second holder of the database URL, with its own
      failure and cleanup.
    - Expand and contract (ADR 72 decision 11) lets the old pods run on the
      new schema while a rolling update runs, after the cap on `replicas`
      goes (decision 13).

    A startup-probe kill during a migration rolls the migration back,
    because it is one transaction under an advisory lock. A migration that
    runs longer than the startup probe allows rolls back and restarts, and
    then starts again. `opendustpan upgrade check` and the guide name the
    release notes of a long migration. The operator raises
    `probes.startup.failureThreshold` for it.

    Benchmark: Grafana and Keycloak migrate at start. The Temporal chart and
    older Harbor charts run a schema Job as a Helm hook.
16. **Rollback.** `helm rollback` to a revision with the same app version is
    a change of values only, and it is safe. A rollback to an older app
    version works while the minimum binary version of the schema is not
    above the old binary (ADR 72 decision 11). Otherwise the old pod refuses
    to start and names both versions. The startup probe then fails:
    - `RollingUpdate` comes with the removal of the cap on `replicas`
      (decision 13). Then the new pods keep serving, because
      `maxUnavailable` is 0.
    - With `Recreate`, which is the strategy in `v0.1.0`, the server is down
      until the operator rolls forward again.

    The project has no down migration (ADR 33 decision 6). To go back past a
    contract migration, the operator restores the backup made before the
    upgrade into an empty database (ADR 38), and starts the older chart. The
    guide asks for `opendustpan upgrade check` and `opendustpan backup`
    before each `helm upgrade`.

    **No pre-upgrade backup hook (product manager).** The backup stays a
    step in the guide. The chart has no hook that runs `opendustpan backup`
    before an upgrade, because a hook adds a failure point to every upgrade.
    The project looks at this again after the first upgrade between two
    tags.
    Benchmark: as we read the Grafana and Keycloak (codecentric) charts in
    2026, they add no backup step to an upgrade.

### Probes, resources and the pod

17. **Probes (ADR 34).** The startup probe and the liveness probe send
    `GET /health/live`. The readiness probe sends `GET /health/ready`. The
    readiness timeout is 3 seconds, above the 2-second limit of the checks.
    The startup probe allows `periodSeconds` times `failureThreshold`
    seconds, 10 minutes by default, for migrations and the other start-up
    steps. With built-in TLS the probes use `HTTPS`.
18. **Resources (product manager).** The requests are 100m CPU and 256Mi of
    memory. The memory limit is 1Gi. The chart sets no CPU limit by
    default. The architecture and sizing guide gives values for larger
    installs.
    Benchmark: the Grafana and Keycloak (codecentric) charts leave CPU
    limits to the operator.

### Backups

19. **The target and an optional schedule (product manager).**
    `backup.target.type` is `none`, `dir` or `s3`. `dir` mounts a second
    claim at `/backups`, and `s3` uses `s3`. With `none`, `doctor` warns
    (ADR 238 decision 10). `backup.schedule.enabled` adds a CronJob that
    runs the client command `opendustpan backup` against the Service of the
    release, with an access token from a Secret. The CronJob uses the same
    image and the same security context. It does not mount the
    key-encryption key or the database URL, because the server makes the
    backup (ADR 39). The CronJob uses its own ServiceAccount with no
    annotations, `automountServiceAccountToken: false`, and gets no S3
    credentials and no `AWS_*` variable. Its only secret is the token from
    `backup.schedule.tokenSecret`. It sets `concurrencyPolicy: Forbid`. The
    schema does not parse the cron syntax. `kubeconform` checks the
    CronJob against the Kubernetes schema, and the API server checks the
    cron string when it admits the CronJob. The default schedule is once a
    day, which keeps the 25-hour check of `doctor` green. A restore is an
    API call (ADR 38), and the chart has no restore Job.
    Benchmark: the GitLab chart runs its backups from a CronJob.

### Console, ingress and TLS

20. **The console.** `console.enabled` (default `true`) sets
    `--console-dir /console`. A non-empty `console.runtimeConfig` becomes a
    ConfigMap, mounted as `/console/config.json`. The browser reads that
    file, so it holds no secret. A checksum of the ConfigMap in a pod
    annotation rolls the pods when it changes.
21. **Ingress and TLS (product manager).** The default is plain HTTP inside
    the cluster and TLS at the ingress. `ingress.*` makes an Ingress.
    `server.publicURL` sets `--public-url`, and `server.trustedProxies` sets
    `--trusted-proxies` (ADR 27 decision 2). With
    `server.tls.existingSecret`, the server serves TLS itself (ADR 27
    decision 1), and a certificate renewal needs a restart of the pods.
    OpenTofu needs TLS at the edge that the client sees (ADR 25).
    The container listens on `:8080` (`DUSTPAN_LISTEN`, which the chart
    sets), and the Service and the NetworkPolicy target that port.
    `service.port` 0 means 80 with plain HTTP and 443 with built-in TLS.
    Benchmark: the Grafana, Keycloak (codecentric) and Harbor charts end
    TLS at the ingress by default.

### Security

22. **The security context (product manager).** We designed the pod and
    the container against the "restricted" profile of the Kubernetes Pod
    Security Standards:
    - `runAsNonRoot: true`, user and group 65532, `fsGroup: 65532`
    - `allowPrivilegeEscalation: false` and `privileged: false`
    - `readOnlyRootFilesystem: true`, with an `emptyDir` on `/tmp`
    - `seccompProfile: RuntimeDefault`
    - `capabilities.drop: [ALL]`

    The schema holds each boolean, the seccomp profile and the dropped
    capabilities fixed. It accepts a user and a group above 0. It holds
    `automountServiceAccountToken` fixed at `false`, because the server does
    not call the Kubernetes API. The schema refuses the AppArmor and
    seccomp annotations in `podAnnotations`.
    Benchmark: the Vault and Grafana charts run as a user that is not root.
    Bitnami charts set `readOnlyRootFilesystem: true` and drop all
    capabilities by default.
23. **A NetworkPolicy by default, with closed egress (product manager).**
    `networkPolicy.enabled` is `true`. The policy lets
    `networkPolicy.ingressFrom` reach the server port. An empty list lets
    every source reach it. Each item of `ingressFrom` and of `extraEgress`
    is an object with at least one key, because an empty peer matches every
    source. When `ingressFrom` is not empty, the chart adds a rule for the
    pods of its backup CronJob. An example for `ingressFrom` is the namespace
    of Prometheus, for the ServiceMonitor of decision 24.

    `allowAllEgress` is `false` by default. The policy then allows:
    - DNS, UDP and TCP port 53, to the pods with the label
      `k8s-app: kube-dns` in the namespace `kube-system`.
      `networkPolicy.dns` (`namespaceSelector` and `podSelector`) replaces
      these selectors.
    - TCP port 5432 (the database) and TCP port 443 (S3, sign-in providers
      and webhooks) to `0.0.0.0/0` and `::/0`.
    - The rules in `extraEgress`, for other ports. Examples: a webhook on
      port 8443, and the metadata server `169.254.169.254` on port 80.

    The chart does not know the addresses of the database, S3 and sign-in
    providers, so it limits the ports and not the addresses. With
    `allowAllEgress: true`, the policy allows all egress.
    Benchmark: PCI DSS requirement 1.3.2 restricts outbound traffic to what
    the installation needs. Bitnami charts make a NetworkPolicy by default,
    with open egress. Our default is stricter than theirs.
24. **Metrics (optional).** `metrics.serviceMonitor.enabled` makes a
    ServiceMonitor of the Prometheus Operator for `GET /metrics`. It sends
    an access token from `tokenSecret` (ADR 32 decision 4). The default is
    off, because the CRD is not on every cluster.

### What the chart never does

25. **Never.** The chart never:
    - makes a key-encryption key, a token or a password, also not with
      `randAlphaNum` or `lookup`
    - puts a secret in a ConfigMap, an annotation or a value
    - runs a container as root, or with privilege escalation
    - uses the image tag `latest`
    - runs `dev`, or sets `DUSTPAN_DEV`
    - runs a database

    The schema enforces each item that a value can break. Values can not
    break the other items, because no template takes such a value.

    Each object in the schema lists every key of `values.yaml` in
    `required`, so `null` cannot remove a key.

    The templates `fail` when the backup token or the metrics token names
    the same Secret and key as `adminToken`, and when the database ref and
    the key ref are the same Secret and key. The schema cannot compare two
    values, so these checks are in the templates. The pull request that adds
    the templates adds a template test case for each check.

### CI, tests and docs

26. **Static checks in CI (product manager).** A `make chart-lint` target
    runs:
    - `helm lint --strict` with the default values, which must fail,
      because the schema needs a key and a database
    - `helm lint --strict` with each file in `ci/`, which must pass
    - one file for each refused case in `ci/refused/`, each of which must
      fail. The cases include: `replicas` above 1, the tag `latest`, each
      refused name and each plain secret value in `server.extraEnv`, a
      `valueFrom` that is not a `secretKeyRef`, a root user, a root group,
      a privileged container, `automountServiceAccountToken: true`, an
      AppArmor annotation and a seccomp annotation in `podAnnotations`,
      and `null` for each key of `networkPolicy`, `probes` and
      `serviceAccount`, and for the root keys `networkPolicy`, `probes`,
      `serviceAccount`, `automountServiceAccountToken` and `console`
    - `helm template` with each file in `ci/`, through `kubeconform -strict`,
      for the oldest and the newest supported Kubernetes minor version

    CI runs it in a job of its own, in parallel with the Go job. The job
    downloads pinned versions of `helm` and `kubeconform` and checks their
    SHA-256. On this design, `helm lint --strict` with helm v3.18.6 took 0.07
    seconds for each values file, so the job does not make CI slower than the Go job.
    `make check` does not include the target, so a contributor needs no
    Helm. The tools need the owner's yes. See "Open", item 1.
    Benchmark: the Grafana and Bitnami chart repositories lint each chart
    with `helm lint` and check the output against the Kubernetes schemas.
27. **A smoke test on kind, later (product manager).** With the templates,
    a `make chart-smoke` target:
    1. makes a kind cluster
    2. starts PostgreSQL from a plain manifest in the test directory, not
       from the chart
    3. makes the Secrets, with `opendustpan key generate` for the key
    4. installs the chart with each `ci/` file, and waits for readiness
    5. runs `opendustpan doctor --server` and the e2e steps of both tools
       through the Service
    6. upgrades from the chart of the previous tag, then runs
       `helm rollback`

    CI runs it on a change under `deploy/helm/` and on the 2-hour schedule
    of `main`. It is not in `make check`. With `chart-testing` (`ct`), the
    same job lints only the changed charts once more than one chart exists.
    Benchmark: the Grafana and Bitnami chart repositories install each
    changed chart in kind with `chart-testing`.
28. **Docs.** The guide `docs/guides/kubernetes.md` and the reference
    `docs/reference/helm-values.md` land in the pull request that adds the
    templates (ADR 214). The guide gives the Secrets to make, a minimal
    install, the upgrade steps of decision 16, and the limit on several
    instances. A test compares the reference page with `values.yaml`.

29. **The supported Kubernetes versions (product manager).** `Chart.yaml`
    sets `kubeVersion` to the minor versions that the Kubernetes project
    supports on the release day. `make chart-lint` runs `kubeconform` for
    the oldest and the newest of them (decision 26).
    Benchmark: the Kubernetes version skew policy names the supported minor
    versions, so the chart follows the project and does not choose its own
    range.

## Consequences

- An install of the chart with only the defaults fails at `helm install`,
  and the message names the two values that are empty. This is the same
  fail-closed rule as `serve` and the image (ADR 238).
- An operator makes at least two Secrets before the install: the database
  URL and the key-encryption key.
- The chart has no SQLite mode. A trial on a laptop uses `opendustpan dev`
  or the image (ADR 237).
- The chart, the image and the binary have one version. A chart fix needs a
  patch release.
- A rollback across a contract migration needs a restore from a backup.
- The settings that the chart does not name, for example the OIDC settings,
  go in `server.extraEnv`. A value with a secret in it needs a
  `secretKeyRef`. `DUSTPAN_OIDC_CLIENT_SECRET` works from a `secretKeyRef`.
- No value mounts a Secret file for a `_FILE` setting, and the chart cannot
  set those settings. The chart cannot turn on audit chain signing:
  `DUSTPAN_AUDIT_SIGNING_KEY_FILE` has no environment form. The webhook
  secrets of the audit sink and the log sink have an environment form
  (`DUSTPAN_AUDIT_WEBHOOK_SECRET`, `DUSTPAN_LOG_WEBHOOK_SECRET`), so they
  work from a `secretKeyRef` in `server.extraEnv`. A server change that accepts a
  file with owner user 0, the group of the process and mode 0440 would
  need an RFC.
- In `v0.1.0` the chart runs one instance with the `Recreate` strategy, and
  each upgrade has a short time with no server.
- The chart makes the egress of the server narrow by default. A webhook on
  a port other than 443, or the metadata server, needs `extraEgress`.
- The templates come in a later pull request. That pull request runs
  `make chart-lint` and adds the guide.
- RFC 35 keeps its other open items (ACME). ADR 27 decision 8 keeps its
  other items: ACME, image signing, an SBOM, the registry and OS packages.
- The coordinator chart of ADR 136 is a separate chart. This chart does not
  install a coordinator.

## Open

Questions for the project owner. Each has a recommendation.

1. **The tools for CI.** `helm` and `kubeconform` now, and `kind` and
   `chart-testing` with the smoke test. CLAUDE.md asks for the owner's yes
   before the project adds a tool (ADR 27 decision 7 has the same rule).
   Recommendation: yes, each pinned by version and SHA-256, downloaded only
   in CI and in the make targets.
2. **The registry and signing of the chart.** ADR 27 decision 8 leaves the
   registry and signing open. `values.yaml` uses
   `ghcr.io/opendustpan/opendustpan` as a placeholder.
   Recommendation: the chart goes to the same OCI registry as the image,
   and gets the same signing when the image gets it.
