import { describe, it, expect, beforeEach, vi } from 'vitest';
import { getLayoutMode, setLayoutMode } from './preferences';

describe('Layout Preferences', () => {
	beforeEach(() => {
		localStorage.clear();
		// Reset URL to default
		window.history.replaceState({}, '', '/');
	});

	describe('getLayoutMode', () => {
		it('returns traditional by default', () => {
			expect(getLayoutMode()).toBe('traditional');
		});

		it('returns simple when URL param mode=simple', () => {
			window.history.replaceState({}, '', '/?mode=simple');
			expect(getLayoutMode()).toBe('simple');
		});

		it('returns traditional for unknown URL param', () => {
			window.history.replaceState({}, '', '/?mode=unknown');
			expect(getLayoutMode()).toBe('traditional');
		});

		it('reads from localStorage when no URL param', () => {
			localStorage.setItem('switchframe-layout', 'simple');
			expect(getLayoutMode()).toBe('simple');
		});

		it('URL param takes priority over localStorage', () => {
			localStorage.setItem('switchframe-layout', 'traditional');
			window.history.replaceState({}, '', '/?mode=simple');
			expect(getLayoutMode()).toBe('simple');
		});

		it('returns traditional for invalid localStorage value', () => {
			localStorage.setItem('switchframe-layout', 'garbage');
			expect(getLayoutMode()).toBe('traditional');
		});
	});

	describe('setLayoutMode', () => {
		it('persists simple to localStorage', () => {
			setLayoutMode('simple');
			expect(localStorage.getItem('switchframe-layout')).toBe('simple');
		});

		it('persists traditional to localStorage', () => {
			setLayoutMode('traditional');
			expect(localStorage.getItem('switchframe-layout')).toBe('traditional');
		});

		it('value is readable by getLayoutMode', () => {
			setLayoutMode('simple');
			expect(getLayoutMode()).toBe('simple');
		});
	});
});
