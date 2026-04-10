import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OASettingsModal from './OASettingsModal.svelte';

function defaultProps(overrides: Record<string, unknown> = {}) {
	return {
		visible: true,
		onclose: vi.fn(),
		crState: {},
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
		expect(screen.getByText('Presets panel content')).toBeTruthy();
	});

	it('clicking tab changes content', async () => {
		render(OASettingsModal, { props: defaultProps() });
		expect(screen.getByText('Presets panel content')).toBeTruthy();

		await fireEvent.click(screen.getByRole('tab', { name: 'DVE' }));
		expect(screen.getByText('DVE panel content')).toBeTruthy();
		expect(screen.queryByText('Presets panel content')).toBeNull();

		await fireEvent.click(screen.getByRole('tab', { name: 'Team' }));
		expect(screen.getByText('Team panel content')).toBeTruthy();
		expect(screen.queryByText('DVE panel content')).toBeNull();
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

	it('escape key closes', async () => {
		const closeFn = vi.fn();
		render(OASettingsModal, {
			props: defaultProps({ onclose: closeFn }),
		});
		await fireEvent.keyDown(window, { code: 'Escape' });
		expect(closeFn).toHaveBeenCalledOnce();
	});

	it('shows SETTINGS title', () => {
		render(OASettingsModal, { props: defaultProps() });
		expect(screen.getByText('SETTINGS')).toBeTruthy();
	});

	it('each tab shows its content', async () => {
		render(OASettingsModal, { props: defaultProps() });
		const tabContent: Record<string, string> = {
			Presets: 'Presets panel content',
			DVE: 'DVE panel content',
			Keys: 'Keys panel content',
			Video: 'Video processing panel content',
			Encoder: 'Encoder panel content',
			Format: 'Format panel content',
			Team: 'Team panel content',
		};

		for (const [tabName, content] of Object.entries(tabContent)) {
			await fireEvent.click(screen.getByRole('tab', { name: tabName }));
			expect(screen.getByText(content)).toBeTruthy();
		}
	});

	it('non-active tabs have aria-selected false', async () => {
		render(OASettingsModal, { props: defaultProps() });
		await fireEvent.click(screen.getByRole('tab', { name: 'Encoder' }));
		expect(screen.getByRole('tab', { name: 'Encoder' }).getAttribute('aria-selected')).toBe('true');
		expect(screen.getByRole('tab', { name: 'Presets' }).getAttribute('aria-selected')).toBe('false');
		expect(screen.getByRole('tab', { name: 'Team' }).getAttribute('aria-selected')).toBe('false');
	});
});
