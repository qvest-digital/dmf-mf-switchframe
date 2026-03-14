import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import GraphicsPanel from './GraphicsPanel.svelte';

// Mock OffscreenCanvas since jsdom doesn't support it.
const mockCtx2d = {
	clearRect: vi.fn(),
	fillRect: vi.fn(),
	fillText: vi.fn(),
	getImageData: vi.fn(() => ({
		data: new Uint8ClampedArray(384 * 216 * 4),
	})),
	save: vi.fn(),
	restore: vi.fn(),
	beginPath: vi.fn(),
	arc: vi.fn(),
	fill: vi.fn(),
	measureText: vi.fn(() => ({ width: 50 })),
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
	it('should render DSK GRAPHICS label and OFF status when no layers', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		expect(container.textContent).toContain('DSK GRAPHICS');
		expect(container.textContent).toContain('OFF');
	});

	it('should show empty state when no layers exist', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		expect(container.textContent).toContain('Add a layer to get started');
	});

	it('should show ON AIR when any layer is active', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayerActive] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		expect(container.textContent).toContain('ON AIR');
	});

	it('should render a rail item for each layer', () => {
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
		const items = container.querySelectorAll('.rail-item');
		expect(items.length).toBe(2);
	});

	it('should have template cards (6 templates) in detail pane', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const cards = container.querySelectorAll('.tpl-card');
		expect(cards.length).toBe(6);
	});

	it('should show field inputs for default lower-third template', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const inputs = container.querySelectorAll('.field-inp');
		expect(inputs.length).toBe(2);
	});

	it('should have CUT ON/OFF, AUTO ON/OFF action buttons', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const buttons = container.querySelectorAll('.act-btn');
		const labels = Array.from(buttons).map((b) => b.textContent?.trim());
		expect(labels).toContain('CUT ON');
		expect(labels).toContain('AUTO ON');
		expect(labels).toContain('CUT OFF');
		expect(labels).toContain('AUTO OFF');
	});

	it('should disable OFF buttons when layer not active', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const offBtn = container.querySelector('.act-btn.off') as HTMLButtonElement;
		const autoOffBtn = container.querySelector('.act-btn.auto-off') as HTMLButtonElement;
		expect(offBtn.disabled).toBe(true);
		expect(autoOffBtn.disabled).toBe(true);
	});

	it('should disable ON buttons when layer active', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayerActive] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const onBtn = container.querySelector('.act-btn.on') as HTMLButtonElement;
		const autoOnBtn = container.querySelector('.act-btn.auto-on') as HTMLButtonElement;
		expect(onBtn.disabled).toBe(true);
		expect(autoOnBtn.disabled).toBe(true);
	});

	it('should render a preview canvas', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const canvas = container.querySelector('.detail-preview') as HTMLCanvasElement;
		expect(canvas).toBeTruthy();
		expect(canvas.width).toBe(384);
		expect(canvas.height).toBe(216);
	});

	it('should show layer ID and z-order in rail', () => {
		const state = {
			...baseState,
			graphics: { layers: [{ ...oneLayer, id: 3, zOrder: 2 }] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		expect(container.textContent).toContain('L3');
		expect(container.textContent).toContain('z2');
	});

	it('should have z-order up/down buttons in rail', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const microBtns = container.querySelectorAll('.micro-btn');
		// 2 z-order buttons + 1 delete button per layer
		expect(microBtns.length).toBe(3);
	});

	it('should have delete button in rail', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const delBtn = container.querySelector('.micro-btn.del');
		expect(delBtn).toBeTruthy();
	});

	it('should have add layer button', () => {
		const { container } = render(GraphicsPanel, { props: { state: baseState } });
		const addBtn = container.querySelector('.add-btn');
		expect(addBtn).toBeTruthy();
		expect(addBtn?.textContent?.trim()).toBe('+ ADD');
	});

	it('should highlight active layer in rail', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayerActive] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const item = container.querySelector('.rail-item.active');
		expect(item).toBeTruthy();
	});

	it('should auto-select first layer', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const selected = container.querySelector('.rail-item.selected');
		expect(selected).toBeTruthy();
	});

	it('should show fly controls after clicking disclosure', async () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		// Fly controls are behind disclosure toggle
		const disclosures = container.querySelectorAll('.disclosure');
		const flyDisclosure = Array.from(disclosures).find(d => d.textContent?.includes('FLY'));
		expect(flyDisclosure).toBeTruthy();
		await fireEvent.click(flyDisclosure!);
		// After clicking, fly buttons should appear
		const flyBtns = container.querySelectorAll('.act-btn.fly');
		expect(flyBtns.length).toBe(2);
	});

	it('should show animation controls after clicking disclosure', async () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayerActive] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const disclosures = container.querySelectorAll('.disclosure');
		const animDisclosure = Array.from(disclosures).find(d => d.textContent?.includes('ANIM'));
		expect(animDisclosure).toBeTruthy();
		await fireEvent.click(animDisclosure!);
		const animBtn = container.querySelector('.act-btn.anim-start') as HTMLButtonElement;
		expect(animBtn).toBeTruthy();
		expect(animBtn.textContent?.trim()).toBe('ANIMATE');
		expect(animBtn.disabled).toBe(false);
	});

	it('should show STOP button when animation is active', async () => {
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
		// Open anim disclosure
		const disclosures = container.querySelectorAll('.disclosure');
		const animDisclosure = Array.from(disclosures).find(d => d.textContent?.includes('ANIM'));
		await fireEvent.click(animDisclosure!);
		const stopBtn = container.querySelector('.act-btn.anim-stop') as HTMLButtonElement;
		expect(stopBtn).toBeTruthy();
		expect(stopBtn.textContent?.trim()).toBe('STOP');
	});

	it('should sort layers by z-order in rail', () => {
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
		const nums = Array.from(container.querySelectorAll('.layer-num')).map(el => el.textContent);
		expect(nums).toEqual(['L0', 'L2', 'L1']);
	});

	it('should show selected template card highlighted', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const selected = container.querySelector('.tpl-card.selected');
		expect(selected).toBeTruthy();
	});

	it('should show IMAGE disclosure toggle', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const disclosures = container.querySelectorAll('.disclosure');
		const imageDisc = Array.from(disclosures).find(d => d.textContent?.includes('IMAGE'));
		expect(imageDisc).toBeTruthy();
	});

	it('should show image upload button after clicking IMAGE disclosure', async () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const disclosures = container.querySelectorAll('.disclosure');
		const imageDisc = Array.from(disclosures).find(d => d.textContent?.includes('IMAGE'));
		await fireEvent.click(imageDisc!);
		const uploadBtn = Array.from(container.querySelectorAll('.act-btn')).find(b => b.textContent?.includes('UPLOAD'));
		expect(uploadBtn).toBeTruthy();
	});

	it('should show TICKER disclosure toggle', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const disclosures = container.querySelectorAll('.disclosure');
		const tickerDisc = Array.from(disclosures).find(d => d.textContent?.includes('TICKER'));
		expect(tickerDisc).toBeTruthy();
	});

	it('should show ticker controls after clicking TICKER disclosure', async () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const disclosures = container.querySelectorAll('.disclosure');
		const tickerDisc = Array.from(disclosures).find(d => d.textContent?.includes('TICKER'));
		await fireEvent.click(tickerDisc!);
		const startBtn = Array.from(container.querySelectorAll('.act-btn')).find(b => b.textContent?.trim() === 'START');
		expect(startBtn).toBeTruthy();
	});

	it('should show TEXT FX disclosure toggle', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const disclosures = container.querySelectorAll('.disclosure');
		const textFxDisc = Array.from(disclosures).find(d => d.textContent?.includes('TEXT FX'));
		expect(textFxDisc).toBeTruthy();
	});

	it('should show text animation controls after clicking TEXT FX disclosure', async () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const disclosures = container.querySelectorAll('.disclosure');
		const textFxDisc = Array.from(disclosures).find(d => d.textContent?.includes('TEXT FX'));
		await fireEvent.click(textFxDisc!);
		const modeSelect = container.querySelectorAll('.ctl-select');
		const hasTypewriter = Array.from(modeSelect).some(sel =>
			sel.textContent?.includes('Typewriter')
		);
		expect(hasTypewriter).toBe(true);
	});

	it('should have 5 disclosure toggles (FLY, ANIM, IMAGE, TICKER, TEXT FX)', () => {
		const state = {
			...baseState,
			graphics: { layers: [oneLayer] },
		};
		const { container } = render(GraphicsPanel, { props: { state } });
		const disclosures = container.querySelectorAll('.disclosure');
		expect(disclosures.length).toBe(5);
	});

	it('should show animation badge in disclosure when animating', () => {
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
		const badge = container.querySelector('.disclosure-badge');
		expect(badge).toBeTruthy();
		expect(badge?.textContent).toContain('pulse');
	});
});
