import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OABugSection from './OABugSection.svelte';

function defaultProps(overrides: Record<string, unknown> = {}) {
	return {
		bugs: [
			{ id: 'bug-1', name: 'Network Logo' },
			{ id: 'bug-2', name: 'News Bug' },
		],
		onSelectBug: vi.fn(),
		onUploadBug: vi.fn(),
		...overrides,
	};
}

describe('OABugSection', () => {
	it('renders bug list', () => {
		render(OABugSection, { props: defaultProps() });
		expect(screen.getByText('Network Logo')).toBeTruthy();
		expect(screen.getByText('News Bug')).toBeTruthy();
	});

	it('radio selection calls onSelectBug', async () => {
		const fn = vi.fn();
		render(OABugSection, {
			props: defaultProps({ onSelectBug: fn }),
		});
		const radios = screen.getAllByRole('radio');
		await fireEvent.click(radios[1]);
		expect(fn).toHaveBeenCalledOnce();
		expect(fn).toHaveBeenCalledWith('bug-2');
	});

	it('active badge shown on selected bug', () => {
		render(OABugSection, {
			props: defaultProps({ activeBugId: 'bug-1' }),
		});
		expect(screen.getByText('Active')).toBeTruthy();
	});

	it('no active badge when no bug selected', () => {
		render(OABugSection, { props: defaultProps() });
		expect(screen.queryByText('Active')).toBeNull();
	});

	it('empty state renders upload prompt', () => {
		render(OABugSection, {
			props: defaultProps({ bugs: [] }),
		});
		expect(screen.getByText(/No bug loaded/)).toBeTruthy();
	});

	it('upload button triggers file input', async () => {
		const { container } = render(OABugSection, { props: defaultProps() });
		const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement;
		expect(fileInput).toBeTruthy();
		expect(fileInput.style.display).toBe('none');
		const uploadBtn = screen.getByText('Upload');
		expect(uploadBtn).toBeTruthy();
	});
});
