import { describe, it, expect } from 'vitest';
import {
	codecLabel,
	colorFormatLabel,
	formatBadgeText,
	filterEncodersByCodec,
} from './format-badge';
import type { PipelineFormatInfo, EncoderInfo } from '$lib/api/types';

describe('codecLabel', () => {
	it('returns H.264 for undefined codec', () => {
		expect(codecLabel(undefined)).toBe('H.264');
	});

	it('returns H.264 for h264 codec', () => {
		expect(codecLabel('h264')).toBe('H.264');
	});

	it('returns HEVC for h265 codec', () => {
		expect(codecLabel('h265')).toBe('HEVC');
	});

	it('returns HEVC for hevc codec', () => {
		expect(codecLabel('hevc')).toBe('HEVC');
	});
});

describe('colorFormatLabel', () => {
	it('returns 420 8-bit for undefined', () => {
		expect(colorFormatLabel(undefined)).toBe('420 8-bit');
	});

	it('returns 420 8-bit for yuv420p', () => {
		expect(colorFormatLabel('yuv420p')).toBe('420 8-bit');
	});

	it('returns 422 10-bit for yuv422p10le', () => {
		expect(colorFormatLabel('yuv422p10le')).toBe('422 10-bit');
	});

	it('parses unknown format strings containing 422 and 10', () => {
		expect(colorFormatLabel('yuv422p10be')).toBe('422 10-bit');
	});

	it('returns 422 8-bit for formats with 422 but no 10', () => {
		expect(colorFormatLabel('yuv422p')).toBe('422 8-bit');
	});

	it('returns 420 10-bit for formats with 10 but no 422', () => {
		expect(colorFormatLabel('yuv420p10le')).toBe('420 10-bit');
	});
});

describe('formatBadgeText', () => {
	it('returns null when format is undefined', () => {
		expect(formatBadgeText(undefined)).toBeNull();
	});

	it('returns null when codec is missing', () => {
		const format: PipelineFormatInfo = {
			width: 1920,
			height: 1080,
			fpsNum: 30,
			fpsDen: 1,
			name: 'standard',
		};
		expect(formatBadgeText(format)).toBeNull();
	});

	it('returns H.264 · 420 8-bit for standard format', () => {
		const format: PipelineFormatInfo = {
			width: 1920,
			height: 1080,
			fpsNum: 30,
			fpsDen: 1,
			name: 'standard',
			codec: 'h264',
			colorFormat: 'yuv420p',
		};
		expect(formatBadgeText(format)).toBe('H.264 · 420 8-bit');
	});

	it('returns HEVC · 420 8-bit for standard-hevc format', () => {
		const format: PipelineFormatInfo = {
			width: 1920,
			height: 1080,
			fpsNum: 30,
			fpsDen: 1,
			name: 'standard-hevc',
			codec: 'h265',
			colorFormat: 'yuv420p',
		};
		expect(formatBadgeText(format)).toBe('HEVC · 420 8-bit');
	});

	it('returns HEVC · 422 10-bit for professional format', () => {
		const format: PipelineFormatInfo = {
			width: 1920,
			height: 1080,
			fpsNum: 30,
			fpsDen: 1,
			name: 'professional',
			codec: 'h265',
			colorFormat: 'yuv422p10le',
		};
		expect(formatBadgeText(format)).toBe('HEVC · 422 10-bit');
	});
});

describe('filterEncodersByCodec', () => {
	const avcEncoders: EncoderInfo[] = [
		{ name: 'libx264', displayName: 'x264 (software)', isDefault: true },
		{ name: 'h264_nvenc', displayName: 'NVENC H.264', isDefault: false },
		{ name: 'h264_videotoolbox', displayName: 'VideoToolbox H.264', isDefault: false },
	];

	const hevcEncoders: EncoderInfo[] = [
		{ name: 'libx265', displayName: 'x265 (software)', isDefault: true },
		{ name: 'hevc_nvenc', displayName: 'NVENC HEVC', isDefault: false },
		{ name: 'hevc_videotoolbox', displayName: 'VideoToolbox HEVC', isDefault: false },
	];

	const allEncoders = [...avcEncoders, ...hevcEncoders];

	it('returns only AVC encoders when codec is h264', () => {
		const result = filterEncodersByCodec(allEncoders, 'h264');
		expect(result).toEqual(avcEncoders);
	});

	it('returns only AVC encoders when codec is undefined', () => {
		const result = filterEncodersByCodec(allEncoders, undefined);
		expect(result).toEqual(avcEncoders);
	});

	it('returns only HEVC encoders when codec is h265', () => {
		const result = filterEncodersByCodec(allEncoders, 'h265');
		expect(result).toEqual(hevcEncoders);
	});

	it('returns only HEVC encoders when codec is hevc', () => {
		const result = filterEncodersByCodec(allEncoders, 'hevc');
		expect(result).toEqual(hevcEncoders);
	});

	it('returns all encoders when filtering yields empty list (safety)', () => {
		const oddEncoders: EncoderInfo[] = [
			{ name: 'something_else', displayName: 'Mystery Encoder', isDefault: true },
		];
		const result = filterEncodersByCodec(oddEncoders, 'h264');
		expect(result).toEqual(oddEncoders);
	});

	it('returns empty array when input is empty', () => {
		const result = filterEncodersByCodec([], 'h264');
		expect(result).toEqual([]);
	});

	it('matches openh264 as AVC encoder', () => {
		const encoders: EncoderInfo[] = [
			{ name: 'openh264', displayName: 'OpenH264', isDefault: false },
			{ name: 'hevc_nvenc', displayName: 'NVENC HEVC', isDefault: false },
		];
		const result = filterEncodersByCodec(encoders, 'h264');
		expect(result).toHaveLength(1);
		expect(result[0].name).toBe('openh264');
	});
});
