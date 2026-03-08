/**
 * API base URL for HTTP/3 commands.
 * Empty string = same-origin (production).
 * Set to QUIC server origin for dev (e.g. "https://localhost:8080").
 */
let apiBaseUrl = '';

export function getApiBaseUrl(): string {
	return apiBaseUrl;
}

export function setApiBaseUrl(url: string): void {
	apiBaseUrl = url.replace(/\/$/, '');
}

/** Prepend the API base URL to a relative path. */
export function resolveApiUrl(path: string): string {
	return apiBaseUrl + path;
}
