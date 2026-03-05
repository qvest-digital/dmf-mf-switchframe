import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import ErrorBoundary from './ErrorBoundary.svelte';

describe('ErrorBoundary', () => {
	it('should render without crashing when no children provided', () => {
		const { container } = render(ErrorBoundary);
		// With optional chaining on children render, no error is thrown
		// and the error boundary overlay should NOT be visible
		expect(container.querySelector('.error-boundary-overlay')).toBeNull();
	});

	it('should not show error UI in normal state', () => {
		const { container } = render(ErrorBoundary);
		expect(container.querySelector('.error-title')).toBeNull();
		expect(container.querySelector('.error-message')).toBeNull();
		expect(container.querySelector('.retry-btn')).toBeNull();
		expect(container.querySelector('.reload-btn')).toBeNull();
	});

	// Note: Testing the actual error boundary behavior (child throws -> recovery UI
	// with "Try Again" and "Reload Page" buttons) requires a child component that
	// throws during rendering. In Svelte 5, <svelte:boundary> catches errors from
	// child component render and shows the `failed` snippet. However,
	// @testing-library/svelte does not provide a straightforward way to pass
	// snippet children that intentionally throw during render.
	//
	// The error boundary recovery UI is validated via E2E tests where a real
	// component tree can trigger the boundary. The recovery UI includes:
	// - "Something went wrong" heading
	// - Error message in monospace box
	// - "Try Again" button (calls reset to re-render children)
	// - "Reload Page" button (calls window.location.reload())
});
