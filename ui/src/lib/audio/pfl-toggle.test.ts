import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createPFLToggle, type PFLPipeline } from './pfl-toggle';

/** A fake pipeline: what each channel would play at, 0 when silent. */
function fakePipeline() {
	const muted = new Map<string, boolean>();
	const gain = new Map<string, number>();
	const p: PFLPipeline & { audible(k: string): number } = {
		setSourceMuted: vi.fn((k: string, m: boolean) => { muted.set(k, m); }),
		setSourceGain: vi.fn((k: string, g: number) => { gain.set(k, g); }),
		audible: (k: string) => (muted.get(k) === false ? (gain.get(k) ?? 1) : 0),
	};
	return p;
}

/**
 * Engine A's and engine B's pipelines, `active` the one that plays, and a
 * solo flag standing for the program bus: what the monitor carries.
 */
function makeCtx(faders: Record<string, number> = {}) {
	const a = fakePipeline();
	const b = fakePipeline();
	let active = a;
	let solo = false;
	const ctx = {
		pipeline: () => active,
		aflGain: (k: string) => faders[k] ?? 1,
		setSolo: vi.fn((s: boolean) => { solo = s; }),
	};
	return {
		ctx, a, b,
		activate: (p: typeof a) => { active = p; },
		audible: (k: string) => active.audible(k),
		programHeard: () => !solo,
	};
}

describe('PFL/AFL toggle', () => {
	beforeEach(() => { vi.useFakeTimers(); });
	afterEach(() => { vi.useRealTimers(); });

	it('PFL plays the channel at unity in place of program', () => {
		const { ctx, audible, programHeard } = makeCtx({ cam1: 0.25 });
		const pfl = createPFLToggle(ctx);
		expect(programHeard()).toBe(true);

		expect(pfl.toggle('cam1')).toEqual({ source: 'cam1', mode: 'pfl' });
		expect(audible('cam1')).toBe(1);
		expect(programHeard()).toBe(false);
	});

	it('AFL plays the channel at its fader level, then OFF restores program', () => {
		const { ctx, audible, programHeard } = makeCtx({ cam1: 0.25 });
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		vi.advanceTimersByTime(100);

		expect(pfl.toggle('cam1')).toEqual({ source: 'cam1', mode: 'afl' });
		expect(audible('cam1')).toBe(0.25);
		expect(programHeard()).toBe(false);

		vi.advanceTimersByTime(100);
		expect(pfl.toggle('cam1')).toEqual({ source: null, mode: 'off' });
		expect(audible('cam1')).toBe(0);
		expect(programHeard()).toBe(true);
	});

	it('AFL follows the fader', () => {
		const faders = { cam1: 0.5 };
		const { ctx, audible } = makeCtx(faders);
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		vi.advanceTimersByTime(100);
		pfl.toggle('cam1');

		faders.cam1 = 0; // fader down, or the channel muted
		pfl.refreshAFL();
		expect(audible('cam1')).toBe(0);
		pfl.setAFLGain('cam1', 0.3);
		expect(audible('cam1')).toBe(0.3);
	});

	it('a fader change leaves a PFL channel at unity', () => {
		const faders = { cam1: 0.5 };
		const { ctx, audible } = makeCtx(faders);
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		pfl.refreshAFL();
		pfl.setAFLGain('cam1', 0.2);
		expect(audible('cam1')).toBe(1);
	});

	it('moving the solo to another channel silences the first and starts in PFL', () => {
		const { ctx, audible, programHeard } = makeCtx({ cam1: 0.25, cam2: 0.5 });
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		vi.advanceTimersByTime(100);
		pfl.toggle('cam1'); // AFL on cam1
		vi.advanceTimersByTime(100);

		expect(pfl.toggle('cam2')).toEqual({ source: 'cam2', mode: 'pfl' });
		expect(audible('cam1')).toBe(0);
		expect(audible('cam2')).toBe(1);
		expect(programHeard()).toBe(false);

		// cam1 comes back at unity the next time it is soloed in PFL.
		vi.advanceTimersByTime(100);
		pfl.toggle('cam1');
		expect(audible('cam1')).toBe(1);
	});

	// After a failover only engine B's pipeline plays: a solo left on engine
	// A's would be silence, with program off the monitor too.
	it('follows the solo to engine B after a failover, and back', () => {
		const { ctx, a, b, activate, programHeard } = makeCtx({ cam1: 0.25 });
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		vi.advanceTimersByTime(100);
		pfl.toggle('cam1'); // AFL at 0.25

		activate(b);
		pfl.follow();
		expect(b.audible('cam1')).toBe(0.25);
		expect(a.audible('cam1')).toBe(0);
		expect(programHeard()).toBe(false);

		activate(a);
		pfl.follow();
		expect(a.audible('cam1')).toBe(0.25);
		expect(b.audible('cam1')).toBe(0);
	});

	it('a solo started while engine B is active plays on engine B', () => {
		const { ctx, a, b, activate } = makeCtx();
		activate(b);
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		expect(b.audible('cam1')).toBe(1);
		expect(a.setSourceMuted).not.toHaveBeenCalled();
	});

	it('clear ends the solo and brings program back', () => {
		const { ctx, audible, programHeard } = makeCtx();
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		expect(pfl.clear()).toEqual({ source: null, mode: 'off' });
		expect(audible('cam1')).toBe(0);
		expect(programHeard()).toBe(true);
		expect(pfl.activeSource).toBeNull();
	});

	it('ignores rapid toggles within 100ms', () => {
		const { ctx } = makeCtx();
		const pfl = createPFLToggle(ctx);
		pfl.toggle('cam1');
		expect(pfl.toggle('cam2')).toEqual({ source: 'cam1', mode: 'pfl' });
		expect(pfl.activeSource).toBe('cam1');

		vi.advanceTimersByTime(100);
		pfl.toggle('cam2');
		expect(pfl.activeSource).toBe('cam2');
	});
});
