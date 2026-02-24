# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html) for the Helm chart.

## [0.2.0] - 2025-02-23

### Added

- **PrometheusRule** for alerts:
  - `BinbotsExporterDown`: fires when all k8s-ai-exporter targets are down for 5m
  - `BinbotsAgentNotRun`: fires when the k8s-ai-agent CronJob has not had a successful run within the configured threshold (default 30m)
- **Grafana dashboard wiring**: Dashboard JSON is embedded in the Helm chart under `helm/dashboards/`; ConfigMap supports optional `grafana_dashboard_folder` label for kube-prometheus-stack. README updated with import instructions for raw YAML deployments.
- **Testing**: Go unit tests in `go/metrics_test.go` for `parsePrometheusValue` and `parseContainerMetrics`. Python tests in `python/test_ai_agent.py` for recommendation logic and forecast helpers (run with `pytest`).
- **Multi-environment**: `helm/values-dev.yaml` and `helm/values-prod.yaml` for environment-specific schedules, resources, and alert thresholds. README documents usage with `-f values.yaml -f values-prod.yaml`.
- **CHANGELOG.md**: This file.

### Changed

- Helm chart version bumped to `0.2.0` (appVersion `0.2.0`).
- Grafana dashboard ConfigMap template now reads from `dashboards/grafana-dashboard-binbots.json` inside the chart (dashboard copied from `deploy/` into `helm/dashboards/` for correct `.Files.Get` behavior).

---

## [0.1.0] - Initial release

- Go exporter (DaemonSet) scraping kubelet/cAdvisor via API server proxy
- Python AI agent (CronJob) with Prometheus metrics and trend prediction (Prophet / fallback)
- Raw Kubernetes manifests in `deploy/`
- Helm chart in `helm/` with ServiceMonitor and optional Grafana dashboard ConfigMap
- Grafana dashboard JSON for node CPU, memory, and active pods
