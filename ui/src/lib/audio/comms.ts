/**
 * CommsAudioManager — WebTransport bidirectional stream for operator comms audio.
 *
 * Captures microphone via getUserMedia, encodes to Opus via WebCodecs
 * AudioEncoder, sends over a WebTransport bidirectional stream, receives
 * Opus from server's N-1 mix, decodes via WebCodecs AudioDecoder, and
 * plays through AudioContext.
 *
 * Wire protocol: [1 byte type][2 bytes BE length][payload]
 *   - MSG_AUDIO (0x01): Opus-encoded audio frame
 *   - MSG_CONTROL (0x02): JSON control message
 */

const MSG_AUDIO = 0x01;
const MSG_CONTROL = 0x02;
const FRAME_SIZE = 960; // 20ms at 48kHz
const SAMPLE_RATE = 48000;

export interface CommsConfig {
	operatorId: string;
	operatorName: string;
	onError?: (error: string) => void;
	/** Called when comms audio activity changes (for program audio ducking). */
	onCommsActive?: (active: boolean) => void;
}

export class CommsAudioManager {
	private config: CommsConfig;
	private captureCtx: AudioContext | null = null;
	private playbackCtx: AudioContext | null = null;
	private mediaStream: MediaStream | null = null;
	private workletNode: AudioWorkletNode | null = null;
	private writer: WritableStreamDefaultWriter<Uint8Array> | null = null;
	private reader: ReadableStreamDefaultReader<Uint8Array> | null = null;
	private running = false;
	private sampleBuffer: Float32Array = new Float32Array(0);

	// WebCodecs Opus encoder/decoder
	private encoder: AudioEncoder | null = null;
	private decoder: AudioDecoder | null = null;

	// Playback scheduling
	private nextPlayTime = 0;

	// Comms activity tracking for auto-duck
	private commsActivityTimer: ReturnType<typeof setTimeout> | null = null;
	private commsActive = false;

	// Monotonic decode timestamp (microseconds, 20ms per frame)
	private decodeTimestamp = 0;

	// Comms playback gain (boosts comms audio relative to program)
	private commsGain: GainNode | null = null;

	constructor(config: CommsConfig) {
		this.config = config;
	}

	/**
	 * Start comms audio: request microphone, open a bidirectional stream,
	 * set up Opus encode/decode, and begin capture + read loops.
	 */
	async start(transport: WebTransport): Promise<void> {
		if (this.running) return;

		try {
			// Request microphone access
			this.mediaStream = await navigator.mediaDevices.getUserMedia({
				audio: {
					sampleRate: SAMPLE_RATE,
					channelCount: 1,
					echoCancellation: true,
					noiseSuppression: true,
					autoGainControl: true,
				},
			});

			// Open a bidirectional stream for comms audio
			const bidiStream = await transport.createBidirectionalStream();
			this.writer = bidiStream.writable.getWriter();
			this.reader = bidiStream.readable.getReader();

			// Send handshake identifying this operator
			const hello = JSON.stringify({
				action: 'hello',
				operatorId: this.config.operatorId,
			});
			const helloBytes = new TextEncoder().encode(hello);
			const helloMsg = new Uint8Array(3 + helloBytes.length);
			helloMsg[0] = MSG_CONTROL;
			helloMsg[1] = (helloBytes.length >> 8) & 0xff;
			helloMsg[2] = helloBytes.length & 0xff;
			helloMsg.set(helloBytes, 3);
			await this.writer.write(helloMsg);

			// Create AudioContext for capture and playback
			// Separate contexts: capture context for mic processing,
			// playback context for mix output. Keeps echo cancellation
			// from treating our playback as echo to suppress.
			this.captureCtx = new AudioContext({ sampleRate: SAMPLE_RATE });
			this.playbackCtx = new AudioContext({ sampleRate: SAMPLE_RATE });
			this.commsGain = this.playbackCtx.createGain();
			this.commsGain.gain.value = 2.0; // +6dB boost so comms cuts through program
			this.commsGain.connect(this.playbackCtx.destination);
			this.nextPlayTime = 0;

			// Set up WebCodecs Opus encoder
			this.encoder = new AudioEncoder({
				output: (chunk) => this.onEncodedChunk(chunk),
				error: (e) => this.config.onError?.(`Opus encode error: ${e.message}`),
			});
			this.encoder.configure({
				codec: 'opus',
				sampleRate: SAMPLE_RATE,
				numberOfChannels: 1,
				bitrate: 32000,
			});

			// Set up WebCodecs Opus decoder
			this.decoder = new AudioDecoder({
				output: (frame) => this.onDecodedFrame(frame),
				error: (e) => this.config.onError?.(`Opus decode error: ${e.message}`),
			});
			this.decoder.configure({
				codec: 'opus',
				sampleRate: SAMPLE_RATE,
				numberOfChannels: 1,
			});

			this.running = true;

			await this.captureLoop();
			this.readLoop();
		} catch (err) {
			const msg = err instanceof Error ? err.message : String(err);
			this.config.onError?.(`Comms start failed: ${msg}`);
			this.cleanup();
			throw err;
		}
	}

	/**
	 * Stop comms audio: close writer, stop mic tracks, close AudioContext.
	 */
	async stop(): Promise<void> {
		this.running = false;
		this.cleanup();
	}

	/**
	 * Toggle microphone mute by enabling/disabling the mic track directly.
	 */
	setMuted(muted: boolean): void {
		if (!this.mediaStream) return;
		for (const track of this.mediaStream.getAudioTracks()) {
			track.enabled = !muted;
		}
	}

	/**
	 * Called by WebCodecs AudioEncoder when an Opus frame is ready.
	 * Sends the encoded data over the WebTransport stream.
	 */
	private onEncodedChunk(chunk: EncodedAudioChunk): void {
		if (!this.writer || !this.running) return;

		const data = new Uint8Array(chunk.byteLength);
		chunk.copyTo(data);

		// Wire protocol: [type(1)][length BE(2)][payload]
		const message = new Uint8Array(3 + data.length);
		message[0] = MSG_AUDIO;
		message[1] = (data.length >> 8) & 0xff;
		message[2] = data.length & 0xff;
		message.set(data, 3);

		this.writer.write(message).catch((err) => {
			if (this.running) {
				const msg = err instanceof Error ? err.message : String(err);
				this.config.onError?.(`Comms send error: ${msg}`);
			}
		});
	}

	/**
	 * Called by WebCodecs AudioDecoder when a decoded audio frame is ready.
	 * Schedules playback via AudioContext.
	 */
	private onDecodedFrame(frame: AudioData): void {
		if (!this.playbackCtx) {
			frame.close();
			return;
		}

		// Signal comms activity for auto-duck
		if (!this.commsActive) {
			this.commsActive = true;
			this.config.onCommsActive?.(true);
		}
		if (this.commsActivityTimer) clearTimeout(this.commsActivityTimer);
		this.commsActivityTimer = setTimeout(() => {
			this.commsActive = false;
			this.config.onCommsActive?.(false);
		}, 300); // 300ms after last frame = comms went quiet

		const samples = frame.numberOfFrames;
		const float32 = new Float32Array(samples);
		frame.copyTo(float32, { planeIndex: 0 });
		frame.close();

		// Create AudioBuffer and schedule playback
		const buffer = this.playbackCtx.createBuffer(1, samples, SAMPLE_RATE);
		buffer.getChannelData(0).set(float32);

		const source = this.playbackCtx.createBufferSource();
		source.buffer = buffer;
		source.connect(this.commsGain ?? this.playbackCtx.destination);

		// Schedule seamlessly after previous buffer
		const now = this.playbackCtx.currentTime;
		if (this.nextPlayTime < now) {
			this.nextPlayTime = now;
		}
		source.start(this.nextPlayTime);
		this.nextPlayTime += samples / SAMPLE_RATE;
	}

	/**
	 * Read incoming messages from the bidirectional stream.
	 * Parses the wire protocol and feeds Opus data to the decoder.
	 */
	private async readLoop(): Promise<void> {
		if (!this.reader) return;

		let buffer = new Uint8Array(0);

		try {
			while (this.running) {
				const { value, done } = await this.reader.read();
				if (done || !this.running) break;
				if (!value) continue;

				// Append new data to buffer
				const newBuffer = new Uint8Array(buffer.length + value.length);
				newBuffer.set(buffer);
				newBuffer.set(value, buffer.length);
				buffer = newBuffer;

				// Parse complete messages from buffer
				while (buffer.length >= 3) {
					const type = buffer[0];
					const length = (buffer[1] << 8) | buffer[2];
					const totalLength = 3 + length;

					if (buffer.length < totalLength) break;

					const payload = buffer.slice(3, totalLength);
					buffer = buffer.slice(totalLength);

					if (type === MSG_AUDIO && this.decoder) {
						// Feed Opus data to WebCodecs decoder
						const chunk = new EncodedAudioChunk({
							type: 'key', // Opus frames are independent
							timestamp: this.decodeTimestamp,
							data: payload,
						});
						this.decodeTimestamp += 20_000; // 20ms per frame in microseconds
						this.decoder.decode(chunk);
					} else if (type === MSG_CONTROL) {
						// eslint-disable-next-line no-console
						console.log('[comms] control:', new TextDecoder().decode(payload));
					}
				}
			}
		} catch (err) {
			if (this.running) {
				const msg = err instanceof Error ? err.message : String(err);
				this.config.onError?.(`Comms read error: ${msg}`);
			}
		}
	}

	/**
	 * Set up the microphone capture pipeline using AudioWorkletNode.
	 * The worklet processor runs on the audio rendering thread, sending
	 * 128-sample chunks to the main thread. We accumulate them into
	 * 960-sample (20ms) frames for the WebCodecs Opus encoder.
	 */
	private async captureLoop(): Promise<void> {
		if (!this.captureCtx || !this.mediaStream) return;

		// Register the worklet processor from an inline Blob URL.
		const processorCode = `
			class CommsCaptureProcessor extends AudioWorkletProcessor {
				process(inputs) {
					const input = inputs[0];
					if (input.length > 0 && input[0].length > 0) {
						this.port.postMessage(input[0]);
					}
					return true;
				}
			}
			registerProcessor('comms-capture', CommsCaptureProcessor);
		`;
		const blob = new Blob([processorCode], { type: 'application/javascript' });
		const url = URL.createObjectURL(blob);
		try {
			await this.captureCtx.audioWorklet.addModule(url);
		} finally {
			URL.revokeObjectURL(url);
		}

		const source = this.captureCtx.createMediaStreamSource(this.mediaStream);
		this.workletNode = new AudioWorkletNode(this.captureCtx, 'comms-capture', {
			channelCount: 1,
			numberOfInputs: 1,
			numberOfOutputs: 1, // Must have an output for browsers to process inputs
		});

		this.sampleBuffer = new Float32Array(0);
		let timestamp = 0;

		// Receive 128-sample chunks from the audio thread, accumulate
		// into 960-sample frames, and feed to the Opus encoder.
		this.workletNode.port.onmessage = (event: MessageEvent<Float32Array>) => {
			if (!this.running || !this.encoder) return;

			const input = event.data;

			// Accumulate samples
			const newBuffer = new Float32Array(this.sampleBuffer.length + input.length);
			newBuffer.set(this.sampleBuffer);
			newBuffer.set(input, this.sampleBuffer.length);
			this.sampleBuffer = newBuffer;

			// Feed complete 20ms frames to the Opus encoder
			while (this.sampleBuffer.length >= FRAME_SIZE) {
				const frame = this.sampleBuffer.slice(0, FRAME_SIZE);
				this.sampleBuffer = this.sampleBuffer.slice(FRAME_SIZE);

				const audioData = new AudioData({
					format: 'f32',
					sampleRate: SAMPLE_RATE,
					numberOfFrames: FRAME_SIZE,
					numberOfChannels: 1,
					timestamp: timestamp,
					data: frame,
				});
				this.encoder.encode(audioData);
				audioData.close();
				timestamp += (FRAME_SIZE / SAMPLE_RATE) * 1_000_000; // microseconds
			}
		};

		source.connect(this.workletNode);
	}

	/**
	 * Clean up all resources.
	 */
	private cleanup(): void {
		if (this.commsActivityTimer) {
			clearTimeout(this.commsActivityTimer);
			this.commsActivityTimer = null;
		}
		if (this.commsActive) {
			this.commsActive = false;
			this.config.onCommsActive?.(false);
		}

		if (this.workletNode) {
			this.workletNode.port.onmessage = null;
			this.workletNode.disconnect();
			this.workletNode = null;
		}

		if (this.encoder) {
			try { this.encoder.close(); } catch { /* ignore */ }
			this.encoder = null;
		}

		if (this.decoder) {
			try { this.decoder.close(); } catch { /* ignore */ }
			this.decoder = null;
		}

		if (this.writer) {
			this.writer.close().catch(() => {});
			this.writer = null;
		}

		if (this.reader) {
			this.reader.cancel().catch(() => {});
			this.reader = null;
		}

		if (this.mediaStream) {
			for (const track of this.mediaStream.getTracks()) {
				track.stop();
			}
			this.mediaStream = null;
		}

		if (this.captureCtx) {
			this.captureCtx.close().catch(() => {});
			this.captureCtx = null;
		}

		if (this.commsGain) {
			this.commsGain.disconnect();
			this.commsGain = null;
		}

		if (this.playbackCtx) {
			this.playbackCtx.close().catch(() => {});
			this.playbackCtx = null;
		}

		this.sampleBuffer = new Float32Array(0);
	}
}
