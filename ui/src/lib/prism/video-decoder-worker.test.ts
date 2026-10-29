import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest';

// The worker module registers self.onmessage on import. A fake VideoDecoder
// counts what reaches it and reports a decode queue the test controls: a
// decoder that has not put out a frame since the burst began.
const decoded: number[] = [];
let queued = 0;
let emit: ((frame: { timestamp: number }) => void) | null = null;
let decoderError: ((e: Error) => void) | null = null;
class FakeVideoDecoder {
	state = 'unconfigured';
	constructor(init: { output: (f: unknown) => void; error: (e: Error) => void }) {
		emit = (f) => init.output(f);
		decoderError = (e) => init.error(e);
	}
	get decodeQueueSize() { return queued; }
	configure() { this.state = 'configured'; queued = 0; }
	decode(chunk: { timestamp: number }) { decoded.push(chunk.timestamp); queued++; }
	reset() { this.state = 'unconfigured'; queued = 0; }
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

	// Once a backlog is dropped, the last frame out is older than the
	// keyframe decoding resumes at. Measured against it, every delta queued
	// behind the still-decoding keyframe read as more than a second behind:
	// the decoder put out the keyframe alone, one picture a GOP, for as
	// long as the source ran.
	it('decodes the GOP after a dropped backlog, not only its keyframe', () => {
		for (let i = 1; i <= 60; i++) {
			send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: false, timestamp: i * FRAME_US, isDisco: false });
		}
		emit!({ timestamp: 50 * FRAME_US });
		queued = 0;
		for (let i = 61; i < 150; i++) {
			send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: false, timestamp: i * FRAME_US, isDisco: false });
		}
		decoded.length = 0;
		// A 2 s GOP at 50 fps: the keyframe is still in the decode queue
		// when the ten deltas after it arrive, then the decoder keeps up.
		for (let i = 150; i < 250; i++) {
			send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: i === 150, timestamp: i * FRAME_US, isDisco: false });
			if (i >= 160) {
				for (let j = i === 160 ? 150 : i; j <= i; j++) emit!({ timestamp: j * FRAME_US });
				queued = 0;
			}
		}
		expect(decoded).toHaveLength(100);
	});

	// Whatever the timestamps, a backlog stops at 120 chunks.
	it('drops past 120 queued chunks', () => {
		for (let i = 1; i <= 130; i++) {
			// Timestamps that never run a second ahead of the last output.
			send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: false, timestamp: i * 10, isDisco: false });
		}
		expect(decoded).toHaveLength(120);
	});

	// A discontinuity starts a new timeline, whose timestamps a lag against
	// the old one's last frame would read as far ahead.
	it('takes a discontinuity keyframe however far its timestamp jumps', () => {
		queued = 5;
		send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: true, timestamp: 3_600_000_000, isDisco: true });
		expect(decoded).toEqual([3_600_000_000]);
	});

	// After an error the decoder is reset and has put out nothing: lag
	// against the old decoder's last frame would drop its first keyframe.
	it('measures lag afresh after a decoder error', async () => {
		send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: false, timestamp: FRAME_US, isDisco: false });
		decoderError!(new Error('decode failed'));
		queued = 3;
		send({ type: 'decode', data: new ArrayBuffer(1), isKeyframe: true, timestamp: 5_000_000, isDisco: false });
		expect(decoded).toEqual([FRAME_US, 5_000_000]);
	});
});
