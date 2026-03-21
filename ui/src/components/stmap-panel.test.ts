import { describe, it, expect } from 'vitest';

/**
 * Pure logic functions extracted from STMapPanel for testability.
 */

function deriveMapName(generator: string, sourceLabel: string): string {
	return `${generator}_${sourceLabel.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
}

function parseUploadFilename(filename: string): string {
	return filename.replace(/\.(exr|png|stmap)$/i, '');
}

function sampleCheckerboard(s: number, t: number, divisions: number): number {
	return (Math.floor(s * divisions) + Math.floor(t * divisions)) % 2 === 0 ? 200 : 55;
}

describe('STMap utilities', () => {
	describe('deriveMapName', () => {
		it('derives map name from generator and source label', () => {
			expect(deriveMapName('barrel', 'Camera 1')).toBe('barrel_Camera_1');
			expect(deriveMapName('fisheye_to_rectilinear', 'cam3')).toBe(
				'fisheye_to_rectilinear_cam3',
			);
		});

		it('replaces special characters with underscores', () => {
			expect(deriveMapName('barrel', 'My Camera #2!')).toBe('barrel_My_Camera__2_');
			expect(deriveMapName('barrel', 'cam.test')).toBe('barrel_cam_test');
		});

		it('preserves hyphens and underscores', () => {
			expect(deriveMapName('barrel', 'cam-1_test')).toBe('barrel_cam-1_test');
		});
	});

	describe('parseUploadFilename', () => {
		it('strips .exr extension', () => {
			expect(parseUploadFilename('lens_correction.exr')).toBe('lens_correction');
		});

		it('strips .png extension', () => {
			expect(parseUploadFilename('my-map.png')).toBe('my-map');
		});

		it('strips .stmap extension', () => {
			expect(parseUploadFilename('raw.stmap')).toBe('raw');
		});

		it('handles uppercase extensions', () => {
			expect(parseUploadFilename('test.EXR')).toBe('test');
			expect(parseUploadFilename('test.PNG')).toBe('test');
		});

		it('returns filename unchanged if no recognized extension', () => {
			expect(parseUploadFilename('no-extension')).toBe('no-extension');
			expect(parseUploadFilename('file.txt')).toBe('file.txt');
		});
	});

	describe('sampleCheckerboard', () => {
		it('returns light value for even checkerboard cells', () => {
			expect(sampleCheckerboard(0.0, 0.0, 8)).toBe(200); // 0+0 = even
			expect(sampleCheckerboard(0.25, 0.25, 8)).toBe(200); // 2+2 = even
		});

		it('returns dark value for odd checkerboard cells', () => {
			expect(sampleCheckerboard(0.13, 0.0, 8)).toBe(55); // 1+0 = odd
			expect(sampleCheckerboard(0.0, 0.13, 8)).toBe(55); // 0+1 = odd
		});

		it('handles identity mapping (s=x, t=y) correctly', () => {
			// At (0.5, 0.5) with 8 divisions: floor(4) + floor(4) = 8, even
			expect(sampleCheckerboard(0.5, 0.5, 8)).toBe(200);
			// At (0.5, 0.625) with 8 divisions: floor(4) + floor(5) = 9, odd
			expect(sampleCheckerboard(0.5, 0.625, 8)).toBe(55);
		});

		it('works with different division counts', () => {
			expect(sampleCheckerboard(0.0, 0.0, 4)).toBe(200);
			expect(sampleCheckerboard(0.3, 0.0, 4)).toBe(55); // floor(1.2) = 1, odd
		});

		it('handles edge case at s=1.0, t=1.0', () => {
			// floor(8) + floor(8) = 16, even
			expect(sampleCheckerboard(1.0, 1.0, 8)).toBe(200);
		});
	});
});
