{{/*
Namespace the chart deploys into. Centralized so --namespace and
.Values.namespace.name can't silently disagree across templates.
*/}}
{{- define "hydra-operator.namespace" -}}
{{- .Values.namespace.name -}}
{{- end -}}

{{/*
Common labels, mirroring the app.kubernetes.io/name label used throughout
deploy/base so the two manifest sets stay comparable rule-for-rule.
*/}}
{{- define "hydra-operator.labels" -}}
app.kubernetes.io/name: hydra-operator
{{- end -}}
