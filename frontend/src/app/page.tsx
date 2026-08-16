"use client";
// src/app/page.tsx — GopherProxy Observability Dashboard
//
// PURPOSE: Auto-refreshing metrics dashboard reading live data from Prometheus.
//
// WHY "use client":
// - useState to hold fetched metrics and loading state
// - useEffect to trigger fetch on mount AND set up auto-refresh interval
// - setInterval for periodic refresh (browser API — not available server-side)
//
// DATA FLOW:
//   1. Component mounts in browser
//   2. useEffect fires → calls fetchAllMetrics() from lib/metrics.ts
//   3. metrics.ts queries Prometheus HTTP API (/api/v1/query) with PromQL expressions
//   4. Results typed as ProxyMetrics and stored in useState
//   5. Component re-renders with live values
//   6. setInterval fires every 10 seconds → repeat from step 2
//
// WHY useEffect AND NOT server-side fetch here?
// The dashboard needs real-time updates — it must run a setInterval in the browser.
// A Server Component runs once per request and cannot maintain an interval.
// For a static first load of metrics, a Server Component would work, but
// you'd lose the live-update behaviour.
//
// INTERVIEW QUESTION: "How would you improve this for production?"
// Answer: Use SWR or React Query for polling with automatic retry and
// deduplication. Add WebSocket push from a backend if sub-second latency matters.
// Use Next.js ISR (Incremental Static Regeneration) for the initial data load.

import { useState, useEffect, useCallback } from "react";
import { fetchAllMetrics, ProxyMetrics } from "./lib/metrics";

const REFRESH_INTERVAL_MS = 10_000; // 10 seconds

export default function DashboardPage() {
  const [metrics, setMetrics] = useState<ProxyMetrics | null>(null);
  const [loading, setLoading] = useState(true);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  // ── Fetch function (used by auto-refresh + manual refresh button) ──────────
  const refresh = useCallback(async (isManual = false) => {
    if (isManual) setRefreshing(true);
    try {
      const data = await fetchAllMetrics();
      setMetrics(data);
      setLastUpdated(new Date());
    } finally {
      setLoading(false);
      if (isManual) setRefreshing(false);
    }
  }, []);

  // ── Mount: initial fetch + set up auto-refresh interval ───────────────────
  useEffect(() => {
    refresh();
    const interval = setInterval(() => refresh(), REFRESH_INTERVAL_MS);
    // Cleanup: clear the interval when component unmounts to prevent memory leaks
    // This is important! Without cleanup, the interval keeps running after navigation.
    return () => clearInterval(interval);
  }, [refresh]);

  // ── Derived convenience values ─────────────────────────────────────────────
  const proxyStatus = metrics === null
    ? "loading"
    : metrics.proxyOnline
    ? "online"
    : "offline";

  const prometheusStatus = metrics === null
    ? "loading"
    : metrics.prometheusOnline
    ? "online"
    : "offline";

  // For the response-by-status bar chart, find the max value for % scaling
  const maxResponses = metrics
    ? Math.max(...metrics.responsesByStatus.map((r) => r.value), 1)
    : 1;

  // Classify HTTP status code strings into groups for coloring
  function statusClass(code: string): string {
    if (code.startsWith("2")) return "s2xx";
    if (code.startsWith("4")) return "s4xx";
    return "s5xx";
  }

  // ── Render ─────────────────────────────────────────────────────────────────
  return (
    <>
      {/* ── Header ─────────────────────────────────────────────────────────── */}
      <header className="header">
        <div className="container">
          <div className="header-inner">
            <div className="brand">
              <span className="brand-name">
                Gopher<span>Proxy</span>
              </span>
              <span className="brand-tag">observability dashboard</span>
            </div>
            <div className="header-right">
              {lastUpdated && (
                <span className="last-updated">
                  updated {lastUpdated.toLocaleTimeString()}
                </span>
              )}
              <button
                id="refresh-btn"
                className="refresh-btn"
                onClick={() => refresh(true)}
                disabled={refreshing || loading}
                title="Refresh metrics now"
              >
                {refreshing ? <span className="spinner" /> : "↻"}
                Refresh
              </button>
            </div>
          </div>
        </div>
      </header>

      {/* ── Main content ───────────────────────────────────────────────────── */}
      <main className="container">
        <div className="page-header">
          <h1 className="page-title">Proxy Metrics</h1>
          <p className="page-subtitle">
            Live data from Prometheus · auto-refreshes every {REFRESH_INTERVAL_MS / 1000}s
          </p>
        </div>

        {/* ── Status pills ─────────────────────────────────────────────────── */}
        <div className="status-banner">
          <div
            id="proxy-status"
            className={`status-pill ${proxyStatus === "loading" ? "unknown" : proxyStatus}`}
          >
            <span className={`status-dot ${proxyStatus === "online" ? "pulse" : ""}`} />
            GopherProxy :8080&nbsp;
            {proxyStatus === "loading"
              ? "Checking…"
              : proxyStatus === "online"
              ? "Online"
              : "Offline"}
          </div>
          <div
            id="prometheus-status"
            className={`status-pill ${prometheusStatus === "loading" ? "unknown" : prometheusStatus}`}
          >
            <span className={`status-dot ${prometheusStatus === "online" ? "pulse" : ""}`} />
            Prometheus :9090&nbsp;
            {prometheusStatus === "loading"
              ? "Checking…"
              : prometheusStatus === "online"
              ? "Online"
              : "Offline"}
          </div>
        </div>

        {/* ── Loading skeleton ─────────────────────────────────────────────── */}
        {loading && (
          <div className="offline-notice">
            <div className="offline-icon">📡</div>
            <p className="offline-title">Connecting to Prometheus…</p>
            <p className="offline-hint">
              Make sure GopherProxy stack is running: <code>make up</code>
            </p>
          </div>
        )}

        {/* ── Metrics grid ─────────────────────────────────────────────────── */}
        {!loading && (
          <>
            {!metrics?.prometheusOnline && (
              <div className="section" style={{ marginBottom: "24px" }}>
                <div className="offline-notice" style={{ padding: "24px 0" }}>
                  <div className="offline-icon">🔌</div>
                  <p className="offline-title">Prometheus is unreachable</p>
                  <p className="offline-hint">
                    Start the stack: <code>make up</code> or <code>docker compose up -d</code>
                  </p>
                </div>
              </div>
            )}

            <div className="metrics-grid">
              {/* Active Backends */}
              <div
                className="metric-card"
                id="card-active-backends"
                style={{
                  "--card-accent": metrics?.activeBackends !== null && metrics!.activeBackends! > 0
                    ? "var(--green)"
                    : "var(--red)",
                  "--card-color":  metrics?.activeBackends !== null && metrics!.activeBackends! > 0
                    ? "var(--green)"
                    : "var(--red)",
                } as React.CSSProperties}
              >
                <div className="metric-label">Active Backends</div>
                {metrics?.activeBackends !== null ? (
                  <div className="metric-value">{metrics?.activeBackends ?? "—"}</div>
                ) : (
                  <div className="metric-null">—</div>
                )}
                <div className="metric-unit">alive nodes</div>
              </div>

              {/* Processed Requests */}
              <div
                className="metric-card"
                id="card-processed"
                style={{
                  "--card-accent": "var(--blue)",
                  "--card-color": "var(--blue)",
                } as React.CSSProperties}
              >
                <div className="metric-label">Requests Proxied</div>
                {metrics?.processedRequests !== null ? (
                  <div className="metric-value">
                    {metrics?.processedRequests !== null
                      ? (metrics!.processedRequests! >= 1000
                        ? (metrics!.processedRequests! / 1000).toFixed(1) + "k"
                        : metrics!.processedRequests!.toFixed(0))
                      : "—"}
                  </div>
                ) : (
                  <div className="metric-null">—</div>
                )}
                <div className="metric-unit">total (since startup)</div>
              </div>

              {/* Dropped Requests */}
              <div
                className="metric-card"
                id="card-dropped"
                style={{
                  "--card-accent": "var(--yellow)",
                  "--card-color": "var(--yellow)",
                } as React.CSSProperties}
              >
                <div className="metric-label">Dropped / Rate-Limited</div>
                {metrics?.droppedRequests !== null ? (
                  <div className="metric-value">
                    {metrics?.droppedRequests !== null
                      ? (metrics!.droppedRequests! >= 1000
                        ? (metrics!.droppedRequests! / 1000).toFixed(1) + "k"
                        : metrics!.droppedRequests!.toFixed(0))
                      : "—"}
                  </div>
                ) : (
                  <div className="metric-null">—</div>
                )}
                <div className="metric-unit">429 + 503 responses</div>
              </div>

              {/* P95 Latency */}
              <div
                className="metric-card"
                id="card-p95"
                style={{
                  "--card-accent": "var(--cyan)",
                  "--card-color": "var(--cyan)",
                } as React.CSSProperties}
              >
                <div className="metric-label">P95 Latency</div>
                {metrics?.p95LatencyMs !== null ? (
                  <>
                    <div className="metric-value">{metrics?.p95LatencyMs ?? "—"}</div>
                    <div className="metric-unit">ms (2min window)</div>
                  </>
                ) : (
                  <>
                    <div className="metric-null">—</div>
                    <div className="metric-unit">no traffic yet</div>
                  </>
                )}
              </div>
            </div>

            {/* ── Response by status code ─────────────────────────────────── */}
            <div className="section">
              <div className="section-title">
                <span>📊</span> Responses by HTTP Status Code
              </div>

              {metrics?.responsesByStatus && metrics.responsesByStatus.length > 0 ? (
                <div className="status-bars" id="status-bars">
                  {[...metrics.responsesByStatus]
                    .sort((a, b) => a.label.localeCompare(b.label))
                    .map((item) => {
                      const pct = (item.value / maxResponses) * 100;
                      const cls = statusClass(item.label);
                      return (
                        <div key={item.label} className="status-bar-row" id={`bar-${item.label}`}>
                          <span className={`status-code ${cls}`}>{item.label}</span>
                          <div className="bar-track">
                            <div
                              className={`bar-fill ${cls}`}
                              style={{ width: `${pct}%` }}
                            />
                          </div>
                          <span className="bar-count">
                            {item.value >= 1000
                              ? (item.value / 1000).toFixed(1) + "k"
                              : item.value.toFixed(0)}
                          </span>
                        </div>
                      );
                    })}
                </div>
              ) : (
                <p className="metric-null">
                  No response data yet — send some requests through the proxy first.
                </p>
              )}
            </div>

            {/* ── Architecture note ──────────────────────────────────────── */}
            <div className="section">
              <div className="section-title"><span>🏗</span> How This Works</div>
              <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "16px" }}>
                {[
                  {
                    label: "Data Plane",
                    value: "GopherProxy :8080",
                    note: "Round-robin load balancer with per-IP token-bucket rate limiter"
                  },
                  {
                    label: "Control Plane",
                    value: "Sentinel",
                    note: "TCP health-checks backends → writes SADD/SREM to Redis every 2s"
                  },
                  {
                    label: "Service Registry",
                    value: "Redis SET",
                    note: "gopher_backends key; proxy polls SMEMBERS every 5s — no restart needed"
                  },
                  {
                    label: "Observability",
                    value: "Prometheus :9090",
                    note: "Scrapes 5 metric series from :2112/metrics every 15s; this dashboard reads via HTTP API"
                  },
                ].map((item) => (
                  <div key={item.label} style={{
                    background: "var(--bg-elevated)",
                    borderRadius: "var(--radius-sm)",
                    padding: "12px",
                    border: "1px solid var(--border)"
                  }}>
                    <div style={{
                      fontSize: "0.6875rem",
                      textTransform: "uppercase" as const,
                      letterSpacing: "0.08em",
                      color: "var(--text-muted)",
                      fontFamily: "var(--font-mono)",
                      marginBottom: "4px"
                    }}>{item.label}</div>
                    <div style={{
                      fontSize: "0.875rem",
                      fontWeight: 600,
                      color: "var(--cyan)",
                      fontFamily: "var(--font-mono)",
                      marginBottom: "4px"
                    }}>{item.value}</div>
                    <div style={{
                      fontSize: "0.8125rem",
                      color: "var(--text-secondary)"
                    }}>{item.note}</div>
                  </div>
                ))}
              </div>
            </div>
          </>
        )}
      </main>

      {/* ── Footer ─────────────────────────────────────────────────────────── */}
      <footer className="footer">
        <div className="container" style={{ display: "flex", justifyContent: "space-between", flexWrap: "wrap", gap: "8px" }}>
          <span>GopherProxy · Go + Redis + Prometheus</span>
          <span>Prometheus: {process.env.NEXT_PUBLIC_PROMETHEUS_URL}</span>
        </div>
      </footer>
    </>
  );
}
