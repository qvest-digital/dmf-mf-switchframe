import { describe, it, expect } from 'vitest';
import { healthDotColor } from './health-dots';
import type { SourceHealthStatus } from '$lib/api/types';

describe('healthDotColor', () => {
	it('returns green for healthy', () => {
		expect(healthDotColor('healthy')).toBe('#22c55e');
	});

	it('returns amber for stale', () => {
		expect(healthDotColor('stale')).toBe('#f59e0b');
	});

	it('returns red for no_signal', () => {
		expect(healthDotColor('no_signal')).toBe('#ef4444');
	});

	it('returns gray for offline', () => {
		expect(healthDotColor('offline')).toBe('#6b7280');
	});

	it('returns dark gray for not_present', () => {
		expect(healthDotColor('not_present')).toBe('#666');
	});

	it('covers all SourceHealthStatus values', () => {
		const allStatuses: SourceHealthStatus[] = ['healthy', 'stale', 'no_signal', 'offline'];
		for (const status of allStatuses) {
			expect(healthDotColor(status)).toBeTruthy();
		}
	});

	it('returns gray for unknown values', () => {
		expect(healthDotColor('something_unknown' as SourceHealthStatus)).toBe('#6b7280');
	});
});
