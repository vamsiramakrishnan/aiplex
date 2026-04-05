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
{{- if .Values.api.image.repository -}}
{{ .Values.api.image.repository }}:{{ .Values.api.image.tag }}
{{- else -}}
{{ .Values.global.registry }}/aiplex-api:{{ .Values.api.image.tag }}
{{- end -}}
{{- end }}

{{/*
Console image
*/}}
{{- define "aiplex.console.image" -}}
{{- if .Values.console.image.repository -}}
{{ .Values.console.image.repository }}:{{ .Values.console.image.tag }}
{{- else -}}
{{ .Values.global.registry }}/aiplex-console:{{ .Values.console.image.tag }}
{{- end -}}
{{- end }}

{{/*
Namespace
*/}}
{{- define "aiplex.namespace" -}}
aiplex-system
{{- end }}
