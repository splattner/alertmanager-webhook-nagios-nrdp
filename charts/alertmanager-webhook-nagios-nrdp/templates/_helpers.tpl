{{/*
Chart name, truncated and DNS-1123-safe.
*/}}
{{- define "alertmanager-webhook-nagios-nrdp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name, honoring fullnameOverride / nameOverride.
*/}}
{{- define "alertmanager-webhook-nagios-nrdp.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "alertmanager-webhook-nagios-nrdp.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "alertmanager-webhook-nagios-nrdp.labels" -}}
helm.sh/chart: {{ include "alertmanager-webhook-nagios-nrdp.chart" . }}
{{ include "alertmanager-webhook-nagios-nrdp.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "alertmanager-webhook-nagios-nrdp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "alertmanager-webhook-nagios-nrdp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "alertmanager-webhook-nagios-nrdp.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "alertmanager-webhook-nagios-nrdp.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
