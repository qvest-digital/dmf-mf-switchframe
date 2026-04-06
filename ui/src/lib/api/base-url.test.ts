import { describe, it, expect, beforeEach } from 'vitest';
import { getApiBaseUrl, setApiBaseUrl, resolveApiUrl, setEngineUrls, getEngineUrls } from './base-url';

describe('base-url', () => {
	beforeEach(() => {
		setApiBaseUrl('');
	});

	it('should return empty string by default (same-origin)', () => {
		expect(getApiBaseUrl()).toBe('');
	});

	it('should return configured URL after setApiBaseUrl', () => {
		setApiBaseUrl('https://localhost:8080');
		expect(getApiBaseUrl()).toBe('https://localhost:8080');
	});

	it('should strip trailing slash', () => {
		setApiBaseUrl('https://localhost:8080/');
		expect(getApiBaseUrl()).toBe('https://localhost:8080');
	});

	it('should resolve relative URLs with base', () => {
		setApiBaseUrl('https://localhost:8080');
		expect(resolveApiUrl('/api/switch/cut')).toBe('https://localhost:8080/api/switch/cut');
	});

	it('should resolve relative URLs without base (same-origin)', () => {
		expect(resolveApiUrl('/api/switch/cut')).toBe('/api/switch/cut');
	});
});

describe('engine URLs', () => {
	beforeEach(() => {
		setEngineUrls(null);
	});

	it('getEngineUrls returns null by default', () => {
		expect(getEngineUrls()).toBeNull();
	});

	it('setEngineUrls stores and retrieves engine URLs', () => {
		const engines = [
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		];
		setEngineUrls(engines);
		expect(getEngineUrls()).toEqual(engines);
	});

	it('setEngineUrls(null) clears engine URLs', () => {
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);
		expect(getEngineUrls()).not.toBeNull();
		setEngineUrls(null);
		expect(getEngineUrls()).toBeNull();
	});
});
