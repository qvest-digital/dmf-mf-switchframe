import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import ConnectionStatus from './ConnectionStatus.svelte';

describe('ConnectionStatus', () => {
	it('should show "LIVE" text when state is webtransport', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'webtransport' } });
		expect(container.textContent).toContain('LIVE');
	});

	it('should show "POLLING" text when state is polling', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'polling' } });
		expect(container.textContent).toContain('POLLING');
	});

	it('should show "OFFLINE" text when state is disconnected', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'disconnected' } });
		expect(container.textContent).toContain('OFFLINE');
	});

	it('should apply status-live class for webtransport state', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'webtransport' } });
		const badge = container.querySelector('.status-live');
		expect(badge).toBeTruthy();
	});

	it('should apply status-warning class for polling state', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'polling' } });
		const badge = container.querySelector('.status-warning');
		expect(badge).toBeTruthy();
	});

	it('should apply status-error class for disconnected state', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'disconnected' } });
		const badge = container.querySelector('.status-error');
		expect(badge).toBeTruthy();
	});

	it('should not apply other status classes for webtransport', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'webtransport' } });
		expect(container.querySelector('.status-warning')).toBeFalsy();
		expect(container.querySelector('.status-error')).toBeFalsy();
	});

	it('should not apply other status classes for polling', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'polling' } });
		expect(container.querySelector('.status-live')).toBeFalsy();
		expect(container.querySelector('.status-error')).toBeFalsy();
	});

	it('should not apply other status classes for disconnected', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'disconnected' } });
		expect(container.querySelector('.status-live')).toBeFalsy();
		expect(container.querySelector('.status-warning')).toBeFalsy();
	});

	it('should render with connection-status base class', () => {
		const { container } = render(ConnectionStatus, { props: { state: 'webtransport' } });
		const badge = container.querySelector('.connection-status');
		expect(badge).toBeTruthy();
	});
});
