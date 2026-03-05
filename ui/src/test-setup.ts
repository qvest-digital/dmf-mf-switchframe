/**
 * Global test setup for vitest (jsdom environment).
 * Provides browser API polyfills that jsdom does not include.
 */

// ResizeObserver is not available in jsdom — provide a minimal stub.
if (typeof globalThis.ResizeObserver === 'undefined') {
	globalThis.ResizeObserver = class ResizeObserver {
		private callback: ResizeObserverCallback;
		constructor(callback: ResizeObserverCallback) {
			this.callback = callback;
		}
		observe() {}
		unobserve() {}
		disconnect() {}
	};
}
