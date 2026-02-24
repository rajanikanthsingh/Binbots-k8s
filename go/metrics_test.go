package main

import (
	"strings"
	"testing"
)

func TestParsePrometheusValue(t *testing.T) {
	tests := []struct {
		line string
		want float64
	}{
		{"container_cpu_usage_seconds_total{id=\"/\"} 123.45", 123.45},
		{"container_memory_working_set_bytes{id=\"/\"} 1073741824", 1073741824},
		{"metric_name 0", 0},
		{"metric_name 1.5e2", 150},
		{"no_value", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := parsePrometheusValue(tt.line)
		if got != tt.want {
			t.Errorf("parsePrometheusValue(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestParseContainerMetrics(t *testing.T) {
	// Sample Prometheus-style text (cpu and memory lines)
	body := strings.NewReader(`# HELP container_cpu_usage_seconds_total CPU usage
# TYPE container_cpu_usage_seconds_total counter
container_cpu_usage_seconds_total{id="/"} 1.5
container_cpu_usage_seconds_total{id="/system"} 0.2
container_memory_working_set_bytes{id="/"} 536870912
container_memory_working_set_bytes{id="/system"} 268435456
`)
	cpu, mem, err := parseContainerMetrics(body, "container_cpu_usage_seconds_total", "container_memory_working_set_bytes")
	if err != nil {
		t.Fatalf("parseContainerMetrics: %v", err)
	}
	if cpu != 1.7 {
		t.Errorf("cpu = %v, want 1.7", cpu)
	}
	if mem != 805306368 {
		t.Errorf("mem = %v, want 805306368", mem)
	}
}

func TestParseContainerMetricsEmpty(t *testing.T) {
	body := strings.NewReader("")
	cpu, mem, err := parseContainerMetrics(body, "container_cpu_usage_seconds_total", "container_memory_working_set_bytes")
	if err != nil {
		t.Fatalf("parseContainerMetrics: %v", err)
	}
	if cpu != 0 || mem != 0 {
		t.Errorf("empty body: cpu=%v mem=%v, want 0,0", cpu, mem)
	}
}
