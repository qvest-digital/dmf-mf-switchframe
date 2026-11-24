import { describe, it, expect, vi } from 'vitest';
import { orderByHealth, RelayTargetPoller, wireRelayTarget, type PeerHealth } from './relay-target';

const h = (label: string, extra: Partial<PeerHealth> = {}): PeerHealth =>
	({ status: 'ok', engineLabel: label, programSource: 'mxl:cam1', epoch: 0, ...extra });

describe('orderByHealth', () => {
	it('puts the leader by label first', () => {
		expect(orderByHealth([{ id: 'B', health: h('b') }, { id: 'A', health: h('a') }])).toEqual(['A', 'B']);
	});

	it('puts a forced leader first by its epoch', () => {
		expect(orderByHealth([{ id: 'A', health: h('a') }, { id: 'B', health: h('b', { epoch: 2 }) }])).toEqual(['B', 'A']);
	});

	it('puts an unreachable engine last', () => {
		expect(orderByHealth([{ id: 'A', health: null }, { id: 'B', health: h('b') }])).toEqual(['B', 'A']);
	});

	it('puts an engine still starting after one on air', () => {
		expect(orderByHealth([{ id: 'A', health: h('a', { programSource: '' }) }, { id: 'B', health: h('b') }])).toEqual(['B', 'A']);
	});
});

describe('RelayTargetPoller', () => {
	const engines = () => [{ id: 'A', url: 'https://a' }, { id: 'B', url: 'https://b' }];

	it('keeps the configured order until the first read', () => {
		expect(new RelayTargetPoller(engines, vi.fn()).order()).toEqual(['A', 'B']);
	});

	it('orders the engines by their peer health, an unreachable one last', async () => {
		const fetchFn = vi.fn(async (url: string) => {
			if (url.startsWith('https://a')) throw new TypeError('Failed to fetch');
			return new Response(JSON.stringify(h('b')), { status: 200 });
		}) as unknown as typeof fetch;
		const p = new RelayTargetPoller(engines, fetchFn);
		await p.poll();
		expect(p.order()).toEqual(['B', 'A']);
		expect(vi.mocked(fetchFn).mock.calls.map((c) => c[0])).toEqual(['https://a/api/peer/health', 'https://b/api/peer/health']);
	});

	it('counts an engine answering an error as unreachable', async () => {
		const fetchFn = vi.fn(async (url: string) =>
			url.startsWith('https://a')
				? new Response(JSON.stringify(h('a', { epoch: 5 })), { status: 503 })
				: new Response(JSON.stringify(h('b')), { status: 200 })) as unknown as typeof fetch;
		const p = new RelayTargetPoller(engines, fetchFn);
		await p.poll();
		expect(p.order()).toEqual(['B', 'A']);
	});

	it('names the engines to send to only while the first relays', async () => {
		let relays = false;
		const fetchFn = vi.fn(async (url: string) =>
			new Response(JSON.stringify(h(url.startsWith('https://a') ? 'a' : 'b', { relays })), { status: 200 })) as unknown as typeof fetch;
		const p = new RelayTargetPoller(engines, fetchFn);
		expect(p.singleSendOrder()).toBeNull();
		await p.poll();
		expect(p.singleSendOrder()).toBeNull();
		relays = true;
		await p.poll();
		expect(p.singleSendOrder()).toEqual(['A', 'B']);
	});

	it('wireRelayTarget starts polling and sets the provider, and stops both', async () => {
		const fetchFn = vi.fn(async () => new Response(JSON.stringify(h('a', { relays: true })), { status: 200 })) as unknown as typeof fetch;
		const p = new RelayTargetPoller(engines, fetchFn, 60_000);
		const setProvider = vi.fn();
		const stop = wireRelayTarget(p, setProvider);
		expect(fetchFn).toHaveBeenCalled();
		await vi.waitFor(() => expect(setProvider.mock.calls[0][0]()).toEqual(['A', 'B']));
		stop();
		expect(setProvider).toHaveBeenLastCalledWith(null);
	});
});
