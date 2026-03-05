import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createPFLToggle } from './pfl-toggle';

describe('PFL toggle debounce', () => {
	beforeEach(() => { vi.useFakeTimers(); });
	afterEach(() => { vi.useRealTimers(); });

	it('ignores rapid toggles within 100ms', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1'); // enables cam1
		pfl.toggle('cam2'); // should be ignored (busy)
		expect(pfl.activeSource).toBe('cam1');
		expect(ctx.pflManager.enablePFL).toHaveBeenCalledTimes(1);
	});

	it('processes toggle after debounce period', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		vi.advanceTimersByTime(100);
		pfl.toggle('cam2');
		expect(pfl.activeSource).toBe('cam2');
		expect(ctx.pflManager.enablePFL).toHaveBeenCalledTimes(2);
	});

	it('concurrent enable/disable settles to final state', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1'); // enable cam1
		vi.advanceTimersByTime(100);
		pfl.toggle('cam1'); // disable cam1
		expect(pfl.activeSource).toBe(null);
		expect(ctx.pflManager.disablePFL).toHaveBeenCalledTimes(1);
	});

	it('mutes previous source and unmutes new source on switch', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		vi.advanceTimersByTime(100);
		pfl.toggle('cam2');

		// cam1 should be muted in pipeline
		expect(ctx.pipeline.setSourceMuted).toHaveBeenCalledWith('cam1', true);
		// cam2 should be unmuted in pipeline
		expect(ctx.pipeline.setSourceMuted).toHaveBeenCalledWith('cam2', false);
		// pflManager should enable cam2
		expect(ctx.pflManager.enablePFL).toHaveBeenCalledWith('cam2');
	});

	it('mutes source when disabling PFL', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1'); // enable
		vi.advanceTimersByTime(100);
		pfl.toggle('cam1'); // disable

		expect(ctx.pflManager.disablePFL).toHaveBeenCalledTimes(1);
		expect(ctx.pipeline.setSourceMuted).toHaveBeenCalledWith('cam1', true);
		expect(pfl.activeSource).toBeNull();
	});

	it('returns the new active source from toggle', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		const result1 = pfl.toggle('cam1');
		expect(result1).toBe('cam1');

		vi.advanceTimersByTime(100);
		const result2 = pfl.toggle('cam1');
		expect(result2).toBeNull();
	});

	it('returns current active source when busy (no change)', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		const result = pfl.toggle('cam2'); // busy, ignored
		expect(result).toBe('cam1'); // returns current, not cam2
	});
});
