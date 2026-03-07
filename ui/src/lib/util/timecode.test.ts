import { describe, it, expect } from 'vitest';
import { formatTimecode, formatClipDuration } from './timecode';

describe('formatTimecode', () => {
	it('formats Unix ms as HH:MM:SS.mmm', () => {
		// 2024-01-15 14:30:45.123 UTC
		const ts = new Date('2024-01-15T14:30:45.123Z').getTime();
		const result = formatTimecode(ts);
		// Exact output depends on local timezone, but format must be HH:MM:SS.mmm
		expect(result).toMatch(/^\d{2}:\d{2}:\d{2}\.\d{3}$/);
	});

	it('returns empty string for 0', () => {
		expect(formatTimecode(0)).toBe('');
	});

	it('returns empty string for undefined', () => {
		expect(formatTimecode(undefined)).toBe('');
	});
});

describe('formatClipDuration', () => {
	it('formats millisecond delta as M:SS.s', () => {
		expect(formatClipDuration(65500)).toBe('1:05.5');
	});

	it('formats sub-minute duration', () => {
		expect(formatClipDuration(12300)).toBe('0:12.3');
	});

	it('returns empty string when either mark is missing', () => {
		expect(formatClipDuration(0)).toBe('');
	});
});
