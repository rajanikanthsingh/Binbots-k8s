"""Tests for the AI agent: recommendations and forecast logic."""
import os
import pytest
import pandas as pd
from datetime import datetime, timedelta

# Set env before importing ai_agent so PrometheusConnect isn't required for unit tests
os.environ.setdefault("PROMETHEUS_URL", "http://localhost:9090")

from ai_agent import (
    recommend_cpu,
    recommend_mem,
    forecast_prophet,
    _to_prophet_df,
    LOOKBACK_MINUTES,
    FORECAST_MINUTES,
)


class TestRecommendCpu:
    def test_high_utilization(self):
        r = recommend_cpu("node-1", 0.9, 0.7)
        assert "scale up" in r or "Consider adding" in r or "moving pods" in r

    def test_low_utilization(self):
        r = recommend_cpu("node-1", 0.1, 0.15)
        assert "underutilized" in r or "consolidating" in r or "scale down" in r

    def test_healthy(self):
        r = recommend_cpu("node-1", 0.5, 0.4)
        assert "healthy" in r or "no change" in r


class TestRecommendMem:
    def test_high_memory(self):
        r = recommend_mem("node-1", 16.0, 12.0)
        assert "High memory" in r or "increasing" in r

    def test_low_memory(self):
        r = recommend_mem("node-1", 1.0, 0.5)
        assert "underutilized" in r or "bin-packing" in r or "smaller" in r

    def test_acceptable_memory(self):
        r = recommend_mem("node-1", 8.0, 6.0)
        assert "acceptable" in r or "acceptable" in r


class TestToProphetDf:
    def test_renames_columns(self):
        ts = pd.DataFrame({"value": [1.0, 2.0], "timestamp": pd.date_range("2024-01-01", periods=2, freq="min")})
        ts = ts.set_index("timestamp")
        out = _to_prophet_df(ts)
        assert "ds" in out.columns and "y" in out.columns
        assert list(out["y"]) == [1.0, 2.0]

    def test_empty_value_column(self):
        ts = pd.DataFrame({"other": [1, 2]})
        out = _to_prophet_df(ts)
        assert out.empty


class TestForecastProphet:
    def test_few_points_returns_fallback(self):
        ts = pd.DataFrame({"value": [1.0, 2.0, 3.0]})
        max_v, mean_v = forecast_prophet(ts)
        assert max_v == 3.0
        assert mean_v == 2.0

    def test_empty_returns_zero(self):
        ts = pd.DataFrame()
        max_v, mean_v = forecast_prophet(ts)
        assert max_v == 0.0 and mean_v == 0.0

    def test_single_column_y(self):
        ts = pd.DataFrame({"y": [1.0, 2.0, 3.0, 4.0, 5.0]})
        max_v, mean_v = forecast_prophet(ts)
        assert max_v >= 0 and mean_v >= 0
