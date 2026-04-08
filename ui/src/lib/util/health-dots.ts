import type { SourceHealthStatus } from '$lib/api/types';

/** Map engine health status to a CSS color for dual-engine health dots. */
export function healthDotColor(status: SourceHealthStatus | 'not_present'): string {
	switch (status) {
		case 'healthy': return '#22c55e';
		case 'stale': return '#f59e0b';
		case 'no_signal': return '#ef4444';
		case 'offline': return '#6b7280';
		case 'not_present': return '#666';
		default: return '#6b7280'; // gray fallback
	}
}
