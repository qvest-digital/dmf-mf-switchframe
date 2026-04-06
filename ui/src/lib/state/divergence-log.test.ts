import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createDivergenceLog, type DivergenceLogEntry } from './divergence-log.svelte';

describe('divergence log store', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	it('adds entries with timestamps', () => {
		const log = createDivergenceLog();
		vi.setSystemTime(1000);
		log.add({ type: 'transient', message: 'Clock drift detected' });
		expect(log.entries).toHaveLength(1);
		expect(log.entries[0].timestamp).toBe(1000);
		expect(log.entries[0].type).toBe('transient');
		expect(log.entries[0].message).toBe('Clock drift detected');
	});

	it('entries are reverse chronological (newest first)', () => {
		const log = createDivergenceLog();
		vi.setSystemTime(1000);
		log.add({ type: 'transient', message: 'First' });
		vi.setSystemTime(2000);
		log.add({ type: 'lagging', message: 'Second' });
		vi.setSystemTime(3000);
		log.add({ type: 'diverged', message: 'Third' });

		expect(log.entries).toHaveLength(3);
		expect(log.entries[0].message).toBe('Third');
		expect(log.entries[0].timestamp).toBe(3000);
		expect(log.entries[1].message).toBe('Second');
		expect(log.entries[1].timestamp).toBe(2000);
		expect(log.entries[2].message).toBe('First');
		expect(log.entries[2].timestamp).toBe(1000);
	});

	it('caps at maxEntries (add 120, get 100)', () => {
		const log = createDivergenceLog(100);
		for (let i = 0; i < 120; i++) {
			vi.setSystemTime(i * 100);
			log.add({ type: 'info', message: `Entry ${i}` });
		}
		expect(log.entries).toHaveLength(100);
		// Newest entry should be the last one added
		expect(log.entries[0].message).toBe('Entry 119');
		// Oldest kept should be entry 20 (first 20 were trimmed)
		expect(log.entries[99].message).toBe('Entry 20');
	});

	it('clear() empties entries', () => {
		const log = createDivergenceLog();
		log.add({ type: 'mismatch', message: 'State mismatch' });
		log.add({ type: 'resolved', message: 'Resolved' });
		expect(log.entries).toHaveLength(2);
		log.clear();
		expect(log.entries).toHaveLength(0);
	});

	it('custom maxEntries works', () => {
		const log = createDivergenceLog(5);
		for (let i = 0; i < 10; i++) {
			log.add({ type: 'info', message: `Entry ${i}` });
		}
		expect(log.entries).toHaveLength(5);
		expect(log.entries[0].message).toBe('Entry 9');
		expect(log.entries[4].message).toBe('Entry 5');
	});

	it('preserves warnings array on entries', () => {
		const log = createDivergenceLog();
		log.add({
			type: 'snapshot',
			message: 'Snapshot pushed',
			warnings: ['audio drift 12ms', 'preview mismatch'],
		});
		expect(log.entries[0].warnings).toEqual(['audio drift 12ms', 'preview mismatch']);
	});

	it('entries without warnings have undefined warnings', () => {
		const log = createDivergenceLog();
		log.add({ type: 'info', message: 'No warnings' });
		expect(log.entries[0].warnings).toBeUndefined();
	});

	it('deduplicates consecutive identical entries', () => {
		const log = createDivergenceLog();
		vi.setSystemTime(1000);
		log.add({ type: 'lagging', message: 'Engine lagging behind' });
		vi.setSystemTime(2000);
		log.add({ type: 'lagging', message: 'Engine lagging behind' });
		vi.setSystemTime(3000);
		log.add({ type: 'lagging', message: 'Engine lagging behind' });
		expect(log.entries).toHaveLength(1);
		expect(log.entries[0].timestamp).toBe(1000);
	});

	it('allows same type with different message', () => {
		const log = createDivergenceLog();
		vi.setSystemTime(1000);
		log.add({ type: 'diverged', message: 'Engines diverged' });
		vi.setSystemTime(2000);
		log.add({ type: 'diverged', message: 'Recovery failed: timeout' });
		expect(log.entries).toHaveLength(2);
	});

	it('allows same message with different type', () => {
		const log = createDivergenceLog();
		vi.setSystemTime(1000);
		log.add({ type: 'lagging', message: 'Engine lagging behind' });
		vi.setSystemTime(2000);
		log.add({ type: 'diverged', message: 'Engine lagging behind' });
		expect(log.entries).toHaveLength(2);
	});

	it('allows re-adding after a different entry breaks the streak', () => {
		const log = createDivergenceLog();
		vi.setSystemTime(1000);
		log.add({ type: 'lagging', message: 'Engine lagging behind' });
		vi.setSystemTime(2000);
		log.add({ type: 'resolved', message: 'Engines synced' });
		vi.setSystemTime(3000);
		log.add({ type: 'lagging', message: 'Engine lagging behind' });
		expect(log.entries).toHaveLength(3);
	});
});
