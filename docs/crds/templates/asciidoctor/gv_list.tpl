{{- define "gvList" -}}
{{- $groupVersions := . -}}

[id="k3k-api-reference"]
= API Reference
:revdate: "2006-01-02"
:page-revdate: {revdate}
:anchor_prefix: k8s-api

.Packages
{{- range $groupVersions }}
- {{ asciidocRenderGVLink . }}
{{- end }}

{{ range $groupVersions }}
{{ template "gvDetails" . }}
{{ end }}

{{- end -}}