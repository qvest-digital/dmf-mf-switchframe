import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OAClipsSection from './OAClipsSection.svelte';

function defaultProps(overrides: Record<string, unknown> = {}) {
	return {
		clips: [
			{ id: 'clip-1', name: 'Bumper Intro', durationMs: 125000 },
			{ id: 'clip-2', name: 'Sponsor Spot', durationMs: 30000 },
			{ id: 'clip-3', name: 'Outro' },
		],
		onUploadClip: vi.fn(),
		onMakeSlate: vi.fn(),
		onDeleteClip: vi.fn(),
		...overrides,
	};
}

describe('OAClipsSection', () => {
	it('renders clip list with names and durations', () => {
		render(OAClipsSection, { props: defaultProps() });
		expect(screen.getByText('Bumper Intro')).toBeTruthy();
		expect(screen.getByText('Sponsor Spot')).toBeTruthy();
		expect(screen.getByText('Outro')).toBeTruthy();
		// 125000ms = 2:05
		expect(screen.getByText('2:05')).toBeTruthy();
		// 30000ms = 0:30
		expect(screen.getByText('0:30')).toBeTruthy();
	});

	it('slate button calls onMakeSlate', async () => {
		const fn = vi.fn();
		render(OAClipsSection, {
			props: defaultProps({ onMakeSlate: fn }),
		});
		const slateButtons = screen.getAllByTitle('Add to slates');
		await fireEvent.click(slateButtons[0]);
		expect(fn).toHaveBeenCalledOnce();
		expect(fn).toHaveBeenCalledWith('clip-1', 'Bumper Intro');
	});

	it('delete button calls onDeleteClip', async () => {
		const fn = vi.fn();
		render(OAClipsSection, {
			props: defaultProps({ onDeleteClip: fn }),
		});
		const deleteButtons = screen.getAllByTitle('Delete clip');
		await fireEvent.click(deleteButtons[0]);
		expect(fn).toHaveBeenCalledOnce();
		expect(fn).toHaveBeenCalledWith('clip-1');
	});

	it('asks before deleting a library clip, naming the shared library', async () => {
		const fn = vi.fn();
		render(OAClipsSection, {
			props: defaultProps({
				clips: [{ id: 'lib-1', name: 'Opener', libraryPath: 'Opener.mp4' }],
				onDeleteClip: fn,
			}),
		});
		await fireEvent.click(screen.getByTitle('Delete clip'));
		expect(fn).not.toHaveBeenCalled();
		expect(screen.getByRole('alertdialog').textContent).toContain('every future booking');

		await fireEvent.click(screen.getByText('Delete'));
		expect(fn).toHaveBeenCalledWith('lib-1');
		expect(screen.queryByRole('alertdialog')).toBeNull();
	});

	it('keeps a library clip when the delete is cancelled', async () => {
		const fn = vi.fn();
		render(OAClipsSection, {
			props: defaultProps({
				clips: [{ id: 'lib-1', name: 'Opener', libraryPath: 'Opener.mp4' }],
				onDeleteClip: fn,
			}),
		});
		await fireEvent.click(screen.getByTitle('Delete clip'));
		await fireEvent.click(screen.getByText('Cancel'));
		expect(fn).not.toHaveBeenCalled();
	});

	it('upload zone renders', () => {
		const { container } = render(OAClipsSection, { props: defaultProps() });
		const dropZone = container.querySelector('.upload-zone');
		expect(dropZone).toBeTruthy();
		const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement;
		expect(fileInput).toBeTruthy();
	});

	it('progress bar shown when uploadProgress provided', () => {
		render(OAClipsSection, {
			props: defaultProps({
				uploadProgress: { filename: 'test.mp4', percent: 45 },
			}),
		});
		expect(screen.getByText('test.mp4')).toBeTruthy();
		expect(screen.getByText('45%')).toBeTruthy();
	});

	it('progress bar not shown when uploadProgress is null', () => {
		render(OAClipsSection, {
			props: defaultProps({ uploadProgress: null }),
		});
		expect(screen.queryByText('%')).toBeNull();
	});

	it('renders empty state when no clips', () => {
		const { container } = render(OAClipsSection, {
			props: defaultProps({ clips: [] }),
		});
		const clipRows = container.querySelectorAll('.clip-row');
		expect(clipRows.length).toBe(0);
		// Upload zone should still be present
		const dropZone = container.querySelector('.upload-zone');
		expect(dropZone).toBeTruthy();
	});
});
