import { describe, it, expect, vi, beforeAll } from 'vitest';
import { render } from '@testing-library/svelte';
import GraphicsPanel from './GraphicsPanel.svelte';

// Mock OffscreenCanvas since jsdom doesn't support it.
// Must be set up before module imports evaluate.
const mockCtx2d = {
	clearRect: vi.fn(),
	fillRect: vi.fn(),
	fillText: vi.fn(),
	getImageData: vi.fn(() => ({
		data: new Uint8ClampedArray(320 * 240 * 4),
	})),
	save: vi.fn(),
	restore: vi.fn(),
	set font(_v: string) { /* noop */ },
	set fillStyle(_v: string) { /* noop */ },
	set textBaseline(_v: string) { /* noop */ },
	set textAlign(_v: string) { /* noop */ },
};

class MockOffscreenCanvas {
	width: number;
	height: number;
	constructor(width: number, height: number) {
		this.width = width;
		this.height = height;
	}
	getContext() {
		return mockCtx2d;
	}
}

// @ts-expect-error - mock for jsdom
globalThis.OffscreenCanvas = MockOffscreenCanvas;

// Also mock HTMLCanvasElement.prototype.getContext for preview canvas
const origGetContext = HTMLCanvasElement.prototype.getContext;
// @ts-expect-error - mock
HTMLCanvasElement.prototype.getContext = function(type: string) {
	if (type === '2d') {
		return mockCtx2d;
	}
	return origGetContext.call(this, type);
};

const baseState = {
	programSource: 'cam1',
	previewSource: 'cam2',
	transitionType: 'cut',
	transitionDurationMs: 0,
	transitionPosition: 0,
	inTransition: false,
	ftbActive: false,
	audioChannels: undefined,
	masterLevel: 0,
	programPeak: [0, 0] as [number, number],
	tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
	sources: {
		cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' as const, lastFrameTime: 0 },
		cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' as const, lastFrameTime: 0 },
	},
	seq: 1,
	timestamp: Date.now(),
};

describe('GraphicsPanel', () => {
	it('should render DSK label and OFF status', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		expect(container.textContent).toContain('DSK');
		expect(container.textContent).toContain('OFF');
	});

	it('should show ON AIR when graphics active', () => {
		const state = {
			...baseState,
			graphics: { active: true, template: 'lower-third', fadePosition: 1.0 },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		expect(container.textContent).toContain('ON AIR');
	});

	it('should have template selector with 3 templates', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		const select = container.querySelector('.template-select') as HTMLSelectElement;
		expect(select).toBeTruthy();
		expect(select.options.length).toBe(3);
	});

	it('should show field inputs for selected template', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		// Default template is lower-third which has Name and Title fields
		const inputs = container.querySelectorAll('.field-input');
		expect(inputs.length).toBe(2);
	});

	it('should have CUT ON/OFF and AUTO ON/OFF buttons', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		const buttons = container.querySelectorAll('.gfx-btn');
		expect(buttons.length).toBe(4);

		const labels = Array.from(buttons).map((b) => b.textContent?.trim());
		expect(labels).toContain('CUT ON');
		expect(labels).toContain('AUTO ON');
		expect(labels).toContain('CUT OFF');
		expect(labels).toContain('AUTO OFF');
	});

	it('should disable OFF buttons when not active', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		const offBtn = container.querySelector('.gfx-btn.off') as HTMLButtonElement;
		const autoOffBtn = container.querySelector('.gfx-btn.auto-off') as HTMLButtonElement;
		expect(offBtn.disabled).toBe(true);
		expect(autoOffBtn.disabled).toBe(true);
	});

	it('should disable ON buttons when active', () => {
		const state = {
			...baseState,
			graphics: { active: true, template: 'lower-third', fadePosition: 1.0 },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const onBtn = container.querySelector('.gfx-btn.on') as HTMLButtonElement;
		const autoOnBtn = container.querySelector('.gfx-btn.auto-on') as HTMLButtonElement;
		expect(onBtn.disabled).toBe(true);
		expect(autoOnBtn.disabled).toBe(true);
	});

	it('should render a preview canvas', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		const canvas = container.querySelector('.gfx-preview') as HTMLCanvasElement;
		expect(canvas).toBeTruthy();
		expect(canvas.width).toBe(320);
		expect(canvas.height).toBe(240);
	});
});
