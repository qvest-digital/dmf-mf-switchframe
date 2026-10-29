import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { watchStaleBundle, type StaleBundleDeps } from './stale-bundle';

// A tab left open across engine upgrades kept an old build's request loop
// running for hours against a refused token.
describe('watchStaleBundle', () => {
	let newer: boolean;
	let hidden: boolean;
	let refused: boolean;
	let check: ReturnType<typeof vi.fn<() => Promise<boolean>>>;
	let deps: StaleBundleDeps & { reload: ReturnType<typeof vi.fn<() => void>>; prompt: ReturnType<typeof vi.fn<() => void>> };
	let stop: () => void;

	beforeEach(() => {
		vi.useFakeTimers();
		newer = false;
		hidden = false;
		refused = false;
		check = vi.fn(async () => newer);
		deps = {
			check,
			reload: vi.fn<() => void>(),
			prompt: vi.fn<() => void>(),
			isHidden: () => hidden,
			isTokenRefused: () => refused,
		};
	});

	afterEach(() => {
		stop?.();
		vi.useRealTimers();
	});

	const minutes = (n: number) => vi.advanceTimersByTimeAsync(n * 60_000);

	it('checks once a minute and does nothing while the build is current', async () => {
		stop = watchStaleBundle(deps);
		await minutes(10);
		expect(check).toHaveBeenCalledTimes(10);
		expect(deps.reload).not.toHaveBeenCalled();
		expect(deps.prompt).not.toHaveBeenCalled();
	});

	it('reloads a stale tab whose token the engines refuse', async () => {
		stop = watchStaleBundle(deps);
		newer = true;
		refused = true;
		await minutes(1);
		expect(deps.reload).toHaveBeenCalledTimes(1);
	});

	it('reloads a stale tab once it has been hidden for five minutes', async () => {
		stop = watchStaleBundle(deps);
		newer = true;
		hidden = true;
		await minutes(4);
		expect(deps.reload).not.toHaveBeenCalled();
		await minutes(1);
		expect(deps.reload).toHaveBeenCalledTimes(1);
		await minutes(5);
		expect(deps.reload).toHaveBeenCalledTimes(1);
		expect(check).toHaveBeenCalledTimes(1);
	});

	it('asks a tab in use once instead of reloading under the operator', async () => {
		stop = watchStaleBundle(deps);
		newer = true;
		await minutes(10);
		expect(deps.reload).not.toHaveBeenCalled();
		expect(deps.prompt).toHaveBeenCalledTimes(1);
		// The build is not fetched again once it is known to be stale.
		expect(check).toHaveBeenCalledTimes(1);
	});

	it('starts the hidden time over when the tab is shown', async () => {
		stop = watchStaleBundle(deps);
		newer = true;
		hidden = true;
		await minutes(4);
		hidden = false;
		await minutes(1);
		hidden = true;
		await minutes(4);
		expect(deps.reload).not.toHaveBeenCalled();
		await minutes(1);
		expect(deps.reload).toHaveBeenCalledTimes(1);
	});

	it('keeps checking when the engine cannot be reached', async () => {
		check.mockRejectedValueOnce(new Error('offline'));
		stop = watchStaleBundle(deps);
		newer = true;
		refused = true;
		await minutes(1);
		expect(deps.reload).not.toHaveBeenCalled();
		await minutes(1);
		expect(deps.reload).toHaveBeenCalledTimes(1);
	});

	it('does nothing after stop', async () => {
		stop = watchStaleBundle(deps);
		stop();
		newer = true;
		refused = true;
		await minutes(10);
		expect(check).not.toHaveBeenCalled();
		expect(deps.reload).not.toHaveBeenCalled();
	});
});
