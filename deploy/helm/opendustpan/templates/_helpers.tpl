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
