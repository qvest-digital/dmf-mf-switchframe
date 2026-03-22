import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createPFLToggle } from './pfl-toggle';

describe('PFL/AFL toggle', () => {
	beforeEach(() => { vi.useFakeTimers(); });
	afterEach(() => { vi.useRealTimers(); });

	it('ignores rapid toggles within 100ms', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1'); // enables cam1 PFL
		pfl.toggle('cam2'); // should be ignored (busy)
		expect(pfl.activeSource).toBe('cam1');
		expect(pfl.activeMode).toBe('pfl');
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
		expect(pfl.activeMode).toBe('pfl');
		expect(ctx.pflManager.enablePFL).toHaveBeenCalledTimes(2);
	});

	it('cycles through OFF -> PFL -> AFL -> OFF on same source', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);

		// OFF -> PFL
		const r1 = pfl.toggle('cam1');
		expect(r1).toEqual({ source: 'cam1', mode: 'pfl' });
		expect(pfl.activeSource).toBe('cam1');
		expect(pfl.activeMode).toBe('pfl');
		expect(ctx.pflManager.enablePFL).toHaveBeenCalledWith('cam1', 'pfl');

		// PFL -> AFL
		vi.advanceTimersByTime(100);
		const r2 = pfl.toggle('cam1');
		expect(r2).toEqual({ source: 'cam1', mode: 'afl' });
		expect(pfl.activeSource).toBe('cam1');
		expect(pfl.activeMode).toBe('afl');
		expect(ctx.pflManager.enablePFL).toHaveBeenCalledWith('cam1', 'afl');

		// AFL -> OFF
		vi.advanceTimersByTime(100);
		const r3 = pfl.toggle('cam1');
		expect(r3).toEqual({ source: null, mode: 'off' });
		expect(pfl.activeSource).toBeNull();
		expect(pfl.activeMode).toBe('off');
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
		// pflManager should enable cam2 in PFL mode
		expect(ctx.pflManager.enablePFL).toHaveBeenCalledWith('cam2', 'pfl');
	});

	it('switching source always starts in PFL mode', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		// Activate cam1 and cycle to AFL
		pfl.toggle('cam1');
		vi.advanceTimersByTime(100);
		pfl.toggle('cam1'); // now AFL
		expect(pfl.activeMode).toBe('afl');

		// Switch to cam2 -- should start in PFL, not AFL
		vi.advanceTimersByTime(100);
		pfl.toggle('cam2');
		expect(pfl.activeSource).toBe('cam2');
		expect(pfl.activeMode).toBe('pfl');
	});

	it('mutes source when transitioning from AFL to OFF', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1'); // PFL
		vi.advanceTimersByTime(100);
		pfl.toggle('cam1'); // AFL
		vi.advanceTimersByTime(100);
		pfl.toggle('cam1'); // OFF

		expect(ctx.pflManager.disablePFL).toHaveBeenCalledTimes(1);
		expect(ctx.pipeline.setSourceMuted).toHaveBeenCalledWith('cam1', true);
		expect(pfl.activeSource).toBeNull();
	});

	it('returns current state when busy (no change)', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		const result = pfl.toggle('cam2'); // busy, ignored
		expect(result).toEqual({ source: 'cam1', mode: 'pfl' });
	});

	it('returns result with source and mode from toggle', () => {
		const ctx = {
			pflManager: { enablePFL: vi.fn(), disablePFL: vi.fn() },
			pipeline: { setSourceMuted: vi.fn() },
		};
		const pfl = createPFLToggle(ctx);
		const result1 = pfl.toggle('cam1');
		expect(result1).toEqual({ source: 'cam1', mode: 'pfl' });

		vi.advanceTimersByTime(100);
		const result2 = pfl.toggle('cam1');
		expect(result2).toEqual({ source: 'cam1', mode: 'afl' });

		vi.advanceTimersByTime(100);
		const result3 = pfl.toggle('cam1');
		expect(result3).toEqual({ source: null, mode: 'off' });
	});
});
