import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest';

// The worker module registers self.onmessage on import. A fake VideoDecoder
// counts what reaches it and reports a decode queue the test controls: a
// decoder that has not put out a frame since the burst began.
const decoded: number[] = [];
let queued = 0;
let emit: ((frame: { timestamp: number }) => void) | null = null;
class FakeVideoDecoder {
	state = 'unconfigured';
	constructor(init: { output: (f: unknown) => void }) {
		emit = (f) => init.output(f);
	}
	get decodeQueueSize() { return queued; }
	configure() { this.state = 'configured'; }
	decode(chunk: { timestamp: number }) { decoded.push(chunk.timestamp); queued++; }
	reset() {}
	close() { this.state = 'closed'; }
	async flush() {}
}
class FakeChunk {
	timestamp: number;
	constructor(init: { timestamp: number }) { this.timestamp = init.timestamp; }
}

const send = (data: Record<string, unknown>) =>
	(self.onmessage as (e: MessageEvent) => void)({ data } as MessageEvent);
const FRAME_US = 20_000; // 50 fps

describe('video decoder worker backlog', () => {
	beforeAll(async () => {
		vi.stubGlobal('VideoDecoder', FakeVideoDecoder);
		vi.stubGlobal('EncodedVideoChunk', FakeChunk);
		vi.spyOn(self, 'postMessage').mockImplementation(() => {});
		await import('./video-decoder-worker');
	});
	beforeEach(() => {
		decoded.length = 0;
		queued = 0;
		send({ type: 'configure', codec: 'avc1.640028', width: 1920, height: 1080 });
		// One frame out, so the decoder has a position to lag behind.
		send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: true, timestamp: 0, isDisco: false });
		emit!({ timestamp: 0 });
		queued = 0;
		decoded.length = 0;
	});

	// A main thread held up for half a second hands the worker 25 chunks at
	// once at 50 fps. With 16 queued the worker dropped the 17th and every
	// delta after it until the next keyframe, a second of picture.
	it('decodes a half-second burst whole', () => {
		for (let i = 1; i <= 25; i++) {
			send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: false, timestamp: i * FRAME_US, isDisco: false });
		}
		expect(decoded).toHaveLength(25);
	});

	it('drops to the next keyframe once the backlog is more than a second behind', () => {
		for (let i = 1; i <= 60; i++) {
			send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: false, timestamp: i * FRAME_US, isDisco: false });
		}
		// 20 ms frames: 50 are within a second of the last output.
		expect(decoded).toHaveLength(50);

		send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: true, timestamp: 61 * FRAME_US, isDisco: false });
		expect(decoded).toHaveLength(50);
		// The decoder catches up and puts out the backlog: the keyframe after
		// it is decoded.
		emit!({ timestamp: 60 * FRAME_US });
		queued = 0;
		send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: true, timestamp: 62 * FRAME_US, isDisco: false });
		expect(decoded).toHaveLength(51);
	});
});
