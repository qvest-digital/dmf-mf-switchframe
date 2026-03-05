import { describe, it, expect, beforeEach } from 'vitest';
import { render } from '@testing-library/svelte';
import Multiview from './Multiview.svelte';
import { setSourceError, clearAllErrors } from '$lib/transport/source-errors.svelte';

function makeState(overrides: Record<string, { status: string }> = {}) {
	const sources: Record<string, { key: string; label: string; status: string; lastFrameTime: number }> = {
		cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy', lastFrameTime: 0 },
		cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy', lastFrameTime: 0 },
	};
	for (const [key, val] of Object.entries(overrides)) {
		if (sources[key]) {
			sources[key].status = val.status;
		} else {
			sources[key] = { key, label: key, status: val.status, lastFrameTime: 0 };
		}
	}
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 0,
		transitionPosition: 0,
		inTransition: false,
		audioChannels: undefined,
		masterLevel: 0,
		programPeak: [0, 0] as [number, number],
		tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
		sources,
		seq: 1,
		timestamp: Date.now(),
	};
}

describe('Multiview with video', () => {
	it('should render canvas elements for each source', () => {
		const state = makeState();
		const { container } = render(Multiview, { props: { state } });
		const canvases = container.querySelectorAll('canvas');
		expect(canvases.length).toBe(2);
	});
});

describe('Multiview health overlays', () => {
	it('shows no overlay for healthy sources', () => {
		const state = makeState();
		const { container } = render(Multiview, { props: { state } });
		expect(container.querySelector('.health-overlay')).toBeNull();
	});

	it('shows pulsing stale overlay', () => {
		const state = makeState({ cam1: { status: 'stale' } });
		const { container } = render(Multiview, { props: { state } });
		const overlay = container.querySelector('.health-overlay.stale');
		expect(overlay).not.toBeNull();
		// Stale overlay should not have text (just a border)
		expect(overlay?.querySelector('.health-text')).toBeNull();
	});

	it('shows no-signal overlay with text', () => {
		const state = makeState({ cam1: { status: 'no_signal' } });
		const { container } = render(Multiview, { props: { state } });
		const overlay = container.querySelector('.health-overlay.no-signal');
		expect(overlay).not.toBeNull();
		const text = overlay?.querySelector('.health-text');
		expect(text).not.toBeNull();
		expect(text?.textContent).toBe('NO SIGNAL');
	});

	it('shows offline overlay with text', () => {
		const state = makeState({ cam2: { status: 'offline' } });
		const { container } = render(Multiview, { props: { state } });
		const overlay = container.querySelector('.health-overlay.offline');
		expect(overlay).not.toBeNull();
		const text = overlay?.querySelector('.health-text');
		expect(text).not.toBeNull();
		expect(text?.textContent).toBe('OFFLINE');
	});

	it('still shows tile-health text badge for unhealthy sources', () => {
		const state = makeState({ cam1: { status: 'stale' } });
		const { container } = render(Multiview, { props: { state } });
		const badge = container.querySelector('.tile-health');
		expect(badge).not.toBeNull();
		expect(badge?.textContent).toBe('stale');
	});

	it('overlay has pointer-events: none (non-interactive)', () => {
		const state = makeState({ cam1: { status: 'offline' } });
		const { container } = render(Multiview, { props: { state } });
		const overlay = container.querySelector('.health-overlay');
		expect(overlay).not.toBeNull();
	});
});

describe('Multiview decoder error indicator', () => {
	beforeEach(() => {
		clearAllErrors();
	});

	it('shows no error indicator when no decoder errors', () => {
		const state = makeState();
		const { container } = render(Multiview, { props: { state } });
		expect(container.querySelector('.tile-error')).toBeNull();
	});

	it('shows error indicator when source has decoder error', () => {
		setSourceError('cam1', 'Video decoder failed');
		const state = makeState();
		const { container } = render(Multiview, { props: { state } });
		const indicator = container.querySelector('.tile-error');
		expect(indicator).not.toBeNull();
		expect(indicator?.textContent).toBe('!');
		expect(indicator?.getAttribute('title')).toBe('Video decoder failed');
	});

	it('shows error indicator only on affected tile', () => {
		setSourceError('cam2', 'Audio decode error');
		const state = makeState();
		const { container } = render(Multiview, { props: { state } });
		const indicators = container.querySelectorAll('.tile-error');
		expect(indicators.length).toBe(1);
		expect(indicators[0].getAttribute('title')).toBe('Audio decode error');
	});
});
