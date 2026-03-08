import { describe, it, expect, beforeEach } from 'vitest';
import { getApiBaseUrl, setApiBaseUrl, resolveApiUrl } from './base-url';

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
