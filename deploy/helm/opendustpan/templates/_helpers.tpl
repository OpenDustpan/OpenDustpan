{{/* The name of the chart. */}}
{{- define "opendustpan.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* The full name of the release objects. */}}
{{- define "opendustpan.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "opendustpan.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Selector labels of the server pods. */}}
{{- define "opendustpan.selectorLabels" -}}
app.kubernetes.io/name: {{ include "opendustpan.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: server
{{- end -}}

{{/* Selector labels of the worker pods. */}}
{{- define "opendustpan.workerSelectorLabels" -}}
app.kubernetes.io/name: {{ include "opendustpan.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: worker
{{- end -}}

{{/* Selector labels of the server pods and the worker pods together. */}}
{{- define "opendustpan.podSelectorLabels" -}}
app.kubernetes.io/name: {{ include "opendustpan.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Selector labels of the backup CronJob pods. */}}
{{- define "opendustpan.backupSelectorLabels" -}}
app.kubernetes.io/name: {{ include "opendustpan.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: backup
{{- end -}}

{{- define "opendustpan.commonLabels" -}}
helm.sh/chart: {{ include "opendustpan.chart" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/part-of: opendustpan
{{- end -}}

{{- define "opendustpan.labels" -}}
{{ include "opendustpan.commonLabels" . }}
{{ include "opendustpan.selectorLabels" . }}
{{- end -}}

{{- define "opendustpan.workerLabels" -}}
{{ include "opendustpan.commonLabels" . }}
{{ include "opendustpan.workerSelectorLabels" . }}
{{- end -}}

{{- define "opendustpan.backupLabels" -}}
{{ include "opendustpan.commonLabels" . }}
{{ include "opendustpan.backupSelectorLabels" . }}
{{- end -}}

{{/* The image reference. A digest wins over a tag. */}}
{{- define "opendustpan.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
{{- end -}}

{{- define "opendustpan.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "opendustpan.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "opendustpan.backupServiceAccountName" -}}
{{- printf "%s-backup" (include "opendustpan.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* The port of the Service: 80 with plain HTTP, 443 with built-in TLS. */}}
{{- define "opendustpan.servicePort" -}}
{{- if .Values.service.port -}}
{{- .Values.service.port -}}
{{- else if .Values.server.tls.existingSecret -}}
443
{{- else -}}
80
{{- end -}}
{{- end -}}

{{- define "opendustpan.scheme" -}}
{{- if .Values.server.tls.existingSecret -}}HTTPS{{- else -}}HTTP{{- end -}}
{{- end -}}

{{- define "opendustpan.blobClaim" -}}
{{- default (printf "%s-blobs" (include "opendustpan.fullname" .)) .Values.blobStore.fs.persistence.existingClaim -}}
{{- end -}}

{{- define "opendustpan.backupClaim" -}}
{{- default (printf "%s-backups" (include "opendustpan.fullname" .)) .Values.backup.target.dir.persistence.existingClaim -}}
{{- end -}}

{{/* The environment of the server container and the worker container. */}}
{{- define "opendustpan.env" -}}
{{- $tls := .Values.server.tls.existingSecret -}}
- name: DUSTPAN_LISTEN
  value: ":8080"
- name: DUSTPAN_DATABASE
  valueFrom:
    secretKeyRef:
      name: {{ .Values.database.existingSecret.name | quote }}
      key: {{ .Values.database.existingSecret.key | quote }}
- name: DUSTPAN_KEY_ENCRYPTION_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Values.keyEncryptionKey.existingSecret.name | quote }}
      key: {{ .Values.keyEncryptionKey.existingSecret.key | quote }}
{{- with .Values.adminToken.existingSecret }}
{{- if .name }}
- name: DUSTPAN_ADMIN_TOKEN
  valueFrom:
    secretKeyRef:
      name: {{ .name | quote }}
      key: {{ .key | quote }}
{{- end }}
{{- end }}
{{- with .Values.s3.credentials.existingSecret }}
{{- if .name }}
- name: AWS_ACCESS_KEY_ID
  valueFrom:
    secretKeyRef:
      name: {{ .name | quote }}
      key: {{ .accessKeyIDKey | quote }}
- name: AWS_SECRET_ACCESS_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .name | quote }}
      key: {{ .secretAccessKeyKey | quote }}
{{- end }}
{{- end }}
{{- if $tls }}
- name: DUSTPAN_TLS_CERT
  value: /tls/tls.crt
- name: DUSTPAN_TLS_KEY
  value: /tls/tls.key
{{- end }}
{{- with .Values.server.extraEnv }}
{{- toYaml . | nindent 0 }}
{{- end }}
{{- end -}}

{{/*
Checks that the schema cannot make: two values that name the same Secret
and key, and values that the release needs.
*/}}
{{- define "opendustpan.validate" -}}
{{- $db := .Values.database.existingSecret -}}
{{- $kek := .Values.keyEncryptionKey.existingSecret -}}
{{- if or (not $db.name) (not $kek.name) -}}
{{- fail "database.existingSecret.name and keyEncryptionKey.existingSecret.name are required: make the two Secrets and set both names" -}}
{{- end -}}
{{- if and (eq $db.name $kek.name) (eq $db.key $kek.key) -}}
{{- fail "database.existingSecret and keyEncryptionKey.existingSecret name the same Secret and key: use two different Secrets or keys" -}}
{{- end -}}
{{- if gt (int .Values.replicas) 1 -}}
{{- if eq .Values.blobStore.type "fs" -}}
{{- fail (printf "replicas is %d and blobStore.type is fs: the fs blob store keeps blobs on one volume that one pod mounts. Set blobStore.type to s3 and name an S3-compatible bucket in blobStore.s3.bucket, or set replicas to 1" (int .Values.replicas)) -}}
{{- end -}}
{{- if eq .Values.backup.target.type "dir" -}}
{{- fail (printf "replicas is %d and backup.target.type is dir: a dir backup target keeps backups on one volume that one pod mounts. Set backup.target.type to s3 and name an S3-compatible bucket in backup.target.s3.bucket, or set replicas to 1" (int .Values.replicas)) -}}
{{- end -}}
{{- if not (gt (int .Values.preStopSeconds) (int .Values.probes.readiness.periodSeconds)) -}}
{{- fail (printf "replicas is %d and preStopSeconds is %d: the preStop wait must be longer than probes.readiness.periodSeconds (%d). Raise preStopSeconds" (int .Values.replicas) (int .Values.preStopSeconds) (int .Values.probes.readiness.periodSeconds)) -}}
{{- end -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) (add (int .Values.preStopSeconds) 40) -}}
{{- fail (printf "terminationGracePeriodSeconds (%d) must be at least preStopSeconds (%d) + 40, the sum of the 10 s drain and the 30 s shutdown timeout of the server, or Kubernetes stops the pod before the server finishes its requests. Raise terminationGracePeriodSeconds to %d or more" (int .Values.terminationGracePeriodSeconds) (int .Values.preStopSeconds) (add (int .Values.preStopSeconds) 40)) -}}
{{- end -}}
{{- if and .Values.podDisruptionBudget.enabled (ge (int .Values.podDisruptionBudget.minAvailable) (int .Values.replicas)) -}}
{{- fail (printf "podDisruptionBudget.minAvailable (%d) must be less than replicas (%d), or no pod can leave during a node drain. Lower minAvailable, raise replicas, or set podDisruptionBudget.enabled to false" (int .Values.podDisruptionBudget.minAvailable) (int .Values.replicas)) -}}
{{- end -}}
{{- range .Values.server.extraEnv -}}
{{- $name := .name -}}
{{- $value := default "" .value | trim -}}
{{- if and (has .name (list "DUSTPAN_AUDIT_SINK" "DUSTPAN_LOG_SINK")) $value -}}
{{- range regexSplit "\\s+" $value -1 -}}
{{- if regexMatch "(^|=)file:" . -}}
{{- fail (printf "replicas is above 1 and server.extraEnv %s has a file: sink (%s): each instance would write only its own entries to its own file. Use a blob:, syslog+tls:// or webhook: sink for DUSTPAN_AUDIT_SINK, a syslog+tls:// sink for DUSTPAN_LOG_SINK, or set replicas to 1" $name .) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if hasPrefix "sqlite:" $value -}}
{{- fail (printf "replicas is above 1 and server.extraEnv %s starts with sqlite:. SQLite holds one instance only. Use a PostgreSQL database in database.existingSecret, or set replicas to 1" .name) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if .Values.worker.enabled -}}
{{- if eq .Values.blobStore.type "fs" -}}
{{- fail "worker.enabled is true and blobStore.type is fs: the worker pods and the server pods would share one volume. Set blobStore.type to s3, or set worker.enabled to false" -}}
{{- end -}}
{{- if eq .Values.backup.target.type "dir" -}}
{{- fail "worker.enabled is true and backup.target.type is dir: the worker pods and the server pods would share one volume. Set backup.target.type to s3 or none, or set worker.enabled to false" -}}
{{- end -}}
{{- end -}}
{{- $admin := .Values.adminToken.existingSecret -}}
{{- if $admin.name -}}
{{- $bt := .Values.backup.schedule.tokenSecret -}}
{{- if and .Values.backup.schedule.enabled (eq $bt.name $admin.name) (eq $bt.key $admin.key) -}}
{{- fail "backup.schedule.tokenSecret names the same Secret and key as adminToken.existingSecret: make a separate token for the backup" -}}
{{- end -}}
{{- $mt := .Values.metrics.serviceMonitor.tokenSecret -}}
{{- if and .Values.metrics.serviceMonitor.enabled (eq $mt.name $admin.name) (eq $mt.key $admin.key) -}}
{{- fail "metrics.serviceMonitor.tokenSecret names the same Secret and key as adminToken.existingSecret: make a separate token for the metrics" -}}
{{- end -}}
{{- end -}}
{{- if and .Values.backup.schedule.enabled (not .Values.backup.schedule.tokenSecret.name) -}}
{{- fail "backup.schedule.tokenSecret.name is required when backup.schedule.enabled is true" -}}
{{- end -}}
{{- if and .Values.metrics.serviceMonitor.enabled (not .Values.metrics.serviceMonitor.tokenSecret.name) -}}
{{- fail "metrics.serviceMonitor.tokenSecret.name is required when metrics.serviceMonitor.enabled is true" -}}
{{- end -}}
{{- end -}}
