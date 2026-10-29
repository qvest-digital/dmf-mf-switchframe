import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import RedundancyPanel from './RedundancyPanel.svelte';
import PipelineRoutingOverlay from './on-air/PipelineRoutingOverlay.svelte';
import type { SyncHealth, EngineHealthDetail } from '$lib/state/reconciler.svelte';

// Each poll here reads two things. One that keeps failing must back off
// alone, not slow the other to the backoff ceiling.
describe('polls with two halves', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		URL.createObjectURL = vi.fn(() => 'blob:x');
		URL.revokeObjectURL = vi.fn();
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.unstubAllGlobals();
	});

	function count(fetchMock: ReturnType<typeof vi.fn>, part: string): number {
		return fetchMock.mock.calls.filter((c) => String(c[0]).includes(part)).length;
	}

	it('keeps engine A thumbnail at 1s while engine B has no confidence output', async () => {
		const fetchMock = vi.fn(async (url: string) => (url.startsWith('https://engine-b.example')
			? new Response('{"error":"output not configured"}', { status: 501 })
			: new Response('jpg', { status: 200 })));
		vi.stubGlobal('fetch', fetchMock);
		const health = { status: 'synced', divergences: [], authoritative: 'A', warnings: [], staleEngines: [],
			engineA: { lastSeen: 0, lastCommandSeq: 1, connected: true },
			engineB: { lastSeen: 0, lastCommandSeq: 1, connected: true } } as unknown as SyncHealth;
		const engine = { connected: true, rtt: 1, clockOffset: 0, lastSeq: 1, uptime: 1, lastSeen: 0, sources: {} } as EngineHealthDetail;
		render(RedundancyPanel, { props: {
			health, engineHealth: { A: engine, B: engine }, divergenceLog: [], visible: true, onclose: () => {},
			engineBUrl: 'https://engine-b.example',
		} });

		await vi.advanceTimersByTimeAsync(30_500);

		expect(count(fetchMock, 'https://engine-b.example')).toBeLessThanOrEqual(5);
		expect(fetchMock.mock.calls.length - count(fetchMock, 'https://engine-b.example')).toBeGreaterThanOrEqual(25);
	});

	it('keeps the routing snapshot at 1s while thumbnails fail', async () => {
		const fetchMock = vi.fn(async (url: string) => (url.includes('/thumbnails')
			? new Response('{"error":"boom"}', { status: 500 })
			: new Response('{"nodes":[],"connections":[]}', { status: 200 })));
		vi.stubGlobal('fetch', fetchMock);
		render(PipelineRoutingOverlay, { props: { visible: true, onclose: () => {} } });

		await vi.advanceTimersByTimeAsync(30_500);

		expect(count(fetchMock, '/api/pipeline/thumbnails')).toBeLessThanOrEqual(5);
		expect(count(fetchMock, '/api/pipeline/snapshot')).toBeGreaterThanOrEqual(25);
	});
});
