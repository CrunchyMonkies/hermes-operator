{{/* Common helpers for the hermes-operator chart. */}}

{{- define "hermes-operator.name" -}}
hermes-operator
{{- end -}}

{{- define "hermes-operator.fullname" -}}
{{- printf "%s-%s" .Release.Name "hermes-operator" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "hermes-operator.labels" -}}
app.kubernetes.io/name: hermes-operator
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: hermes-operator-{{ .Chart.Version }}
{{- end -}}

{{- define "hermes-operator.selectorLabels" -}}
app.kubernetes.io/name: hermes-operator
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: controller-manager
{{- end -}}

{{- define "hermes-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.name -}}
{{ .Values.serviceAccount.name }}
{{- else -}}
{{ include "hermes-operator.fullname" . }}
{{- end -}}
{{- end -}}

{{- define "hermes-operator.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{- define "hermes-operator.reloaderImage" -}}
{{- $tag := .Values.reloaderImage.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.reloaderImage.repository $tag -}}
{{- end -}}
