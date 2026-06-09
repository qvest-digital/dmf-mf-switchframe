import { describe, it, expect } from 'vitest';
import { StreamBuffer } from './stream-buffer';

/**
 * Build a StreamBuffer backed by a reader that yields the given chunks in order
 * and then signals EOF. Lets us drive StreamBuffer.read without a real network
 * stream.
 */
function bufferFrom(...chunks: Uint8Array[]): StreamBuffer {
	let i = 0;
	const reader = {
		read: async () => {
			if (i < chunks.length) {
				return { value: chunks[i++], done: false };
			}
			return { value: undefined, done: true };
		},
		releaseLock: () => {},
	} as unknown as ReadableStreamDefaultReader<Uint8Array>;
	return new StreamBuffer(reader);
}

describe('StreamBuffer.read', () => {
	it('returns exactly n bytes spanning multiple chunks', async () => {
		const buf = bufferFrom(new Uint8Array([1, 2]), new Uint8Array([3, 4, 5]));
		expect(await buf.read(4)).toEqual(new Uint8Array([1, 2, 3, 4]));
		// Remaining byte is preserved for the next read.
		expect(await buf.read(1)).toEqual(new Uint8Array([5]));
	});

	it('returns null on EOF before n bytes are available', async () => {
		const buf = bufferFrom(new Uint8Array([1, 2]));
		expect(await buf.read(4)).toBeNull();
	});

	it('rejects n larger than maxLen without consuming the stream', async () => {
		// An oversized length sourced from an untrusted network varint must be
		// rejected immediately (return null) before any bytes are accumulated,
		// so a huge length cannot trigger an unbounded allocation / RangeError.
		const data = new Uint8Array([1, 2, 3, 4]);
		const buf = bufferFrom(data);

		expect(await buf.read(1024 * 1024, 8)).toBeNull();

		// The stream was not consumed by the rejected read: a subsequent
		// in-bounds read still sees the original bytes.
		expect(await buf.read(4, 8)).toEqual(new Uint8Array([1, 2, 3, 4]));
	});

	it('allows n up to and including maxLen', async () => {
		const buf = bufferFrom(new Uint8Array([1, 2, 3]));
		expect(await buf.read(3, 3)).toEqual(new Uint8Array([1, 2, 3]));
	});
});
