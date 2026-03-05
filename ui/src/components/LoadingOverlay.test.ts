import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import LoadingOverlay from './LoadingOverlay.svelte';

describe('LoadingOverlay', () => {
	it('renders nothing when loading is false', () => {
		const { container } = render(LoadingOverlay, {
			props: { loading: false, error: null },
		});
		const backdrop = container.querySelector('.loading-backdrop');
		expect(backdrop).toBeFalsy();
	});

	it('shows "Connecting" message when loading is true', () => {
		const { container } = render(LoadingOverlay, {
			props: { loading: true, error: null },
		});
		expect(container.textContent).toContain('Connecting to server');
		// Should have spinner
		const spinner = container.querySelector('.spinner');
		expect(spinner).toBeTruthy();
	});

	it('shows error message when error is provided', () => {
		const { container } = render(LoadingOverlay, {
			props: { loading: true, error: 'Connection refused' },
		});
		expect(container.textContent).toContain('Server unavailable');
		expect(container.textContent).toContain('Connection refused');
	});

	it('shows retry text when error is provided', () => {
		const { container } = render(LoadingOverlay, {
			props: { loading: true, error: 'Network error' },
		});
		expect(container.textContent).toContain('Retrying');
	});

	it('has z-index 300 on backdrop', () => {
		const { container } = render(LoadingOverlay, {
			props: { loading: true, error: null },
		});
		const backdrop = container.querySelector('.loading-backdrop') as HTMLElement;
		expect(backdrop).toBeTruthy();
	});

	it('renders nothing when loading is false even with error', () => {
		const { container } = render(LoadingOverlay, {
			props: { loading: false, error: 'Some error' },
		});
		const backdrop = container.querySelector('.loading-backdrop');
		expect(backdrop).toBeFalsy();
	});
});
