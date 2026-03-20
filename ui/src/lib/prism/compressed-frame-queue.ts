/**
 * CompressedFrameQueue — holds encoded H.264 frames ordered by PTS,
 * bounded by a max time duration. Frames are released just-in-time
 * based on the audio clock to prevent the VideoDecoder from buffering
 * too far ahead.
 */

export interface CompressedFrame {
	data: Uint8Array;
	timestamp: number; // microseconds
	isKeyframe: boolean;
	description?: Uint8Array; // avcC for keyframes
}

export class CompressedFrameQueue {
	private frames: CompressedFrame[] = [];
	private maxDurationUs: number;

	constructor(maxDurationUs: number) {
		this.maxDurationUs = maxDurationUs;
	}

	/**
	 * Appends a frame. If the time span (newest - oldest) exceeds
	 * maxDurationUs after insertion, evicts oldest frames until
	 * within bounds.
	 */
	push(data: Uint8Array, timestamp: number, isKeyframe: boolean, description?: Uint8Array): void {
		const frame: CompressedFrame = { data, timestamp, isKeyframe };
		if (description !== undefined) {
			frame.description = description;
		}

		// Insert in sorted order by timestamp
		let insertIdx = this.frames.length;
		for (let i = this.frames.length - 1; i >= 0; i--) {
			if (this.frames[i].timestamp <= timestamp) {
				break;
			}
			insertIdx = i;
		}
		this.frames.splice(insertIdx, 0, frame);

		// Evict oldest frames if span exceeds max duration
		this.evict();
	}

	/**
	 * Returns and removes all frames with timestamp <= targetPTS + lookaheadUs,
	 * in PTS order.
	 */
	drain(targetPTS: number, lookaheadUs: number): CompressedFrame[] {
		const threshold = targetPTS + lookaheadUs;
		let splitIdx = 0;

		while (splitIdx < this.frames.length && this.frames[splitIdx].timestamp <= threshold) {
			splitIdx++;
		}

		if (splitIdx === 0) {
			return [];
		}

		const result = this.frames.splice(0, splitIdx);
		return result;
	}

	/** Clears all frames. */
	flush(): void {
		this.frames.length = 0;
	}

	/** Returns the number of frames in the queue. */
	size(): number {
		return this.frames.length;
	}

	/** Returns the PTS of the oldest frame, or -1 if empty. */
	oldestPTS(): number {
		if (this.frames.length === 0) return -1;
		return this.frames[0].timestamp;
	}

	/** Returns the PTS of the newest frame, or -1 if empty. */
	newestPTS(): number {
		if (this.frames.length === 0) return -1;
		return this.frames[this.frames.length - 1].timestamp;
	}

	/** Evict oldest frames until span is within maxDurationUs. */
	private evict(): void {
		while (this.frames.length > 1) {
			const span = this.frames[this.frames.length - 1].timestamp - this.frames[0].timestamp;
			if (span <= this.maxDurationUs) {
				break;
			}
			this.frames.shift();
		}
	}
}
