import { describe, it, expect } from 'vitest';
import { parseRawYUVFrame } from './yuv-renderer';

describe('parseRawYUVFrame', () => {
	it('should parse a valid frame with correct width, height, and plane offsets', () => {
		const width = 8;
		const height = 4;
		const ySize = width * height; // 32
		const cbSize = (width >> 1) * (height >> 1); // 8
		const crSize = cbSize; // 8
		const totalSize = 8 + ySize + cbSize + crSize; // 56

		const data = new Uint8Array(totalSize);
		const view = new DataView(data.buffer);
		view.setUint32(0, width);
		view.setUint32(4, height);

		// Fill Y plane with 0x10 (limited-range black)
		for (let i = 8; i < 8 + ySize; i++) data[i] = 0x10;
		// Fill Cb plane with 0x80 (neutral chroma)
		for (let i = 8 + ySize; i < 8 + ySize + cbSize; i++) data[i] = 0x80;
		// Fill Cr plane with 0x80 (neutral chroma)
		for (let i = 8 + ySize + cbSize; i < totalSize; i++) data[i] = 0x80;

		const result = parseRawYUVFrame(data);
		expect(result).not.toBeNull();
		expect(result!.width).toBe(8);
		expect(result!.height).toBe(4);
		expect(result!.y.length).toBe(ySize);
		expect(result!.cb.length).toBe(cbSize);
		expect(result!.cr.length).toBe(crSize);

		// Verify plane data
		expect(result!.y[0]).toBe(0x10);
		expect(result!.cb[0]).toBe(0x80);
		expect(result!.cr[0]).toBe(0x80);
	});

	it('should return null for data shorter than 8 bytes', () => {
		expect(parseRawYUVFrame(new Uint8Array(0))).toBeNull();
		expect(parseRawYUVFrame(new Uint8Array(4))).toBeNull();
		expect(parseRawYUVFrame(new Uint8Array(7))).toBeNull();
	});

	it('should return null for header-only data (no YUV planes)', () => {
		const data = new Uint8Array(8);
		const view = new DataView(data.buffer);
		view.setUint32(0, 1920); // width
		view.setUint32(4, 1080); // height
		// No plane data follows

		expect(parseRawYUVFrame(data)).toBeNull();
	});

	it('should parse a small 4x2 frame with exact plane sizes', () => {
		const width = 4;
		const height = 2;
		const ySize = 4 * 2; // 8
		const cbSize = 2 * 1; // 2
		const crSize = 2 * 1; // 2
		const totalSize = 8 + ySize + cbSize + crSize; // 20

		const data = new Uint8Array(totalSize);
		const view = new DataView(data.buffer);
		view.setUint32(0, width);
		view.setUint32(4, height);

		// Fill planes with distinct values for verification
		for (let i = 0; i < ySize; i++) data[8 + i] = 0xAA;
		for (let i = 0; i < cbSize; i++) data[8 + ySize + i] = 0xBB;
		for (let i = 0; i < crSize; i++) data[8 + ySize + cbSize + i] = 0xCC;

		const result = parseRawYUVFrame(data);
		expect(result).not.toBeNull();
		expect(result!.width).toBe(4);
		expect(result!.height).toBe(2);
		expect(result!.y.length).toBe(8);
		expect(result!.cb.length).toBe(2);
		expect(result!.cr.length).toBe(2);

		// Verify each plane has the expected fill value
		for (let i = 0; i < result!.y.length; i++) expect(result!.y[i]).toBe(0xAA);
		for (let i = 0; i < result!.cb.length; i++) expect(result!.cb[i]).toBe(0xBB);
		for (let i = 0; i < result!.cr.length; i++) expect(result!.cr[i]).toBe(0xCC);
	});

	it('should return null when data is shorter than expected for given dimensions', () => {
		const width = 16;
		const height = 16;
		// Expected: 8 + 256 + 64 + 64 = 392 bytes
		// Provide only 100 bytes
		const data = new Uint8Array(100);
		const view = new DataView(data.buffer);
		view.setUint32(0, width);
		view.setUint32(4, height);

		expect(parseRawYUVFrame(data)).toBeNull();
	});

	it('should return null for zero width or height', () => {
		const data = new Uint8Array(8);
		const view = new DataView(data.buffer);

		// Zero width
		view.setUint32(0, 0);
		view.setUint32(4, 4);
		expect(parseRawYUVFrame(data)).toBeNull();

		// Zero height
		view.setUint32(0, 4);
		view.setUint32(4, 0);
		expect(parseRawYUVFrame(data)).toBeNull();
	});

	it('should handle subarray input correctly (non-zero byteOffset)', () => {
		const width = 4;
		const height = 2;
		const ySize = 8;
		const cbSize = 2;
		const crSize = 2;
		const frameSize = 8 + ySize + cbSize + crSize; // 20

		// Embed the frame inside a larger buffer at offset 10
		const outer = new Uint8Array(10 + frameSize + 5);
		const view = new DataView(outer.buffer, 10, frameSize);
		view.setUint32(0, width);
		view.setUint32(4, height);
		for (let i = 0; i < ySize; i++) outer[10 + 8 + i] = 0x55;

		const sub = outer.subarray(10, 10 + frameSize);
		const result = parseRawYUVFrame(sub);
		expect(result).not.toBeNull();
		expect(result!.width).toBe(4);
		expect(result!.height).toBe(2);
		expect(result!.y[0]).toBe(0x55);
	});
});
