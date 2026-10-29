import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { tabStorage, watchStaleBundle, type StaleBundleDeps } from './stale-bundle';
import { holdReload } from './activity';

// A tab left open across engine upgrades kept an old build's request loop
// running for hours against a refused token.
describe('watchStaleBundle', () => {
	let served: string;
	let hidden: boolean;
	let hiddenFor: number;
	let refused: boolean;
	let idle: number;
	let busy: boolean;
	let store: Map<string, string>;
	let fetchVersion: ReturnType<typeof vi.fn<() => Promise<string>>>;
	let reload: ReturnType<typeof vi.fn<() => void>>;
	let prompt: ReturnType<typeof vi.fn<() => void>>;
	let deps: StaleBundleDeps;
	let stop: () => void;

	const MIN = 60_000;
	// Time passes a minute at a step; hidden time grows while hidden and
	// starts over when shown, as the visibilitychange tracking does.
	const minutes = async (n: number) => {
		for (let i = 0; i < n; i++) {
			idle += MIN;
			hiddenFor = hidden ? hiddenFor + MIN : 0;
			await vi.advanceTimersByTimeAsync(MIN);
		}
	};

	beforeEach(() => {
		vi.useFakeTimers();
		served = 'old';
		hidden = false;
		hiddenFor = 0;
		refused = false;
		idle = 0;
		busy = false;
		store = new Map();
		fetchVersion = vi.fn(async () => served);
		reload = vi.fn<() => void>();
		prompt = vi.fn<() => void>();
		deps = {
			current: 'old',
			fetchVersion,
			reload,
			prompt,
			storage: { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => void store.set(k, v) },
			isTokenRefused: () => refused,
			idleForMs: () => idle,
			hiddenForMs: () => hiddenFor,
			reloadWouldLoseWork: () => busy,
		};
	});

	afterEach(() => {
		stop?.();
		vi.useRealTimers();
	});

	it('checks once a minute and does nothing while the build is current', async () => {
		stop = watchStaleBundle(deps);
		hidden = true;
		refused = true;
		await minutes(10);
		expect(fetchVersion).toHaveBeenCalledTimes(10);
		expect(reload).not.toHaveBeenCalled();
		expect(prompt).not.toHaveBeenCalled();
	});

	it('reloads a stale tab hidden and untouched for five minutes', async () => {
		stop = watchStaleBundle(deps);
		served = 'new';
		hidden = true;
		await minutes(4);
		expect(reload).not.toHaveBeenCalled();
		await minutes(1);
		expect(reload).toHaveBeenCalledTimes(1);
		await minutes(5);
		expect(reload).toHaveBeenCalledTimes(1);
	});

	it('reloads an untouched stale tab whose token the engines refuse', async () => {
		stop = watchStaleBundle(deps);
		served = 'new';
		refused = true;
		await minutes(5);
		expect(reload).toHaveBeenCalledTimes(1);
	});

	it('never reloads a tab used in the last five minutes, and asks once instead', async () => {
		stop = watchStaleBundle(deps);
		served = 'new';
		hidden = true;
		refused = true;
		for (let i = 0; i < 30; i++) {
			await minutes(1);
			idle = 2 * MIN; // the operator keeps touching it
		}
		expect(reload).not.toHaveBeenCalled();
		expect(prompt).toHaveBeenCalledTimes(1);
	});

	it('never reloads while a reload would lose work', async () => {
		stop = watchStaleBundle(deps);
		served = 'new';
		hidden = true;
		busy = true;
		await minutes(30);
		expect(reload).not.toHaveBeenCalled();
		busy = false;
		await minutes(1);
		expect(reload).toHaveBeenCalledTimes(1);
	});

	// Hidden and untouched is not unused: an unsaved playlist, comms or an
	// audio monitor in that tab hold the reload through the real tracker.
	for (const reason of ['unsaved playlist', 'comms', 'audio monitor']) {
		it(`never reloads a hidden, idle tab while held by ${reason}`, async () => {
			delete deps.reloadWouldLoseWork;
			const release = holdReload(reason);
			stop = watchStaleBundle(deps);
			served = 'new';
			hidden = true;
			idle = 10 * MIN;
			await minutes(30);
			expect(reload).not.toHaveBeenCalled();
			expect(prompt).toHaveBeenCalledTimes(1);
			release();
			await minutes(1);
			expect(reload).toHaveBeenCalledTimes(1);
		});
	}

	it('starts the hidden time over when the tab is shown', async () => {
		stop = watchStaleBundle(deps);
		served = 'new';
		idle = 10 * MIN;
		hidden = true;
		await minutes(4);
		hidden = false;
		await minutes(1);
		hidden = true;
		await minutes(4);
		expect(reload).not.toHaveBeenCalled();
		await minutes(1);
		expect(reload).toHaveBeenCalledTimes(1);
	});

	// During a rollout, or behind a cache, a reload can come back on the old
	// build while the engine reports the new one; reloading again would loop.
	it('reloads by itself at most once per build across reloads', async () => {
		served = 'new';
		hidden = true;
		idle = 10 * MIN;
		stop = watchStaleBundle(deps);
		await minutes(5);
		expect(reload).toHaveBeenCalledTimes(1);

		// The reloaded page is still the old build: same tab, same storage.
		prompt.mockClear();
		stop = watchStaleBundle(deps);
		await minutes(30);
		expect(reload).toHaveBeenCalledTimes(1);
		expect(prompt).toHaveBeenCalledTimes(1);

		// A later build is a new reason to reload once more.
		served = 'newer';
		await minutes(1);
		expect(reload).toHaveBeenCalledTimes(2);
	});

	it('keeps checking when the engine cannot be reached', async () => {
		fetchVersion.mockRejectedValueOnce(new Error('offline'));
		stop = watchStaleBundle(deps);
		served = 'new';
		refused = true;
		idle = 10 * MIN;
		await minutes(1);
		expect(reload).not.toHaveBeenCalled();
		await minutes(1);
		expect(reload).toHaveBeenCalledTimes(1);
	});

	it('does nothing after stop', async () => {
		stop = watchStaleBundle(deps);
		stop();
		served = 'new';
		refused = true;
		await minutes(10);
		expect(fetchVersion).not.toHaveBeenCalled();
		expect(reload).not.toHaveBeenCalled();
	});

	// Blocked site storage made reading sessionStorage throw, and the root
	// layout render nothing.
	it('falls back to memory where site storage is blocked', () => {
		const original = Object.getOwnPropertyDescriptor(window, 'sessionStorage');
		Object.defineProperty(window, 'sessionStorage', { configurable: true, get: () => { throw new DOMException('blocked', 'SecurityError'); } });
		try {
			const s = tabStorage();
			expect(s.getItem('k')).toBeNull();
			s.setItem('k', 'v');
			expect(s.getItem('k')).toBe('v');
		} finally {
			if (original) Object.defineProperty(window, 'sessionStorage', original);
			else delete (window as unknown as Record<string, unknown>).sessionStorage;
		}
	});
});
