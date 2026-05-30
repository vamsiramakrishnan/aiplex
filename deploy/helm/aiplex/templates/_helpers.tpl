{{/*
Common labels
*/}}
{{- define "aiplex.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: aiplex
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}

{{/*
API image
*/}}
{{- define "aiplex.api.image" -}}
{{- $tag := .Values.api.image.tag | default .Chart.AppVersion -}}
{{- if .Values.api.image.repository -}}
{{ .Values.api.image.repository }}:{{ $tag }}
{{- else -}}
{{ .Values.global.registry }}/aiplex-api:{{ $tag }}
{{- end -}}
{{- end }}

{{/*
Console image
*/}}
{{- define "aiplex.console.image" -}}
{{- $tag := .Values.console.image.tag | default .Chart.AppVersion -}}
{{- if .Values.console.image.repository -}}
{{ .Values.console.image.repository }}:{{ $tag }}
{{- else -}}
{{ .Values.global.registry }}/aiplex-console:{{ $tag }}
{{- end -}}
{{- end }}

{{/*
Namespace
*/}}
{{- define "aiplex.namespace" -}}
aiplex-system
{{- end }}
