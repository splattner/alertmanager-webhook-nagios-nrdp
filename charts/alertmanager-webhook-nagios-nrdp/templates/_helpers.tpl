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

{{/*
Fail the render if `config`'s server.listen port disagrees with
service.port. The Service, the containerPort and the probes all follow
service.port, but the process only listens where `config` tells it to - and
`config` is an opaque string this chart cannot rewrite. Left unchecked, a
mismatch installs cleanly and then fails its readiness probe forever, which
is a far worse way to find out.

An unparseable `config` is left alone: the app itself reports that far
better than a template error can.
*/}}
{{- define "alertmanager-webhook-nagios-nrdp.validatePort" -}}
{{- $cfg := fromYaml .Values.config -}}
{{- if not $cfg.Error -}}
{{- $listen := "" -}}
{{- if $cfg.server -}}{{- $listen = $cfg.server.listen | default "" -}}{{- end -}}
{{- /* ":8080" is the app's own default when server.listen is unset. */ -}}
{{- $configured := "8080" -}}
{{- if $listen -}}{{- $configured = last (splitList ":" $listen) -}}{{- end -}}
{{- $expected := printf "%v" .Values.service.port -}}
{{- if ne $configured $expected -}}
{{- fail (printf "service.port is %s but config's server.listen resolves to port %s - the container would listen on %s while the Service and probes target %s. Set server.listen to \":%s\" in `config`, or change service.port to %s." $expected $configured $configured $expected $expected $configured) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "alertmanager-webhook-nagios-nrdp.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "alertmanager-webhook-nagios-nrdp.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
