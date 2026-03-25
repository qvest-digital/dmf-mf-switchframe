import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import PodBoard from './PodBoard.svelte';
import type { PodStatusBroadcast, PodTemplateDef } from '$lib/api/types';
import * as api from '$lib/api/switch-api';

vi.mock('$lib/api/switch-api', () => ({
	podCreate: vi.fn().mockResolvedValue({ id: 'new-pod' }),
	podArm: vi.fn().mockResolvedValue(undefined),
	podFire: vi.fn().mockResolvedValue(undefined),
	podDump: vi.fn().mockResolvedValue(undefined),
	podHold: vi.fn().mockResolvedValue(undefined),
	podExtend: vi.fn().mockResolvedValue(undefined),
}));

function makePod(overrides: Partial<PodStatusBroadcast> = {}): PodStatusBroadcast {
	return {
		id: 'pod-1',
		type: 'ad_break',
		state: 'scheduled',
		armed: false,
		durationMs: 60000,
		priority: 'normal',
		...overrides,
	};
}

describe('PodBoard', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('renders "POD BOARD" header', () => {
		const { container } = render(PodBoard, { props: { channel: 1 } });
		expect(container.textContent).toContain('POD BOARD');
	});

	it('shows armed badge count when podsArmed > 0', () => {
		const { container } = render(PodBoard, {
			props: { channel: 1, podsArmed: 3 },
		});
		const badge = container.querySelector('.armed-badge');
		expect(badge).not.toBeNull();
		expect(badge!.textContent).toContain('3 armed');
	});

	it('does NOT show armed badge when podsArmed === 0', () => {
		const { container } = render(PodBoard, {
			props: { channel: 1, podsArmed: 0 },
		});
		const badge = container.querySelector('.armed-badge');
		expect(badge).toBeNull();
	});

	it('shows active pod with countdown (remainingMs: 40000 → "0:40")', () => {
		const activePod = makePod({
			state: 'break_active',
			remainingMs: 40000,
			durationMs: 60000,
			elapsedMs: 20000,
		});
		const { container } = render(PodBoard, {
			props: { channel: 1, activePod },
		});
		expect(container.textContent).toContain('ACTIVE');
		const countdown = container.querySelector('.countdown');
		expect(countdown).not.toBeNull();
		expect(countdown!.textContent).toBe('0:40');
	});

	it('shows DUMP/HOLD/+30s buttons for active pod', () => {
		const activePod = makePod({
			state: 'break_active',
			remainingMs: 30000,
			elapsedMs: 30000,
		});
		const { container } = render(PodBoard, {
			props: { channel: 1, activePod },
		});
		const buttons = container.querySelectorAll('.active-actions .action-btn');
		const labels = Array.from(buttons).map((b) => b.textContent);
		expect(labels).toContain('DUMP');
		expect(labels).toContain('HOLD');
		expect(labels).toContain('+30s');
	});

	it('shows armed pods list (pod with armed: true)', () => {
		const pods = [
			makePod({ id: 'armed-1', type: 'timeout', armed: true, state: 'scheduled', durationMs: 120000 }),
			makePod({ id: 'not-armed', armed: false, state: 'scheduled' }),
		];
		const { container } = render(PodBoard, {
			props: { channel: 1, pods },
		});
		const armedSection = container.querySelector('.armed-section');
		expect(armedSection).not.toBeNull();
		const armedPods = armedSection!.querySelectorAll('.armed-pod');
		expect(armedPods).toHaveLength(1);
		expect(armedPods[0].textContent).toContain('timeout');
	});

	it('renders default generic buttons when no podTemplates provided', () => {
		const { container } = render(PodBoard, { props: { channel: 1 } });
		const tmplButtons = container.querySelectorAll('.template-buttons .action-btn');
		const labels = Array.from(tmplButtons).map((b) => b.textContent);
		expect(labels).toContain('+Short');
		expect(labels).toContain('+Standard');
		expect(labels).toContain('+Long');
		expect(labels).toHaveLength(3);
	});

	it('renders manifest-driven template buttons when podTemplates prop provided', () => {
		const podTemplates: PodTemplateDef[] = [
			{ label: 'Quarter', duration_ms: 120000, auto_return: true },
			{ label: 'Timeout', duration_ms: 60000, auto_return: true },
			{ label: 'Injury', duration_ms: 180000, auto_return: false, priority: 'high' },
		];
		const { container } = render(PodBoard, {
			props: { channel: 1, podTemplates },
		});
		const tmplButtons = container.querySelectorAll('.template-buttons .action-btn');
		const labels = Array.from(tmplButtons).map((b) => b.textContent);
		expect(labels).toEqual(['+Quarter', '+Timeout', '+Injury']);
	});

	it('calls podCreate with manifest template label when clicked', async () => {
		const podTemplates: PodTemplateDef[] = [
			{ label: 'Quarter', duration_ms: 120000, auto_return: true },
		];
		const { container } = render(PodBoard, {
			props: { channel: 1, podTemplates },
		});
		const tmplButtons = container.querySelectorAll('.template-buttons .action-btn');
		const quarterBtn = Array.from(tmplButtons).find((b) => b.textContent === '+Quarter')!;
		expect(quarterBtn).toBeDefined();
		await fireEvent.click(quarterBtn);
		expect(api.podCreate).toHaveBeenCalledWith(1, { template: 'Quarter' });
		// After create, it should auto-arm the new pod
		await vi.waitFor(() => {
			expect(api.podArm).toHaveBeenCalledWith(1, 'new-pod');
		});
	});

	it('calls podCreate with default template label when no manifest', async () => {
		const { container } = render(PodBoard, { props: { channel: 1 } });
		const tmplButtons = container.querySelectorAll('.template-buttons .action-btn');
		const shortBtn = Array.from(tmplButtons).find((b) => b.textContent === '+Short')!;
		expect(shortBtn).toBeDefined();
		await fireEvent.click(shortBtn);
		expect(api.podCreate).toHaveBeenCalledWith(1, { template: 'short' });
		await vi.waitFor(() => {
			expect(api.podArm).toHaveBeenCalledWith(1, 'new-pod');
		});
	});

	it('shows progress bar for active pod with correct width', () => {
		const activePod = makePod({
			state: 'break_active',
			durationMs: 100000,
			elapsedMs: 40000,
			remainingMs: 60000,
		});
		const { container } = render(PodBoard, {
			props: { channel: 1, activePod },
		});
		const fill = container.querySelector('.progress-fill') as HTMLElement;
		expect(fill).not.toBeNull();
		// 40000 / 100000 = 40%
		expect(fill.style.width).toBe('40%');
	});
});
