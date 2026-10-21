import { MoQTransport } from '$lib/prism/moq-transport';

export type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'error';

export interface PrismConnectionConfig {
	url: string;
	onControlState: (data: Uint8Array) => void;
	onConnectionChange: (state: ConnectionState) => void;
}

/**
 * The stream whose session carries an engine's control track. Every engine
 * publishes the program preview, and only the control track of it is
 * subscribed, so the session costs no media.
 */
const CONTROL_STREAM = 'program-preview';

/**
 * A MoQ session to one engine that subscribes to its control track, which
 * carries every state change. Its transport also takes the fast-control
 * datagrams sent to that engine. A WebTransport session that never completes
 * the MoQ handshake is dropped by the engine, so this has to be a MoQ client.
 */
export function createPrismConnection(config: PrismConnectionConfig) {
	let state: ConnectionState = 'disconnected';
	let session: MoQTransport | null = null;
	let retryTimeout: ReturnType<typeof setTimeout> | null = null;
	let retryDelay = 2000;
	let stopped = false;
	const MAX_RETRY_DELAY = 30000;

	function _setConnectionState(newState: ConnectionState) {
		if (newState === state) return;
		state = newState;
		config.onConnectionChange(newState);
	}

	function _handleControlData(data: Uint8Array) {
		config.onControlState(data);
	}

	async function connect() {
		if (state === 'connecting' || state === 'connected') return;
		stopped = false;
		_setConnectionState('connecting');

		const noop = () => {};
		const s: MoQTransport = new MoQTransport(
			CONTROL_STREAM,
			{
				onTrackInfo: noop,
				onVideoFrame: noop,
				onAudioFrame: noop,
				onCaptionFrame: noop,
				onServerStats: noop,
				onControlState: _handleControlData,
				onClose: () => {
					if (stopped || session !== s) return;
					session = null;
					_setConnectionState('disconnected');
					scheduleRetry();
				},
				onError: noop,
			},
			64,
			config.url,
			['control'],
		);
		session = s;
		// connect() reports most failures through onError and a null
		// transport, but throws when the handshake fails after the transport
		// is up, such as a rejected subscription.
		let failed = false;
		try {
			await s.connect();
		} catch {
			failed = true;
		}
		// disconnect() may have landed while connect() was suspended; it
		// already closed the session.
		if (stopped || session !== s) {
			s.close();
			return;
		}
		if (failed || !s.getTransport()) {
			session = null;
			s.close();
			_setConnectionState('error');
			scheduleRetry();
			return;
		}
		retryDelay = 2000;
		_setConnectionState('connected');
	}

	function scheduleRetry() {
		if (stopped || retryTimeout) return;
		const jitter = retryDelay * 0.2 * Math.random();
		retryTimeout = setTimeout(() => {
			retryTimeout = null;
			retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY);
			connect();
		}, retryDelay + jitter);
	}

	function disconnect() {
		stopped = true;
		if (retryTimeout) {
			clearTimeout(retryTimeout);
			retryTimeout = null;
		}
		session?.close();
		session = null;
		_setConnectionState('disconnected');
	}

	return {
		get state() {
			return state;
		},
		get transport(): WebTransport | null {
			return session?.getTransport() ?? null;
		},
		connect,
		disconnect,
		// Test helpers (prefixed with _)
		_handleControlData,
		_setConnectionState,
	};
}
