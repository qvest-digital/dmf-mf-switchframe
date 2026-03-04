export type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'error';

export interface PrismConnectionConfig {
	url: string;
	onControlState: (data: Uint8Array) => void;
	onConnectionChange: (state: ConnectionState) => void;
}

export function createPrismConnection(config: PrismConnectionConfig) {
	let state: ConnectionState = 'disconnected';
	let transport: WebTransport | null = null;
	let retryTimeout: ReturnType<typeof setTimeout> | null = null;

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
		_setConnectionState('connecting');

		try {
			// Fetch cert hash for self-signed dev cert
			const certRes = await fetch(`${config.url}/api/cert-hash`);
			const certHash = await certRes.text();

			// Connect via WebTransport with cert pinning
			transport = new WebTransport(config.url, {
				serverCertificateHashes: [
					{
						algorithm: 'sha-256',
						value: Uint8Array.from(atob(certHash), (c) => c.charCodeAt(0)).buffer,
					},
				],
			});
			await transport.ready;
			_setConnectionState('connected');

			// Monitor for close
			transport.closed
				.then(() => {
					_setConnectionState('disconnected');
					scheduleRetry();
				})
				.catch(() => {
					_setConnectionState('error');
					scheduleRetry();
				});
		} catch {
			_setConnectionState('error');
			scheduleRetry();
		}
	}

	function scheduleRetry() {
		if (retryTimeout) return;
		retryTimeout = setTimeout(() => {
			retryTimeout = null;
			connect();
		}, 2000);
	}

	function disconnect() {
		if (retryTimeout) {
			clearTimeout(retryTimeout);
			retryTimeout = null;
		}
		transport?.close();
		transport = null;
		_setConnectionState('disconnected');
	}

	return {
		get state() {
			return state;
		},
		connect,
		disconnect,
		// Test helpers (prefixed with _)
		_handleControlData,
		_setConnectionState,
	};
}
