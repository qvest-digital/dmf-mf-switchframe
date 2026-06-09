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

// localStorage/sessionStorage are missing from vitest's jsdom environment on
// newer Node versions (observed with Node 26 + vitest 4 + jsdom 28; fine on
// the Node 22 CI uses) — provide a spec-shaped in-memory implementation.
if (typeof globalThis.localStorage === 'undefined') {
	class MemoryStorage implements Storage {
		private store = new Map<string, string>();
		get length(): number {
			return this.store.size;
		}
		key(index: number): string | null {
			return [...this.store.keys()][index] ?? null;
		}
		getItem(key: string): string | null {
			return this.store.get(String(key)) ?? null;
		}
		setItem(key: string, value: string): void {
			this.store.set(String(key), String(value));
		}
		removeItem(key: string): void {
			this.store.delete(String(key));
		}
		clear(): void {
			this.store.clear();
		}
	}
	Object.defineProperty(globalThis, 'localStorage', {
		value: new MemoryStorage(),
		writable: true,
		configurable: true,
	});
	Object.defineProperty(globalThis, 'sessionStorage', {
		value: new MemoryStorage(),
		writable: true,
		configurable: true,
	});
}
