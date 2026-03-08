import { resolveApiUrl } from '$lib/api/base-url';

/** Server connection info returned by fetchServerInfo. */
export interface ServerInfo {
	certHash: Uint8Array;
	/** WebTransport host:port (e.g. ":4443"). */
	addr: string;
	/** True when the server is using an externally-provided (trusted) certificate. */
	trusted: boolean;
}

/**
 * Fetches the server's self-signed certificate hash and WebTransport
 * address from the /api/cert-hash endpoint.
 */
export async function fetchServerInfo(): Promise<ServerInfo> {
	const resp = await fetch(resolveApiUrl("/api/cert-hash"));
	if (!resp.ok) {
		throw new Error(`cert-hash fetch failed: HTTP ${resp.status}`);
	}
	const data = await resp.json();
	const hashBase64: string = data.hash;
	const hashBinary = atob(hashBase64);
	const hashBuffer = new Uint8Array(hashBinary.length);
	for (let i = 0; i < hashBinary.length; i++) {
		hashBuffer[i] = hashBinary.charCodeAt(i);
	}
	return {
		certHash: hashBuffer,
		addr: data.addr ?? ":4443",
		trusted: data.trusted === true,
	};
}

/**
 * Derives the WebTransport base URL from the server info, using the
 * current page hostname and the server's configured port.
 */
export function wtBaseURL(info: ServerInfo): string {
	// addr is typically ":4443" or "0.0.0.0:4443"
	const parts = info.addr.split(":");
	const port = parts[parts.length - 1] || "4443";
	return `https://${window.location.hostname}:${port}`;
}

/**
 * Creates and connects a WebTransport session. When the server uses a
 * trusted certificate (e.g. mkcert), no cert pinning is needed. Otherwise
 * the self-signed certificate hash is pinned for dev mode.
 */
export async function connectWebTransport(url: string, certHash: Uint8Array, trusted: boolean): Promise<WebTransport> {
	const opts: WebTransportOptions = {};
	if (!trusted && certHash.length > 0) {
		opts.serverCertificateHashes = [
			{
				algorithm: "sha-256",
				value: certHash.buffer as ArrayBuffer,
			},
		];
	}
	const transport = new WebTransport(url, opts);
	await transport.ready;
	return transport;
}
