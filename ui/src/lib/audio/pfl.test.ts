import { describe, it, expect } from 'vitest';
import { createPFLManager } from './pfl';

describe('PFLManager', () => {
	it('should start with no active PFL', () => {
		const pfl = createPFLManager();
		expect(pfl.activeSource).toBeNull();
	});

	it('should enable PFL for a source', () => {
		const pfl = createPFLManager();
		pfl.enablePFL('cam1');
		expect(pfl.activeSource).toBe('cam1');
	});

	it('should disable PFL', () => {
		const pfl = createPFLManager();
		pfl.enablePFL('cam1');
		pfl.disablePFL();
		expect(pfl.activeSource).toBeNull();
	});

	it('should switch PFL between sources', () => {
		const pfl = createPFLManager();
		pfl.enablePFL('cam1');
		pfl.enablePFL('cam2');
		expect(pfl.activeSource).toBe('cam2');
	});

	it('should return levels for a source', () => {
		const pfl = createPFLManager();
		const levels = pfl.getSourceLevels('cam1');
		expect(levels).toEqual({ peakL: 0, peakR: 0, rmsL: 0, rmsR: 0 });
	});

	it('should clean up on destroy', () => {
		const pfl = createPFLManager();
		pfl.enablePFL('cam1');
		pfl.destroy();
		expect(pfl.activeSource).toBeNull();
	});
});
