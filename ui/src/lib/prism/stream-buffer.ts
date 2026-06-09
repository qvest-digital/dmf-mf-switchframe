/**
 * Buffered reader for a ReadableStream that supports exact-length reads.
 * Accumulates chunks from the underlying stream and returns contiguous
 * byte arrays of the requested size.
 */
export class StreamBuffer {
	private reader: ReadableStreamDefaultReader<Uint8Array>;
	private chunks: Uint8Array[] = [];
	private totalBytes = 0;

	constructor(reader: ReadableStreamDefaultReader<Uint8Array>) {
		this.reader = reader;
	}

	/**
	 * Read exactly `n` bytes from the stream, or null on EOF.
	 *
	 * `maxLen` is an optional sanity cap for lengths that originate from
	 * untrusted wire data (network varints). When `n` exceeds `maxLen` the
	 * read is rejected immediately by returning null — before any bytes are
	 * accumulated — so a buggy or compromised relay sending a huge length
	 * cannot trigger an unbounded allocation / RangeError or memory-pressure
	 * DoS. Callers that pass a fixed, locally-computed length (e.g. 1- or
	 * 2-byte framing reads) omit `maxLen`.
	 */
	async read(n: number, maxLen?: number): Promise<Uint8Array | null> {
		if (maxLen !== undefined && n > maxLen) return null;

		while (this.totalBytes < n) {
			const { value, done } = await this.reader.read();
			if (done || !value) {
				if (this.totalBytes === 0) return null;
				break;
			}
			this.chunks.push(value);
			this.totalBytes += value.length;
		}

		if (this.totalBytes < n) return null;

		const result = new Uint8Array(n);
		let offset = 0;
		let remaining = n;

		while (remaining > 0 && this.chunks.length > 0) {
			const chunk = this.chunks[0];
			const take = Math.min(chunk.length, remaining);

			result.set(chunk.subarray(0, take), offset);
			offset += take;
			remaining -= take;

			if (take < chunk.length) {
				this.chunks[0] = chunk.subarray(take);
			} else {
				this.chunks.shift();
			}
			this.totalBytes -= take;
		}

		return result;
	}
}
