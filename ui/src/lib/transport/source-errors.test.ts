import { describe, it, expect, beforeEach } from 'vitest';
import {
	setSourceError,
	clearSourceError,
	getSourceError,
	getSourceErrors,
	clearAllErrors,
} from './source-errors.svelte';

describe('source-errors', () => {
	beforeEach(() => {
		clearAllErrors();
	});

	it('returns null for unknown source', () => {
		expect(getSourceError('cam1')).toBeNull();
	});

	it('sets and retrieves source error', () => {
		setSourceError('cam1', 'Audio: decoder failed');
		expect(getSourceError('cam1')).toBe('Audio: decoder failed');
	});

	it('overwrites existing error for same source', () => {
		setSourceError('cam1', 'first error');
		setSourceError('cam1', 'second error');
		expect(getSourceError('cam1')).toBe('second error');
	});

	it('clears a source error', () => {
		setSourceError('cam1', 'Transport: connection failed');
		clearSourceError('cam1');
		expect(getSourceError('cam1')).toBeNull();
	});

	it('clearSourceError is no-op for non-existent key', () => {
		clearSourceError('cam99'); // should not throw
		expect(getSourceError('cam99')).toBeNull();
	});

	it('getSourceErrors returns all errors', () => {
		setSourceError('cam1', 'error1');
		setSourceError('cam2', 'error2');
		const all = getSourceErrors();
		expect(all.size).toBe(2);
		expect(all.get('cam1')).toBe('error1');
		expect(all.get('cam2')).toBe('error2');
	});

	it('clearAllErrors removes everything', () => {
		setSourceError('cam1', 'error1');
		setSourceError('cam2', 'error2');
		clearAllErrors();
		expect(getSourceErrors().size).toBe(0);
	});

	it('tracks errors independently per source', () => {
		setSourceError('cam1', 'video decode error');
		setSourceError('cam2', 'audio decode error');
		clearSourceError('cam1');
		expect(getSourceError('cam1')).toBeNull();
		expect(getSourceError('cam2')).toBe('audio decode error');
	});
});
