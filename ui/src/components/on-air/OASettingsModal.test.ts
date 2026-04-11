import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OASettingsModal from './OASettingsModal.svelte';

function defaultProps(overrides: Record<string, unknown> = {}) {
	return {
		visible: true,
		onclose: vi.fn(),
		crState: {
			encoder: { current: 'x264', available: [{ name: 'x264', displayName: 'x264 (Software)', isDefault: true }] },
			pipelineFormat: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
		} as any,
		...overrides,
	};
}

describe('OASettingsModal', () => {
	it('not visible when visible=false', () => {
		const { container } = render(OASettingsModal, {
			props: defaultProps({ visible: false }),
		});
		expect(container.querySelector('.settings-backdrop')).toBeNull();
	});

	it('renders when visible=true', () => {
		const { container } = render(OASettingsModal, {
			props: defaultProps({ visible: true }),
		});
		expect(container.querySelector('.settings-backdrop')).toBeTruthy();
		expect(container.querySelector('.settings-modal')).toBeTruthy();
	});

	it('shows all tab labels', () => {
		render(OASettingsModal, { props: defaultProps() });
		expect(screen.getByRole('tab', { name: 'Presets' })).toBeTruthy();
		expect(screen.getByRole('tab', { name: 'DVE' })).toBeTruthy();
		expect(screen.getByRole('tab', { name: 'Keys' })).toBeTruthy();
		expect(screen.getByRole('tab', { name: 'Video' })).toBeTruthy();
		expect(screen.getByRole('tab', { name: 'Encoder' })).toBeTruthy();
		expect(screen.getByRole('tab', { name: 'Format' })).toBeTruthy();
		expect(screen.getByRole('tab', { name: 'Team' })).toBeTruthy();
	});

	it('default tab is presets', () => {
		render(OASettingsModal, { props: defaultProps() });
		const presetsTab = screen.getByRole('tab', { name: 'Presets' });
		expect(presetsTab.getAttribute('aria-selected')).toBe('true');
	});

	it('DVE tab shows redirect message', async () => {
		render(OASettingsModal, { props: defaultProps() });
		await fireEvent.click(screen.getByRole('tab', { name: 'DVE' }));
		expect(screen.getByText('DVE controls are available in the GFX panel.')).toBeTruthy();
		expect(screen.getByText('Open GFX Panel')).toBeTruthy();
	});

	it('Keys tab shows redirect message', async () => {
		render(OASettingsModal, { props: defaultProps() });
		await fireEvent.click(screen.getByRole('tab', { name: 'Keys' }));
		expect(screen.getByText('Key controls are available in the GFX panel.')).toBeTruthy();
		expect(screen.getByText('Open GFX Panel')).toBeTruthy();
	});

	it('Encoder tab shows current encoder', async () => {
		render(OASettingsModal, { props: defaultProps() });
		await fireEvent.click(screen.getByRole('tab', { name: 'Encoder' }));
		expect(screen.getByText('x264')).toBeTruthy();
	});

	it('Format tab shows pipeline format', async () => {
		render(OASettingsModal, { props: defaultProps() });
		await fireEvent.click(screen.getByRole('tab', { name: 'Format' }));
		expect(screen.getByText('1080p30')).toBeTruthy();
	});

	it('close button calls onclose', async () => {
		const closeFn = vi.fn();
		render(OASettingsModal, {
			props: defaultProps({ onclose: closeFn }),
		});
		const closeBtn = screen.getByTitle('Close settings');
		await fireEvent.click(closeBtn);
		expect(closeFn).toHaveBeenCalledOnce();
	});

	it('backdrop click calls onclose', async () => {
		const closeFn = vi.fn();
		const { container } = render(OASettingsModal, {
			props: defaultProps({ onclose: closeFn }),
		});
		const backdrop = container.querySelector('.settings-backdrop')!;
		await fireEvent.click(backdrop);
		expect(closeFn).toHaveBeenCalledOnce();
	});

	it('clicking modal body does not close', async () => {
		const closeFn = vi.fn();
		const { container } = render(OASettingsModal, {
			props: defaultProps({ onclose: closeFn }),
		});
		const modal = container.querySelector('.settings-modal')!;
		await fireEvent.click(modal);
		expect(closeFn).not.toHaveBeenCalled();
	});

	it('escape key is handled by parent, not OASettingsModal', async () => {
		const closeFn = vi.fn();
		render(OASettingsModal, {
			props: defaultProps({ onclose: closeFn }),
		});
		await fireEvent.keyDown(window, { code: 'Escape' });
		// Escape is handled by OnAirController parent, not this component
		expect(closeFn).not.toHaveBeenCalled();
	});

	it('shows SETTINGS title', () => {
		render(OASettingsModal, { props: defaultProps() });
		expect(screen.getByText('SETTINGS')).toBeTruthy();
	});

	it('each tab renders without error', async () => {
		render(OASettingsModal, { props: defaultProps() });
		const tabNames = ['Presets', 'DVE', 'Keys', 'Video', 'Encoder', 'Format', 'Team'];

		for (const tabName of tabNames) {
			await fireEvent.click(screen.getByRole('tab', { name: tabName }));
			expect(screen.getByRole('tab', { name: tabName }).getAttribute('aria-selected')).toBe('true');
		}
	});

	it('non-active tabs have aria-selected false', async () => {
		render(OASettingsModal, { props: defaultProps() });
		await fireEvent.click(screen.getByRole('tab', { name: 'Encoder' }));
		expect(screen.getByRole('tab', { name: 'Encoder' }).getAttribute('aria-selected')).toBe('true');
		expect(screen.getByRole('tab', { name: 'Presets' }).getAttribute('aria-selected')).toBe('false');
		expect(screen.getByRole('tab', { name: 'Team' }).getAttribute('aria-selected')).toBe('false');
	});

	it('DVE tab redirect button calls onOpenGraphics and closes modal', async () => {
		const closeFn = vi.fn();
		const openGfxFn = vi.fn();
		render(OASettingsModal, {
			props: defaultProps({ onclose: closeFn, onOpenGraphics: openGfxFn }),
		});
		await fireEvent.click(screen.getByRole('tab', { name: 'DVE' }));
		await fireEvent.click(screen.getByText('Open GFX Panel'));
		expect(closeFn).toHaveBeenCalledOnce();
		expect(openGfxFn).toHaveBeenCalledOnce();
	});

	it('encoder tab shows available encoders', async () => {
		render(OASettingsModal, { props: defaultProps() });
		await fireEvent.click(screen.getByRole('tab', { name: 'Encoder' }));
		expect(screen.getByText('x264 (Software)')).toBeTruthy();
	});
});
