#!/usr/bin/env python3
"""
AI Agent: pulls metrics from Prometheus, runs trend prediction for CPU/memory,
and prints optimization suggestions. Safe to run as CronJob in-cluster.
"""
import os
import sys
from datetime import datetime, timedelta

import pandas as pd
from prometheus_api_client import PrometheusConnect, MetricRangeDataFrame

# Optional: use Prophet if installed (heavier dependency)
try:
    from prophet import Prophet
    HAS_PROPHET = True
except ImportError:
    HAS_PROPHET = False

PROM_URL = os.getenv("PROMETHEUS_URL", "http://prometheus-kube-prometheus-prometheus.monitoring.svc:9090")
LOOKBACK_MINUTES = int(os.getenv("LOOKBACK_MINUTES", "120"))
FORECAST_MINUTES = int(os.getenv("FORECAST_MINUTES", "30"))
CPU_QUERY = os.getenv("CPU_QUERY", "k8s_node_cpu_usage_cores")
MEM_QUERY = os.getenv("MEM_QUERY", "k8s_node_memory_usage_bytes")


def fetch_timeseries(prom: PrometheusConnect, query: str) -> pd.DataFrame:
    end_time = datetime.utcnow()
    start_time = end_time - timedelta(minutes=LOOKBACK_MINUTES)
    metric_data = prom.get_metric_range_data(
        query,
        start_time=start_time,
        end_time=end_time,
        chunk_size=timedelta(minutes=LOOKBACK_MINUTES),
        step="60s",
    )
    if not metric_data:
        return pd.DataFrame()
    return MetricRangeDataFrame(metric_data)


def _to_prophet_df(ts: pd.DataFrame) -> pd.DataFrame:
    """MetricRangeDataFrame uses timestamp index + 'value'; Prophet needs 'ds' and 'y'."""
    if "value" not in ts.columns:
        return pd.DataFrame()
    if "ds" in ts.columns:
        out = ts[["ds", "value"]].copy()
    else:
        out = ts.reset_index()
        time_col = [c for c in out.columns if c != "value"][0]
        out = out[[time_col, "value"]].rename(columns={time_col: "ds"})
    out = out.rename(columns={"value": "y"}).dropna()
    out["ds"] = pd.to_datetime(out["ds"], utc=True)
    return out


def forecast_prophet(ts: pd.DataFrame) -> tuple[float, float]:
    val_col = "value" if "value" in ts.columns else "y"
    if len(ts) < 5:
        return 0.0, 0.0
    fallback_max = float(ts[val_col].max())
    fallback_mean = float(ts[val_col].mean())
    if not HAS_PROPHET or len(ts) < 20:
        return fallback_max, fallback_mean
    df = _to_prophet_df(ts)
    if len(df) < 20:
        return float(df["y"].max()), float(df["y"].mean())
    model = Prophet(daily_seasonality=False, weekly_seasonality=False)
    model.fit(df)
    future = model.make_future_dataframe(periods=FORECAST_MINUTES, freq="min", include_history=False)
    forecast = model.predict(future)
    return float(forecast["yhat"].max()), float(forecast["yhat"].mean())


def recommend_cpu(node: str, max_val: float, mean_val: float) -> str:
    if max_val > 0.8:
        return "Consider adding CPU (scale up) or moving pods away from this node."
    if mean_val < 0.2:
        return "Node is underutilized; consider consolidating pods and scaling down."
    return "Utilization looks healthy; no change needed."


def recommend_mem(node: str, max_gb: float, mean_gb: float) -> str:
    if max_gb > 14:
        return "High memory usage; consider increasing node size or moving memory-heavy workloads."
    if mean_gb < 2:
        return "Node memory underutilized; consider bin-packing or smaller instance."
    return "Memory utilization acceptable."


def main():
    prom = PrometheusConnect(url=PROM_URL, disable_ssl=True)

    cpu_df = fetch_timeseries(prom, CPU_QUERY)
    mem_df = fetch_timeseries(prom, MEM_QUERY)

    if cpu_df.empty and mem_df.empty:
        print("No metrics returned from Prometheus. Check PROMETHEUS_URL and metric names.")
        sys.exit(1)

    label_col = "node" if "node" in (cpu_df.columns if not cpu_df.empty else mem_df.columns) else "instance"
    nodes = set()
    if not cpu_df.empty and label_col in cpu_df.columns:
        nodes.update(cpu_df[label_col].dropna().unique())
    if not mem_df.empty and label_col in mem_df.columns:
        nodes.update(mem_df[label_col].dropna().unique())

    if not nodes:
        print("No node (or instance) labels found in metrics.")
        sys.exit(1)

    for node in sorted(nodes):
        print(f"\n--- Node: {node} ---")
        if not cpu_df.empty and label_col in cpu_df.columns:
            ts = cpu_df[cpu_df[label_col] == node]
            if len(ts) >= 5:
                max_pred, mean_pred = forecast_prophet(ts)
                rec = recommend_cpu(node, max_pred, mean_pred)
                print(f"  CPU: forecast_max={max_pred:.2f} cores, forecast_mean={mean_pred:.2f} -> {rec}")
            else:
                print(f"  CPU: not enough points ({len(ts)}) for trend.")
        if not mem_df.empty and label_col in mem_df.columns:
            ts = mem_df[mem_df[label_col] == node]
            if len(ts) >= 5:
                max_pred, mean_pred = forecast_prophet(ts)
                max_gb = max_pred / (1024**3)
                mean_gb = mean_pred / (1024**3)
                rec = recommend_mem(node, max_gb, mean_gb)
                print(f"  Memory: forecast_max={max_gb:.2f} GiB, mean={mean_gb:.2f} GiB -> {rec}")
            else:
                print(f"  Memory: not enough points ({len(ts)}) for trend.")
    print()


if __name__ == "__main__":
    main()
