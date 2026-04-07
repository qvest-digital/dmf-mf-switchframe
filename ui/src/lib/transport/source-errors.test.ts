import { describe, it, expect, beforeEach } from 'vitest';
import {
	setSourceError,
	clearSourceError,
	getSourceError,
	getSourceErrors,
	clearAllErrors,
	setSourceStalled,
	clearSourceStall,
	isSourceStalled,
	getStalledSources,
	clearAllStalls,
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

describe('source stall tracking', () => {
	beforeEach(() => {
		clearAllStalls();
	});

	it('returns false for unknown source', () => {
		expect(isSourceStalled('cam1')).toBe(false);
	});

	it('marks source as stalled', () => {
		setSourceStalled('cam1');
		expect(isSourceStalled('cam1')).toBe(true);
	});

	it('clears stall state', () => {
		setSourceStalled('cam1');
		clearSourceStall('cam1');
		expect(isSourceStalled('cam1')).toBe(false);
	});

	it('clearSourceStall is no-op for non-stalled key', () => {
		clearSourceStall('cam99'); // should not throw
		expect(isSourceStalled('cam99')).toBe(false);
	});

	it('getStalledSources returns all stalled keys', () => {
		setSourceStalled('cam1');
		setSourceStalled('cam3');
		const stalled = getStalledSources();
		expect(stalled.size).toBe(2);
		expect(stalled.has('cam1')).toBe(true);
		expect(stalled.has('cam3')).toBe(true);
	});

	it('clearAllStalls removes everything', () => {
		setSourceStalled('cam1');
		setSourceStalled('cam2');
		clearAllStalls();
		expect(getStalledSources().size).toBe(0);
	});

	it('setSourceStalled is idempotent', () => {
		setSourceStalled('cam1');
		setSourceStalled('cam1');
		expect(isSourceStalled('cam1')).toBe(true);
		expect(getStalledSources().size).toBe(1);
	});

	it('stalls and errors are independent', () => {
		setSourceError('cam1', 'decode error');
		setSourceStalled('cam1');
		clearSourceError('cam1');
		expect(isSourceStalled('cam1')).toBe(true);
		expect(getSourceError('cam1')).toBeNull();
	});
});
