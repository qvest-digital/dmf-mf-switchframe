import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
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
	beginPath: vi.fn(),
	arc: vi.fn(),
	fill: vi.fn(),
	set font(_v: string) { /* noop */ },
	set fillStyle(_v: string) { /* noop */ },
	set textBaseline(_v: string) { /* noop */ },
	set textAlign(_v: string) { /* noop */ },
	set globalAlpha(_v: number) { /* noop */ },
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
HTMLCanvasElement.prototype.getContext = function(type: string, ...args: unknown[]) {
	if (type === '2d') {
		return mockCtx2d;
	}
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	return (origGetContext as any).call(this, type, ...args);
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
		cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' as const },
		cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' as const },
	},
	seq: 1,
	timestamp: Date.now(),
};

const oneLayer = {
	id: 0, active: false, template: 'lower-third', fadePosition: 0,
	zOrder: 0, x: 0, y: 0, width: 1920, height: 1080,
};

const oneLayerActive = {
	...oneLayer, active: true, fadePosition: 1.0,
};

describe('GraphicsPanel', () => {
	it('should render DSK LAYERS label and OFF status when no layers', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		expect(container.textContent).toContain('DSK LAYERS');
		expect(container.textContent).toContain('OFF');
	});

	it('should show empty state when no layers exist', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		expect(container.textContent).toContain('No layers');
		expect(container.querySelector('.layer-card')).toBeNull();
	});

	it('should show ON AIR when any layer is active', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayerActive] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		expect(container.textContent).toContain('ON AIR');
	});

	it('should render a layer card for each layer', () => {
		const state = {
			...baseState,
			graphics: {
				layers: [
					{ ...oneLayer, id: 0, zOrder: 0 },
					{ ...oneLayer, id: 1, zOrder: 1 },
				],
			},
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const cards = container.querySelectorAll('.layer-card');
		expect(cards.length).toBe(2);
	});

	it('should have template selector with 6 templates per layer', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const select = container.querySelector('.template-select') as HTMLSelectElement;
		expect(select).toBeTruthy();
		expect(select.options.length).toBe(6);
	});

	it('should show field inputs for default lower-third template', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		// Default template is lower-third which has Name and Title fields
		const inputs = container.querySelectorAll('.field-input');
		expect(inputs.length).toBe(2);
	});

	it('should have CUT ON/OFF, AUTO ON/OFF, FLY IN/OUT, and ANIMATE buttons per layer', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const buttons = container.querySelectorAll('.gfx-btn');
		expect(buttons.length).toBe(7);

		const labels = Array.from(buttons).map((b) => b.textContent?.trim());
		expect(labels).toContain('CUT ON');
		expect(labels).toContain('AUTO ON');
		expect(labels).toContain('CUT OFF');
		expect(labels).toContain('AUTO OFF');
		expect(labels).toContain('FLY IN');
		expect(labels).toContain('FLY OUT');
		expect(labels).toContain('ANIMATE');
	});

	it('should disable OFF buttons when layer not active', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const offBtn = container.querySelector('.gfx-btn.off') as HTMLButtonElement;
		const autoOffBtn = container.querySelector('.gfx-btn.auto-off') as HTMLButtonElement;
		expect(offBtn.disabled).toBe(true);
		expect(autoOffBtn.disabled).toBe(true);
	});

	it('should disable ON buttons when layer active', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayerActive] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const onBtn = container.querySelector('.gfx-btn.on') as HTMLButtonElement;
		const autoOnBtn = container.querySelector('.gfx-btn.auto-on') as HTMLButtonElement;
		expect(onBtn.disabled).toBe(true);
		expect(autoOnBtn.disabled).toBe(true);
	});

	it('should render a preview canvas per layer', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const canvas = container.querySelector('.gfx-preview') as HTMLCanvasElement;
		expect(canvas).toBeTruthy();
		expect(canvas.width).toBe(320);
		expect(canvas.height).toBe(240);
	});

	it('should show animation controls for all templates including lower-third', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const animRow = container.querySelector('.gfx-anim-row');
		expect(animRow).toBeTruthy();
		const animBtn = container.querySelector('.gfx-btn.anim-start');
		expect(animBtn).toBeTruthy();
	});

	it('should show ANIMATE button enabled when layer active', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayerActive] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const animBtn = container.querySelector('.gfx-btn.anim-start') as HTMLButtonElement;
		expect(animBtn).toBeTruthy();
		expect(animBtn.textContent?.trim()).toBe('ANIMATE');
		expect(animBtn.disabled).toBe(false);
	});

	it('should show STOP ANIM button when animation is active', () => {
		const state = {
			...baseState,
			graphics: {
				layers: [{
					...oneLayerActive,
					animationMode: 'pulse',
					animationHz: 1.0,
					fadePosition: 0.7,
				}],
			},
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const stopBtn = container.querySelector('.gfx-btn.anim-stop') as HTMLButtonElement;
		expect(stopBtn).toBeTruthy();
		expect(stopBtn.textContent?.trim()).toBe('STOP ANIM');
	});

	it('should disable ANIMATE button when layer not active', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const animBtn = container.querySelector('.gfx-btn.anim-start') as HTMLButtonElement;
		expect(animBtn).toBeTruthy();
		expect(animBtn.disabled).toBe(true);
	});

	it('should show layer ID and z-order', () => {
		const state = {
			...baseState,
			graphics: { layers: [{ ...oneLayer, id: 3, zOrder: 2 }] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		expect(container.textContent).toContain('L3');
		expect(container.textContent).toContain('z2');
	});

	it('should have z-order up/down buttons', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const zBtns = container.querySelectorAll('.z-btn');
		expect(zBtns.length).toBe(2);
	});

	it('should have delete button per layer', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const deleteBtn = container.querySelector('.delete-btn');
		expect(deleteBtn).toBeTruthy();
	});

	it('should have add layer button', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		const addBtn = container.querySelector('.add-layer-btn');
		expect(addBtn).toBeTruthy();
		expect(addBtn?.textContent?.trim()).toBe('+ LAYER');
	});

	it('should highlight active layer card', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayerActive] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const card = container.querySelector('.layer-card.active');
		expect(card).toBeTruthy();
	});

	it('should show fly-in/out controls with direction and duration', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const flyDirSelect = container.querySelector('.fly-direction-select') as HTMLSelectElement;
		expect(flyDirSelect).toBeTruthy();
		expect(flyDirSelect.options.length).toBe(4);

		const flyDuration = container.querySelector('.fly-duration') as HTMLInputElement;
		expect(flyDuration).toBeTruthy();

		const flyInBtn = container.querySelector('.gfx-btn.fly-in') as HTMLButtonElement;
		const flyOutBtn = container.querySelector('.gfx-btn.fly-out') as HTMLButtonElement;
		expect(flyInBtn).toBeTruthy();
		expect(flyOutBtn).toBeTruthy();
	});

	it('should disable FLY IN when active, FLY OUT when inactive', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const flyIn = container.querySelector('.gfx-btn.fly-in') as HTMLButtonElement;
		const flyOut = container.querySelector('.gfx-btn.fly-out') as HTMLButtonElement;
		expect(flyIn.disabled).toBe(false);
		expect(flyOut.disabled).toBe(true);
	});

	it('should show animation mode selector', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const modeSelect = container.querySelector('.anim-mode-select') as HTMLSelectElement;
		expect(modeSelect).toBeTruthy();
		expect(modeSelect.options.length).toBe(2);
		expect(modeSelect.value).toBe('pulse');
	});

	it('should show pulse params (min/max/Hz) for pulse mode', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const params = container.querySelectorAll('.anim-param');
		expect(params.length).toBe(3);
	});

	it('should show transition params (alpha/ms) when mode is transition', async () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const modeSelect = container.querySelector('.anim-mode-select') as HTMLSelectElement;
		await fireEvent.change(modeSelect, { target: { value: 'transition' } });
		const params = container.querySelectorAll('.anim-param');
		expect(params.length).toBe(2);
	});

	it('should show animation badge when animation active', () => {
		const state = {
			...baseState,
			graphics: {
				layers: [{
					...oneLayerActive,
					animationMode: 'pulse',
					animationHz: 1.0,
				}],
			},
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const badge = container.querySelector('.anim-badge');
		expect(badge).toBeTruthy();
		expect(badge?.textContent).toContain('pulse');
		expect(badge?.textContent).toContain('1Hz');
	});

	it('should show layer position in header', () => {
		const state = {
			...baseState,
			graphics: { layers: [{ ...oneLayer, x: 100, y: 50, width: 960, height: 540 }] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const rect = container.querySelector('.layer-rect');
		expect(rect).toBeTruthy();
		expect(rect?.textContent).toContain('960');
		expect(rect?.textContent).toContain('540');
	});

	it('should sort layers by z-order', () => {
		const state = {
			...baseState,
			graphics: {
				layers: [
					{ ...oneLayer, id: 1, zOrder: 2 },
					{ ...oneLayer, id: 0, zOrder: 0 },
					{ ...oneLayer, id: 2, zOrder: 1 },
				],
			},
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const ids = Array.from(container.querySelectorAll('.layer-id')).map(el => el.textContent);
		expect(ids).toEqual(['L0', 'L2', 'L1']);
	});
});
