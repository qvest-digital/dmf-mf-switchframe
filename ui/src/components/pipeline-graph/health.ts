import type { HealthStatus } from './types';

/** Source health based on status string from server.
 *  Server sends: 'healthy', 'stale', 'no_signal', 'offline'. */
export function sourceHealth(status: string): HealthStatus {
	if (status === 'healthy') return 'healthy';
	if (status === 'stale') return 'degraded';
	return 'error'; // no_signal, offline, or unknown
}

/** Decoder health based on last decode time.
 *  Drop count is cumulative (newest-wins policy), so only high counts degrade. */
export function decodeHealth(lastNs: number, drops: number): HealthStatus {
	if (lastNs > 25_000_000) return 'error';
	if (lastNs > 10_000_000 || drops > 100) return 'degraded';
	return 'healthy';
}

/** Pipeline node health relative to its frame budget. */
export function pipelineNodeHealth(lastNs: number, budgetNs: number): HealthStatus {
	if (budgetNs <= 0) return 'healthy';
	const ratio = lastNs / budgetNs;
	if (ratio > 2) return 'error';
	if (ratio > 1) return 'degraded';
	return 'healthy';
}

/** Audio mixer health based on mode and latency.
 *  Error counts are cumulative (lifetime) so we only degrade, not error,
 *  to avoid permanent red from a single past hiccup. */
export function audioMixerHealth(
	mode: string,
	lastNs: number,
	decodeErrors: number,
	encodeErrors: number
): HealthStatus {
	if (mode === 'passthrough') return 'healthy';
	if (lastNs > 15_000_000) return 'error';
	if (lastNs > 5_000_000) return 'degraded';
	if (decodeErrors > 10 || encodeErrors > 10) return 'degraded';
	return 'healthy';
}

/** SRT ingest health based on packet loss rate. */
export function srtIngestHealth(lossRatePct: number): HealthStatus {
	if (lossRatePct >= 5) return 'error';
	if (lossRatePct >= 1) return 'degraded';
	return 'healthy';
}

/** SRT output health based on ring buffer overflow count. */
export function srtOutputHealth(overflowCount: number): HealthStatus {
	if (overflowCount > 0) return 'error';
	return 'healthy';
}

/** Preview encoder health based on encode time.
 *  Dropped frames are cumulative, so high counts only degrade. */
export function previewEncodeHealth(lastEncodeMs: number, framesDropped: number): HealthStatus {
	if (lastEncodeMs > 15) return 'error';
	if (lastEncodeMs > 5 || framesDropped > 100) return 'degraded';
	return 'healthy';
}

/** Browser decoder health based on cumulative decode error count. */
export function browserDecodeHealth(decodeErrors: number): HealthStatus {
	if (decodeErrors >= 5) return 'error';
	if (decodeErrors > 0) return 'degraded';
	return 'healthy';
}

/** Buffer health based on fill level relative to capacity. */
export function bufferHealth(fill: number, capacity: number): HealthStatus {
	if (capacity <= 0) return 'healthy';
	const pct = fill / capacity;
	if (pct >= 0.8) return 'error';
	if (pct >= 0.5) return 'degraded';
	return 'healthy';
}

/** Map health status to CSS color variable with fallback. */
export function healthColor(status: HealthStatus): string {
	switch (status) {
		case 'healthy':
			return 'var(--health-green, #22c55e)';
		case 'degraded':
			return 'var(--health-yellow, #eab308)';
		case 'error':
			return 'var(--health-red, #ef4444)';
	}
}

/** Format nanoseconds as milliseconds with 1 decimal place. */
export function nsToMs(ns: number): string {
	return (ns / 1_000_000).toFixed(1) + 'ms';
}

/** Format bytes as human-readable string (B/KB/MB/GB). */
export function formatBytes(bytes: number): string {
	if (bytes < 1024) return bytes + 'B';
	if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + 'KB';
	if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + 'MB';
	return (bytes / (1024 * 1024 * 1024)).toFixed(1) + 'GB';
}
