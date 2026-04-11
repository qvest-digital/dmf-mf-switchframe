import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OASlatesSection from './OASlatesSection.svelte';

function defaultProps(overrides: Record<string, unknown> = {}) {
	return {
		slates: [
			{ clipId: 'slate-1', name: 'Technical Difficulties' },
			{ clipId: 'slate-2', name: 'Be Right Back' },
		],
		onFireSlate: vi.fn(),
		onAddSlate: vi.fn(),
		onRemoveSlate: vi.fn(),
		...overrides,
	};
}

describe('OASlatesSection', () => {
	it('renders slate buttons with names', () => {
		render(OASlatesSection, { props: defaultProps() });
		expect(screen.getByText('Technical Difficulties')).toBeTruthy();
		expect(screen.getByText('Be Right Back')).toBeTruthy();
	});

	it('click calls onFireSlate with clipId and name', async () => {
		const fn = vi.fn();
		render(OASlatesSection, {
			props: defaultProps({ onFireSlate: fn }),
		});
		await fireEvent.click(screen.getByText('Technical Difficulties'));
		expect(fn).toHaveBeenCalledOnce();
		expect(fn).toHaveBeenCalledWith('slate-1', 'Technical Difficulties');
	});

	it('add button calls onAddSlate', async () => {
		const fn = vi.fn();
		render(OASlatesSection, {
			props: defaultProps({ onAddSlate: fn }),
		});
		const addBtn = screen.getByText('+');
		await fireEvent.click(addBtn);
		expect(fn).toHaveBeenCalledOnce();
	});

	it('ON AIR badge shown when isOnAir is true', () => {
		render(OASlatesSection, {
			props: defaultProps({
				slates: [
					{ clipId: 'slate-1', name: 'Technical Difficulties', isOnAir: true },
					{ clipId: 'slate-2', name: 'Be Right Back' },
				],
			}),
		});
		expect(screen.getByText('ON AIR')).toBeTruthy();
	});

	it('ON AIR badge not shown when isOnAir is false', () => {
		render(OASlatesSection, { props: defaultProps() });
		expect(screen.queryByText('ON AIR')).toBeNull();
	});

	it('empty state shows only add button', () => {
		render(OASlatesSection, {
			props: defaultProps({ slates: [] }),
		});
		expect(screen.getByText('+')).toBeTruthy();
		expect(screen.queryByText('Technical Difficulties')).toBeNull();
		expect(screen.queryByText('Be Right Back')).toBeNull();
	});

	it('right-click calls onRemoveSlate with clipId', async () => {
		const fn = vi.fn();
		render(OASlatesSection, {
			props: defaultProps({ onRemoveSlate: fn }),
		});
		const btn = screen.getByText('Technical Difficulties');
		await fireEvent.contextMenu(btn);
		expect(fn).toHaveBeenCalledOnce();
		expect(fn).toHaveBeenCalledWith('slate-1');
	});
});
