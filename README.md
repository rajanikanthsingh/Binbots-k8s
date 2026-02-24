# Binbots-k8s: Kubernetes Scraper + AI Agent

Go exporter scrapes kubelet/cAdvisor via **API server proxy** (safe on EKS, AKS, GKE and local). Python AI agent pulls metrics from Prometheus and prints CPU/memory trend recommendations. Designed to work with **kube-prometheus-stack** on local and managed clouds.

## How it works

```mermaid
flowchart LR
  subgraph cluster["Kubernetes Cluster"]
    subgraph nodes["Worker Nodes"]
      Kubelet1["kubelet / cAdvisor"]
      Kubelet2["kubelet / cAdvisor"]
    end
    API["API Server"]
    subgraph exporter["Binbots Exporter (DaemonSet)"]
      Exporter1["k8s-ai-exporter :9100"]
      Exporter2["k8s-ai-exporter :9100"]
    end
    Prom["Prometheus"]
    Agent["k8s-ai-agent (CronJob)"]
  end

  Kubelet1 -->|"proxy /metrics"| API
  Kubelet2 -->|"proxy /metrics"| API
  API -->|"scrape via proxy"| Exporter1
  API -->|"scrape via proxy"| Exporter2
  Exporter1 -->|"expose aggregated metrics"| Prom
  Exporter2 -->|"expose aggregated metrics"| Prom
  Prom -->|"query range API"| Agent
  Agent -->|"CPU/memory predictions & recommendations"| Output["Recommendations (logs / future: Slack, Grafana)"]
```

**Flow summary**

| Step | Component | Action |
|------|-----------|--------|
| 1 | **k8s-ai-exporter** (Go, DaemonSet) | Scrapes kubelet/cAdvisor via API server proxy per node; filters out Succeeded/Failed pods; aggregates CPU/memory per node. |
| 2 | **k8s-ai-exporter** | Exposes Prometheus metrics on `:9100/metrics` (`k8s_node_cpu_usage_cores`, `k8s_node_memory_usage_bytes`, `k8s_node_active_pods`). |
| 3 | **Prometheus** (kube-prometheus-stack) | Scrapes each exporter pod (ServiceMonitor); stores time series. |
| 4 | **k8s-ai-agent** (Python, CronJob) | Pulls metrics from Prometheus, runs trend prediction (Prophet or simple stats), prints optimization suggestions. |

## Layout

```
.
├── go/                    # Go exporter (DaemonSet)
│   ├── go.mod
│   ├── main.go
│   ├── metrics_test.go
│   └── Dockerfile
├── python/                # AI agent (CronJob)
│   ├── requirements.txt
│   ├── ai_agent.py
│   ├── test_ai_agent.py
│   └── Dockerfile
├── deploy/                # Kubernetes manifests (separate YAMLs)
│   ├── namespace.yaml
│   ├── serviceaccount.yaml
│   ├── clusterrole.yaml
│   ├── clusterrolebinding.yaml
│   ├── daemonset-exporter.yaml
│   ├── service-exporter.yaml
│   ├── servicemonitor-exporter.yaml
│   ├── prometheusrule-binbots.yaml
│   ├── cronjob-ai-agent.yaml
│   └── grafana-dashboard-binbots.json
├── helm/                  # Helm chart (see "Helm chart" section)
│   ├── Chart.yaml
│   ├── values.yaml
│   ├── values-dev.yaml
│   ├── values-prod.yaml
│   ├── dashboards/
│   └── templates/
├── CHANGELOG.md
└── README.md
```

## Prerequisites

- Kubernetes cluster (local: kind/minikube/k3d, or EKS/AKS/GKE)
- **kube-prometheus-stack** installed (Prometheus Operator) in `monitoring` (or same namespace as below)
- `kubectl` and cluster access

## 1. Install kube-prometheus-stack (if not already)

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
helm install prometheus-stack prometheus-community/kube-prometheus-stack -n monitoring --create-namespace
```

If your release name is different, set the same label on the ServiceMonitor (see below).

## 2. Build and push images

Replace `your-registry` with your container registry (e.g. Docker Hub, ECR, GCR, ACR).

```bash
# Go exporter
docker build -t your-registry/k8s-ai-exporter:latest ./go
docker push your-registry/k8s-ai-exporter:latest

# Python AI agent
docker build -t your-registry/k8s-ai-agent:latest ./python
docker push your-registry/k8s-ai-agent:latest
```

## 3. Update image references in deploy/

Edit:

- `deploy/daemonset-exporter.yaml`: set `image: your-registry/k8s-ai-exporter:latest`
- `deploy/cronjob-ai-agent.yaml`: set `image: your-registry/k8s-ai-agent:latest`

## 4. Deploy (order matters)

Apply in this order (all YAMLs in `deploy/` are separate files):

```bash
kubectl apply -f deploy/namespace.yaml
kubectl apply -f deploy/serviceaccount.yaml
kubectl apply -f deploy/clusterrole.yaml
kubectl apply -f deploy/clusterrolebinding.yaml
kubectl apply -f deploy/daemonset-exporter.yaml
kubectl apply -f deploy/service-exporter.yaml
kubectl apply -f deploy/servicemonitor-exporter.yaml
kubectl apply -f deploy/prometheusrule-binbots.yaml
kubectl apply -f deploy/cronjob-ai-agent.yaml
```

Or apply the whole directory (order is deterministic by filename):

```bash
kubectl apply -f deploy/
```

## 5. ServiceMonitor label (kube-prometheus-stack)

The ServiceMonitor uses:

```yaml
labels:
  release: prometheus-stack
```

This must match the **Helm release name** of your kube-prometheus-stack. If you installed with a different name (e.g. `kube-prometheus-stack`), edit `deploy/servicemonitor-exporter.yaml` and set:

```yaml
labels:
  release: <your-helm-release-name>
```

Alternatively, configure Prometheus to select all ServiceMonitors (e.g. `serviceMonitorSelector: {}` in the Helm values).

## 6. Prometheus URL for the AI agent

The CronJob uses:

`PROMETHEUS_URL=http://prometheus-kube-prometheus-prometheus.monitoring.svc:9090`

That matches the default service name from **kube-prometheus-stack**. If your Prometheus service has a different name, set the `PROMETHEUS_URL` env in `deploy/cronjob-ai-agent.yaml`.

## 7. Verify

- Exporter (one pod per node):

  ```bash
  kubectl get pods -n monitoring -l app=k8s-ai-exporter
  kubectl port-forward -n monitoring svc/k8s-ai-exporter 9100:9100
  curl http://localhost:9100/metrics
  ```

- Prometheus should scrape the exporter (targets UI or `k8s_node_cpu_usage_cores`).
- AI agent runs every 10 minutes; to run once:

  ```bash
  kubectl create job -n monitoring ai-agent-manual --from=cronjob/k8s-ai-agent
  kubectl logs -n monitoring job/ai-agent-manual -f
  ```

## Environments

- **Local (kind, minikube, k3d, k3s)**: Same manifests; ensure kube-prometheus-stack and this stack share the same namespace or adjust `namespaceSelector` / Prometheus URL.
- **EKS / AKS / GKE**: Exporter uses API server proxy only (no direct kubelet node port); no `hostNetwork` or privileged pods. Same YAMLs and ServiceMonitor.

### Cloud notes (EKS / AKS / GKE)

- **API server proxy only**: The Go exporter talks to kubelet/cAdvisor via the Kubernetes API server (`/api/v1/nodes/<node>/proxy/...`), so you do not need to open `10250` on node IPs or run privileged/hostNetwork pods.
- **kube-prometheus-stack**: Install it in the same namespace (`monitoring` by default) and keep the `ServiceMonitor` label `release: prometheus-stack` (or set it to your actual Helm release name).
- **IAM / identity**:
  - EKS: you can run with standard in-cluster ServiceAccount tokens; for locked-down clusters, map the `k8s-ai-exporter` ServiceAccount to an IRSA role if you later add cloud APIs.
  - GKE / AKS: workload identity is only needed if the exporter or AI agent calls cloud provider APIs; Binbots-k8s itself only talks to the Kubernetes API and Prometheus.
- **Namespaces**: The manifests and Helm chart default to `monitoring`. You can change that, but ensure:
  - kube-prometheus-stack is configured to watch that namespace (via `serviceMonitorSelector` / `namespaceSelector`),
  - the CronJob’s `PROMETHEUS_URL` points at the correct Prometheus service FQDN in that namespace.

## Testing

- **Go:** From `go/` run `go test -v ./...` to run unit tests for metric parsing (`parsePrometheusValue`, `parseContainerMetrics`).
- **Python:** From `python/` run `pip install -r requirements.txt` then `pytest test_ai_agent.py -v` to run tests for recommendation logic and forecast helpers.

## Optional

- **Grafana**: The Helm chart creates a ConfigMap with label `grafana_dashboard: "1"` (and optional `grafana_dashboard_folder`) so kube-prometheus-stack’s Grafana sidecar can load it. If you deploy with raw YAML only, import `deploy/grafana-dashboard-binbots.json` in Grafana UI (Dashboards → Import → Upload JSON). The dashboard shows `k8s_node_cpu_usage_cores`, `k8s_node_memory_usage_bytes`, `k8s_node_active_pods`.
- **Slack / webhook**: Extend `ai_agent.py` to POST recommendations to a webhook.
- **Different schedule**: Change `schedule` in `deploy/cronjob-ai-agent.yaml` or `.Values.agent.schedule` in the Helm chart (e.g. `"*/5 * * * *"` for every 5 minutes).

## Helm chart

There is a simple Helm chart under `helm/` that deploys:

- `k8s-ai-exporter` DaemonSet, Service, ServiceAccount, ClusterRole, ClusterRoleBinding
- `k8s-ai-agent` CronJob
- Optional `ServiceMonitor` for kube-prometheus-stack
- Optional Grafana dashboard ConfigMap (embedded from `helm/dashboards/`)
- Optional PrometheusRule (exporter down, agent not run)

Basic usage:

```bash
cd helm
helm install binbots-k8s . \
  --set image.exporter.repository=your-registry/k8s-ai-exporter \
  --set image.agent.repository=your-registry/k8s-ai-agent
```

You can override `namespace`, Prometheus release label, and dashboard options in `values.yaml`.

**Multi-environment:** Use `values-dev.yaml` or `values-prod.yaml` for environment-specific overrides (schedule, resources, alert thresholds, image tags):

```bash
# Dev
helm upgrade --install binbots-k8s . -f values.yaml -f values-dev.yaml --set image.exporter.repository=myreg/k8s-ai-exporter --set image.agent.repository=myreg/k8s-ai-agent

# Prod (pin image tags)
helm upgrade --install binbots-k8s . -f values.yaml -f values-prod.yaml --set image.exporter.repository=myreg/k8s-ai-exporter --set image.exporter.tag=v0.2.0 --set image.agent.repository=myreg/k8s-ai-agent --set image.agent.tag=v0.2.0
```

