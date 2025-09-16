{{/*
Expand the name of the chart.
*/}}
{{- define "k3k.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "k3k.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "k3k.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "k3k.labels" -}}
helm.sh/chart: {{ include "k3k.chart" . }}
{{ include "k3k.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "k3k.selectorLabels" -}}
app.kubernetes.io/name: {{ include "k3k.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "k3k.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "k3k.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
    Print the image pull secrets in the expected format (an array of objects with one possible field, "name").
*/}}
{{- define "image.pullSecrets" }}
    {{- $imagePullSecrets := list }}
    {{- range . }}
        {{- if kindIs "string" . }}
            {{- $imagePullSecrets = append $imagePullSecrets (dict "name" .) }}
        {{- else }}
            {{- $imagePullSecrets = append $imagePullSecrets . }}
        {{- end }}
    {{- end }}
    {{- toYaml $imagePullSecrets }}
{{- end }}

{{- define "controller.registry" }}
{{- $registry := .Values.global.imageRegistry | default .Values.controller.image.registry -}}
{{- if $registry }}
{{- $registry }}/
{{- else }}
{{- $registry }}
{{- end }}
{{- end }}
