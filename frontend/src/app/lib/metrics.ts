// src/app/lib/metrics.ts
//
// PURPOSE: Typed helpers for querying the Prometheus HTTP API.
//
// HOW PROMETHEUS QUERY API WORKS:
// Prometheus stores time-series metrics scraped from /metrics endpoints.
// It exposes an HTTP query API at /api/v1/query (instant query) and
// /api/v1/query_range (range queries for graphs).
//
// We use instant queries: GET /api/v1/query?query=<PromQL expression>
// PromQL is Prometheus's query language.
//
// EXAMPLE:
//   GET http://localhost:9090/api/v1/query?query=gopherproxy_active_backends
//   Response: { "status": "success", "data": { "resultType": "vector",
//               "result": [{ "metric": {...}, "value": [timestamp, "3"] }] } }
//
// WHY not call /metrics directly?
// /metrics returns raw text in the Prometheus exposition format — not JSON.
// The HTTP API (/api/v1/query) returns structured JSON we can parse in TypeScript.
//
// INTERVIEW POINT:
// This is a read-only observability integration. The frontend doesn't control
// the proxy — it only reads data that Prometheus has already scraped.
// This is the correct pattern for an infrastructure dashboard.

const PROMETHEUS_URL =
  process.env.NEXT_PUBLIC_PROMETHEUS_URL ?? "http://localhost:9090";


// ── TypeScript interfaces for Prometheus API responses ────────────────────────
// These match the actual JSON shape returned by /api/v1/query.

interface PrometheusInstantResult {
  metric: Record<string, string>; // label key-value pairs (e.g. { status: "200" })
  value: [number, string];        // [unix_timestamp, "value_as_string"]
}

interface PrometheusQueryResponse {
  status: "success" | "error";
  data: {
    resultType: string;
    result: PrometheusInstantResult[];
  };
  error?: string;
}

// ── Proxy health check ─────────────────────────────────────────────────────────
/**
 * Checks whether GopherProxy is alive by querying gopherproxy_active_backends
 * from Prometheus. If Prometheus has this metric (even at 0), it means
 * Prometheus successfully scraped GopherProxy — so the proxy is up.
 *
 * WHY not call /healthz directly?
 * GopherProxy has no CORS headers. A browser fetch from localhost:3001 to
 * localhost:2112 is blocked by the browser's Same-Origin Policy, even though
 * curl works fine. Routing through Prometheus avoids the CORS issue entirely.
 */
export async function checkProxyHealth(): Promise<boolean> {
  try {
    const url = `${PROMETHEUS_URL}/api/v1/query?query=${encodeURIComponent("gopherproxy_active_backends")}`;
    const res = await fetch(url, { signal: AbortSignal.timeout(4000) });
    if (!res.ok) return false;
    const json: PrometheusQueryResponse = await res.json();
    // If Prometheus has any result for this metric, the proxy is being scraped
    return json.status === "success" && json.data.result.length > 0;
  } catch {
    return false;
  }
}

// ── Prometheus instant query ───────────────────────────────────────────────────
/**
 * Execute a PromQL instant query. Returns the first numeric result value,
 * or null if the query returns no data or fails.
 *
 * @param promql - A PromQL expression, e.g. "gopherproxy_active_backends"
 */
async function queryScalar(promql: string): Promise<number | null> {
  try {
    const url = `${PROMETHEUS_URL}/api/v1/query?query=${encodeURIComponent(promql)}`;
    const res = await fetch(url, { signal: AbortSignal.timeout(5000) });
    if (!res.ok) return null;
    const json: PrometheusQueryResponse = await res.json();
    if (json.status !== "success" || json.data.result.length === 0) return null;
    return parseFloat(json.data.result[0].value[1]);
  } catch {
    return null;
  }
}

/**
 * Execute a PromQL query that returns multiple labeled results (e.g. by status code).
 * Returns an array of { label, value } pairs or empty array on failure.
 */
async function queryLabeled(
  promql: string,
  labelKey: string
): Promise<Array<{ label: string; value: number }>> {
  try {
    const url = `${PROMETHEUS_URL}/api/v1/query?query=${encodeURIComponent(promql)}`;
    const res = await fetch(url, { signal: AbortSignal.timeout(5000) });
    if (!res.ok) return [];
    const json: PrometheusQueryResponse = await res.json();
    if (json.status !== "success") return [];
    return json.data.result.map((r) => ({
      label: r.metric[labelKey] ?? "unknown",
      value: parseFloat(r.value[1]),
    }));
  } catch {
    return [];
  }
}

// ── Exported metric types ──────────────────────────────────────────────────────
export interface ProxyMetrics {
  proxyOnline: boolean;
  prometheusOnline: boolean;
  activeBackends: number | null;
  processedRequests: number | null;
  droppedRequests: number | null;
  p95LatencyMs: number | null;  // converted from seconds to ms
  responsesByStatus: Array<{ label: string; value: number }>;
}

// ── Main export: fetch all metrics in one call ─────────────────────────────────
/**
 * Fetches all relevant GopherProxy metrics from Prometheus.
 * All individual fetches run in parallel using Promise.allSettled —
 * if one fails (metric doesn't exist yet), the others still succeed.
 */
export async function fetchAllMetrics(): Promise<ProxyMetrics> {
  // Run all queries in parallel — don't wait for one to finish before starting next
  const [
    proxyOnline,
    activeBackends,
    processedRequests,
    droppedRequests,
    p95LatencyRaw,
    responsesByStatus,
  ] = await Promise.all([
    checkProxyHealth(),
    queryScalar("gopherproxy_active_backends"),
    queryScalar("gopherproxy_processed_requests_total"),
    queryScalar("gopherproxy_dropped_requests_total"),
    // P95 latency: histogram_quantile aggregates bucket data into a single value
    // rate() computes per-second rate over a 2-minute window
    queryScalar(
      "histogram_quantile(0.95, rate(gopherproxy_request_duration_seconds_bucket[2m]))"
    ),
    queryLabeled("gopherproxy_responses_total", "status"),
  ]);

  // Prometheus stores duration in seconds; convert to milliseconds for display
  const p95LatencyMs =
    p95LatencyRaw !== null ? Math.round(p95LatencyRaw * 1000 * 100) / 100 : null;

  // If we could reach Prometheus to get active backends, Prometheus is online
  const prometheusOnline = activeBackends !== null || processedRequests !== null;

  return {
    proxyOnline,
    prometheusOnline,
    activeBackends,
    processedRequests,
    droppedRequests,
    p95LatencyMs,
    responsesByStatus,
  };
}
