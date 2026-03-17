import { describe, it, expect } from 'vitest';
import {
	sourceHealth,
	decodeHealth,
	pipelineNodeHealth,
	audioMixerHealth,
	srtIngestHealth,
	srtOutputHealth,
	previewEncodeHealth,
	browserDecodeHealth,
	bufferHealth,
	healthColor,
	nsToMs,
	formatBytes
} from './health';

describe('sourceHealth', () => {
	it('returns healthy for online', () => {
		expect(sourceHealth('online')).toBe('healthy');
	});

	it('returns degraded for stale', () => {
		expect(sourceHealth('stale')).toBe('degraded');
	});

	it('returns error for offline', () => {
		expect(sourceHealth('offline')).toBe('error');
	});

	it('returns error for no_signal', () => {
		expect(sourceHealth('no_signal')).toBe('error');
	});

	it('returns error for empty string', () => {
		expect(sourceHealth('')).toBe('error');
	});

	it('returns error for unknown status', () => {
		expect(sourceHealth('something_else')).toBe('error');
	});
});

describe('decodeHealth', () => {
	it('returns healthy when fast and no drops', () => {
		expect(decodeHealth(5_000_000, 0)).toBe('healthy');
	});

	it('returns healthy at exactly 10ms boundary with no drops', () => {
		expect(decodeHealth(10_000_000, 0)).toBe('healthy');
	});

	it('returns degraded above 10ms with no drops', () => {
		expect(decodeHealth(10_000_001, 0)).toBe('degraded');
	});

	it('returns degraded at 25ms boundary with no drops', () => {
		expect(decodeHealth(25_000_000, 0)).toBe('degraded');
	});

	it('returns error above 25ms', () => {
		expect(decodeHealth(25_000_001, 0)).toBe('error');
	});

	it('returns error when drops > 0 even if fast', () => {
		expect(decodeHealth(1_000_000, 1)).toBe('error');
	});

	it('returns error when drops > 0 and slow', () => {
		expect(decodeHealth(30_000_000, 5)).toBe('error');
	});

	it('returns healthy at 0ns with 0 drops', () => {
		expect(decodeHealth(0, 0)).toBe('healthy');
	});
});

describe('pipelineNodeHealth', () => {
	it('returns healthy when under budget', () => {
		expect(pipelineNodeHealth(15_000_000, 33_000_000)).toBe('healthy');
	});

	it('returns healthy at exactly 1x budget', () => {
		expect(pipelineNodeHealth(33_000_000, 33_000_000)).toBe('healthy');
	});

	it('returns degraded above 1x budget', () => {
		expect(pipelineNodeHealth(33_000_001, 33_000_000)).toBe('degraded');
	});

	it('returns degraded at exactly 2x budget', () => {
		expect(pipelineNodeHealth(66_000_000, 33_000_000)).toBe('degraded');
	});

	it('returns error above 2x budget', () => {
		expect(pipelineNodeHealth(66_000_001, 33_000_000)).toBe('error');
	});

	it('returns healthy when budget is 0', () => {
		expect(pipelineNodeHealth(100_000_000, 0)).toBe('healthy');
	});

	it('returns healthy when budget is negative', () => {
		expect(pipelineNodeHealth(100_000_000, -1)).toBe('healthy');
	});
});

describe('audioMixerHealth', () => {
	it('returns healthy in passthrough mode', () => {
		expect(audioMixerHealth('passthrough', 100_000_000, 0, 0)).toBe('healthy');
	});

	it('returns healthy when mixing under 5ms', () => {
		expect(audioMixerHealth('mixing', 4_000_000, 0, 0)).toBe('healthy');
	});

	it('returns healthy at exactly 5ms boundary', () => {
		expect(audioMixerHealth('mixing', 5_000_000, 0, 0)).toBe('healthy');
	});

	it('returns degraded above 5ms', () => {
		expect(audioMixerHealth('mixing', 5_000_001, 0, 0)).toBe('degraded');
	});

	it('returns degraded at exactly 15ms boundary', () => {
		expect(audioMixerHealth('mixing', 15_000_000, 0, 0)).toBe('degraded');
	});

	it('returns error above 15ms', () => {
		expect(audioMixerHealth('mixing', 15_000_001, 0, 0)).toBe('error');
	});

	it('returns error with decode errors even if fast', () => {
		expect(audioMixerHealth('mixing', 1_000_000, 1, 0)).toBe('error');
	});

	it('returns error with encode errors even if fast', () => {
		expect(audioMixerHealth('mixing', 1_000_000, 0, 1)).toBe('error');
	});

	it('returns error with errors even in passthrough mode', () => {
		expect(audioMixerHealth('passthrough', 0, 1, 0)).toBe('error');
	});
});

describe('srtIngestHealth', () => {
	it('returns healthy at 0% loss', () => {
		expect(srtIngestHealth(0)).toBe('healthy');
	});

	it('returns healthy below 1% loss', () => {
		expect(srtIngestHealth(0.5)).toBe('healthy');
	});

	it('returns degraded at exactly 1% loss', () => {
		expect(srtIngestHealth(1)).toBe('degraded');
	});

	it('returns degraded between 1% and 5%', () => {
		expect(srtIngestHealth(3)).toBe('degraded');
	});

	it('returns error at exactly 5% loss', () => {
		expect(srtIngestHealth(5)).toBe('error');
	});

	it('returns error above 5% loss', () => {
		expect(srtIngestHealth(10)).toBe('error');
	});
});

describe('srtOutputHealth', () => {
	it('returns healthy at 0 overflows', () => {
		expect(srtOutputHealth(0)).toBe('healthy');
	});

	it('returns error at 1 overflow', () => {
		expect(srtOutputHealth(1)).toBe('error');
	});

	it('returns error at many overflows', () => {
		expect(srtOutputHealth(100)).toBe('error');
	});
});

describe('previewEncodeHealth', () => {
	it('returns healthy when fast and no drops', () => {
		expect(previewEncodeHealth(3, 0)).toBe('healthy');
	});

	it('returns healthy at exactly 5ms with no drops', () => {
		expect(previewEncodeHealth(5, 0)).toBe('healthy');
	});

	it('returns degraded above 5ms with no drops', () => {
		expect(previewEncodeHealth(5.1, 0)).toBe('degraded');
	});

	it('returns degraded at exactly 15ms with no drops', () => {
		expect(previewEncodeHealth(15, 0)).toBe('degraded');
	});

	it('returns error above 15ms', () => {
		expect(previewEncodeHealth(15.1, 0)).toBe('error');
	});

	it('returns error with drops even if fast', () => {
		expect(previewEncodeHealth(1, 1)).toBe('error');
	});

	it('returns error with drops and slow', () => {
		expect(previewEncodeHealth(20, 5)).toBe('error');
	});
});

describe('browserDecodeHealth', () => {
	it('returns healthy at 0 errors', () => {
		expect(browserDecodeHealth(0)).toBe('healthy');
	});

	it('returns degraded at 1 error', () => {
		expect(browserDecodeHealth(1)).toBe('degraded');
	});

	it('returns degraded at 4 errors', () => {
		expect(browserDecodeHealth(4)).toBe('degraded');
	});

	it('returns error at 5 errors', () => {
		expect(browserDecodeHealth(5)).toBe('error');
	});

	it('returns error above 5 errors', () => {
		expect(browserDecodeHealth(100)).toBe('error');
	});
});

describe('bufferHealth', () => {
	it('returns healthy when empty', () => {
		expect(bufferHealth(0, 100)).toBe('healthy');
	});

	it('returns healthy below 50%', () => {
		expect(bufferHealth(49, 100)).toBe('healthy');
	});

	it('returns degraded at exactly 50%', () => {
		expect(bufferHealth(50, 100)).toBe('degraded');
	});

	it('returns degraded between 50% and 80%', () => {
		expect(bufferHealth(70, 100)).toBe('degraded');
	});

	it('returns error at exactly 80%', () => {
		expect(bufferHealth(80, 100)).toBe('error');
	});

	it('returns error above 80%', () => {
		expect(bufferHealth(95, 100)).toBe('error');
	});

	it('returns error when full', () => {
		expect(bufferHealth(100, 100)).toBe('error');
	});

	it('returns healthy when capacity is 0', () => {
		expect(bufferHealth(0, 0)).toBe('healthy');
	});

	it('returns healthy when capacity is negative', () => {
		expect(bufferHealth(5, -1)).toBe('healthy');
	});
});

describe('healthColor', () => {
	it('returns green for healthy', () => {
		expect(healthColor('healthy')).toBe('var(--health-green, #22c55e)');
	});

	it('returns yellow for degraded', () => {
		expect(healthColor('degraded')).toBe('var(--health-yellow, #eab308)');
	});

	it('returns red for error', () => {
		expect(healthColor('error')).toBe('var(--health-red, #ef4444)');
	});
});

describe('nsToMs', () => {
	it('formats 0 nanoseconds', () => {
		expect(nsToMs(0)).toBe('0.0ms');
	});

	it('formats sub-millisecond', () => {
		expect(nsToMs(500_000)).toBe('0.5ms');
	});

	it('formats whole milliseconds', () => {
		expect(nsToMs(10_000_000)).toBe('10.0ms');
	});

	it('formats fractional milliseconds with rounding', () => {
		expect(nsToMs(33_333_333)).toBe('33.3ms');
	});

	it('formats large values', () => {
		expect(nsToMs(1_000_000_000)).toBe('1000.0ms');
	});
});

describe('formatBytes', () => {
	it('formats bytes', () => {
		expect(formatBytes(500)).toBe('500B');
	});

	it('formats 0 bytes', () => {
		expect(formatBytes(0)).toBe('0B');
	});

	it('formats exactly 1023 bytes', () => {
		expect(formatBytes(1023)).toBe('1023B');
	});

	it('formats kilobytes', () => {
		expect(formatBytes(1024)).toBe('1.0KB');
	});

	it('formats kilobytes with decimals', () => {
		expect(formatBytes(1536)).toBe('1.5KB');
	});

	it('formats megabytes', () => {
		expect(formatBytes(1024 * 1024)).toBe('1.0MB');
	});

	it('formats megabytes with decimals', () => {
		expect(formatBytes(1.5 * 1024 * 1024)).toBe('1.5MB');
	});

	it('formats gigabytes', () => {
		expect(formatBytes(1024 * 1024 * 1024)).toBe('1.0GB');
	});

	it('formats gigabytes with decimals', () => {
		expect(formatBytes(2.5 * 1024 * 1024 * 1024)).toBe('2.5GB');
	});
});
