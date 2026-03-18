/**
 * CommsAudioManager — WebTransport bidirectional stream for operator comms audio.
 *
 * Captures microphone via getUserMedia, sends raw PCM over a WebTransport
 * bidirectional stream, and receives audio from other operators.
 *
 * Wire protocol: [1 byte type][2 bytes BE length][payload]
 *   - MSG_AUDIO (0x01): raw audio payload
 *   - MSG_CONTROL (0x02): control message payload
 *
 * NOTE: This initial version sends raw int16 PCM as a placeholder.
 * WASM Opus encode/decode will be integrated later (see TODO comments).
 */

const MSG_AUDIO = 0x01;
const MSG_CONTROL = 0x02;
const FRAME_SIZE = 960; // 20ms at 48kHz
const SAMPLE_RATE = 48000;

export interface CommsConfig {
	operatorId: string;
	operatorName: string;
	onError?: (error: string) => void;
}

export class CommsAudioManager {
	private config: CommsConfig;
	private audioContext: AudioContext | null = null;
	private mediaStream: MediaStream | null = null;
	private scriptProcessor: ScriptProcessorNode | null = null;
	private writer: WritableStreamDefaultWriter<Uint8Array> | null = null;
	private reader: ReadableStreamDefaultReader<Uint8Array> | null = null;
	private running = false;
	private sampleBuffer: Float32Array = new Float32Array(0);

	constructor(config: CommsConfig) {
		this.config = config;
	}

	/**
	 * Start comms audio: request microphone, open a bidirectional stream,
	 * and begin capture + read loops.
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

			// Create AudioContext and start capture
			this.audioContext = new AudioContext({ sampleRate: SAMPLE_RATE });
			this.running = true;

			this.captureLoop();
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
	 * Read incoming messages from the bidirectional stream.
	 * Parses the wire protocol and routes by message type.
	 */
	private async readLoop(): Promise<void> {
		if (!this.reader) return;

		// Accumulation buffer for partial reads
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

					if (buffer.length < totalLength) {
						// Wait for more data
						break;
					}

					const payload = buffer.slice(3, totalLength);
					buffer = buffer.slice(totalLength);

					if (type === MSG_AUDIO) {
						this.playAudio(payload);
					} else if (type === MSG_CONTROL) {
						// eslint-disable-next-line no-console
						console.log('[comms] control message:', new TextDecoder().decode(payload));
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
	 * Set up the microphone capture pipeline.
	 * Uses ScriptProcessorNode to collect PCM samples and send them
	 * in FRAME_SIZE (960 sample / 20ms) chunks.
	 */
	private captureLoop(): void {
		if (!this.audioContext || !this.mediaStream) return;

		const source = this.audioContext.createMediaStreamSource(this.mediaStream);

		// ScriptProcessorNode is deprecated but universally supported.
		// Will be replaced by AudioWorklet when WASM Opus codec is integrated.
		const bufferSize = 4096;
		this.scriptProcessor = this.audioContext.createScriptProcessor(bufferSize, 1, 1);
		this.sampleBuffer = new Float32Array(0);

		this.scriptProcessor.onaudioprocess = (event: AudioProcessingEvent) => {
			if (!this.running) return;

			const input = event.inputBuffer.getChannelData(0);

			// Accumulate samples
			const newBuffer = new Float32Array(this.sampleBuffer.length + input.length);
			newBuffer.set(this.sampleBuffer);
			newBuffer.set(input, this.sampleBuffer.length);
			this.sampleBuffer = newBuffer;

			// Send complete frames
			while (this.sampleBuffer.length >= FRAME_SIZE) {
				const frame = this.sampleBuffer.slice(0, FRAME_SIZE);
				this.sampleBuffer = this.sampleBuffer.slice(FRAME_SIZE);
				this.sendAudio(frame);
			}
		};

		source.connect(this.scriptProcessor);
		this.scriptProcessor.connect(this.audioContext.destination);
	}

	/**
	 * Convert float32 PCM to int16 and send over the wire.
	 * Wire format: [0x01, lenHi, lenLo, ...int16 PCM bytes]
	 *
	 * TODO: Replace raw PCM with WASM Opus encode when codec is integrated.
	 */
	private sendAudio(pcm: Float32Array): void {
		if (!this.writer || !this.running) return;

		// Convert float32 [-1.0, 1.0] to int16 [-32768, 32767]
		const int16 = new Int16Array(pcm.length);
		for (let i = 0; i < pcm.length; i++) {
			const s = Math.max(-1, Math.min(1, pcm[i]));
			int16[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
		}

		const payload = new Uint8Array(int16.buffer);
		const payloadLength = payload.length;

		// Wire protocol: [type(1)][length BE(2)][payload]
		const message = new Uint8Array(3 + payloadLength);
		message[0] = MSG_AUDIO;
		message[1] = (payloadLength >> 8) & 0xff;
		message[2] = payloadLength & 0xff;
		message.set(payload, 3);

		this.writer.write(message).catch((err) => {
			if (this.running) {
				const msg = err instanceof Error ? err.message : String(err);
				this.config.onError?.(`Comms send error: ${msg}`);
			}
		});
	}

	/**
	 * Play received audio data.
	 *
	 * TODO: Integrate WASM Opus decoder here. For now this is a placeholder
	 * that would decode the payload and play it through the AudioContext.
	 */
	private playAudio(_opusData: Uint8Array): void {
		// TODO: Opus WASM decode + AudioContext playback
		// 1. Decode Opus frame to float32 PCM via WASM module
		// 2. Create AudioBuffer from decoded PCM
		// 3. Schedule playback via AudioBufferSourceNode
	}

	/**
	 * Clean up all resources.
	 */
	private cleanup(): void {
		if (this.scriptProcessor) {
			this.scriptProcessor.disconnect();
			this.scriptProcessor = null;
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

		if (this.audioContext) {
			this.audioContext.close().catch(() => {});
			this.audioContext = null;
		}

		this.sampleBuffer = new Float32Array(0);
	}
}
