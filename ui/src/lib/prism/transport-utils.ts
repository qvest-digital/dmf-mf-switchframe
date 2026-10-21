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
 * Derives the WebTransport base URL from the server info: the host the server
 * advertises in addr when it names one, else the current page hostname, and
 * the server's configured port. A page served through an HTTP proxy that
 * carries no UDP reaches WebTransport on another host than its own.
 */
export function wtBaseURL(info: ServerInfo, pageHost: string = window.location.hostname): string {
	// addr is ":4443", "0.0.0.0:4443", "[::]:4443" or "wt.example.com:4443"
	const sep = info.addr.lastIndexOf(":");
	const port = (sep >= 0 ? info.addr.slice(sep + 1) : "") || "4443";
	const host = (sep >= 0 ? info.addr.slice(0, sep) : info.addr).replace(/^\[|\]$/g, "");
	const listenOnly = host === "" || host === "0.0.0.0" || host === "::";
	const target = listenOnly ? pageHost : host.includes(":") ? `[${host}]` : host;
	return `https://${target}:${port}`;
}

/**
 * Creates and connects a WebTransport session. Always uses
 * serverCertificateHashes for cert pinning — required because the
 * Prism WebTransport server implements draft-02, which Chrome only
 * uses in dev mode (with cert pinning). Trusted CA certs (e.g. mkcert)
 * cause Chrome to use the final RFC protocol, which Prism doesn't
 * support yet.
 */
export async function connectWebTransport(url: string, certHash: Uint8Array, trusted: boolean): Promise<WebTransport> {
	const opts: WebTransportOptions = {};
	// Only use certificate pinning for self-signed certs (dev mode).
	// CA-signed certs (production) use standard TLS verification —
	// Chrome rejects serverCertificateHashes with trusted certificates.
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
