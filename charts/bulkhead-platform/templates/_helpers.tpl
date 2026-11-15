{{/* Labels on every object of the platform chart. */}}
{{- define "platform.labels" -}}
app.kubernetes.io/name: bulkhead-platform
app.kubernetes.io/part-of: bulkhead
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end -}}

{{/* A list of strings as a CEL list literal: ['a', 'b']. The values schema forbids quotes. */}}
{{- define "platform.celList" -}}
[{{ range $i, $v := . }}{{ if $i }}, {{ end }}'{{ $v }}'{{ end }}]
{{- end -}}
