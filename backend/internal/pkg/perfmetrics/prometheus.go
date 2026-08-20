package perfmetrics

import (
	"fmt"
	"strings"
)

// Prometheus text exposition format for /metrics endpoint.
// Converts internal samples to the standard Prometheus format.
func Prometheus(samples []Sample) []byte {
	var builder strings.Builder
	builder.WriteString("# HELP grok2api_metrics_interval Interval metrics collected by grok2api.\n")
	builder.WriteString("# TYPE grok2api_metrics_interval gauge\n")
	for _, sample := range samples {
		name := sanitizeMetricName(sample.Name)
		labels := prometheusLabels(sample.Labels)
		if sample.HasGauge {
			fmt.Fprintf(&builder, "grok2api_%s_gauge{%s} %d\n", name, labels, sample.Gauge)
		}
		if sample.Count > 0 {
			fmt.Fprintf(&builder, "grok2api_%s_count{%s} %d\n", name, labels, sample.Count)
			fmt.Fprintf(&builder, "grok2api_%s_total{%s} %d\n", name, labels, sample.Total)
			fmt.Fprintf(&builder, "grok2api_%s_max{%s} %d\n", name, labels, sample.Maximum)
		}
	}
	return []byte(builder.String())
}

func sanitizeMetricName(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteRune('_')
		}
	}
	return builder.String()
}

func prometheusLabels(labels Labels) string {
	values := make([]string, 0, 8)
	if labels.Subsystem != "" {
		values = append(values, fmt.Sprintf("subsystem=%q", labels.Subsystem))
	}
	if labels.Operation != "" {
		values = append(values, fmt.Sprintf("operation=%q", labels.Operation))
	}
	if labels.Provider != "" {
		values = append(values, fmt.Sprintf("provider=%q", labels.Provider))
	}
	if labels.Plane != "" {
		values = append(values, fmt.Sprintf("plane=%q", labels.Plane))
	}
	if labels.Stage != "" {
		values = append(values, fmt.Sprintf("stage=%q", labels.Stage))
	}
	if labels.Ordinal != "" {
		values = append(values, fmt.Sprintf("ordinal=%q", labels.Ordinal))
	}
	if labels.Outcome != "" {
		values = append(values, fmt.Sprintf("outcome=%q", labels.Outcome))
	}
	return strings.Join(values, ",")
}
