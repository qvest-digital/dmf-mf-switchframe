import { describe, it, expect } from 'vitest';
import { wtBaseURL, type ServerInfo } from './transport-utils';

function info(addr: string): ServerInfo {
	return { certHash: new Uint8Array(), addr, trusted: false };
}

describe('wtBaseURL', () => {
	it('uses the page host when the server advertises a listen address only', () => {
		expect(wtBaseURL(info(':8080'), 'ui.example.com')).toBe('https://ui.example.com:8080');
		expect(wtBaseURL(info('0.0.0.0:4443'), 'ui.example.com')).toBe('https://ui.example.com:4443');
		expect(wtBaseURL(info('[::]:4443'), 'ui.example.com')).toBe('https://ui.example.com:4443');
	});

	// A page served through an HTTP proxy that carries no UDP has to reach
	// WebTransport on the host the server names instead of its own.
	it('uses the host the server advertises', () => {
		expect(wtBaseURL(info('wt.example.com:8080'), 'ui.example.com')).toBe('https://wt.example.com:8080');
		expect(wtBaseURL(info('[2001:db8::1]:8080'), 'ui.example.com')).toBe('https://[2001:db8::1]:8080');
	});

	it('falls back to the default port', () => {
		expect(wtBaseURL(info(''), 'ui.example.com')).toBe('https://ui.example.com:4443');
	});
});
