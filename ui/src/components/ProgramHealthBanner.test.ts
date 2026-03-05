import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import ProgramHealthBanner from './ProgramHealthBanner.svelte';

describe('ProgramHealthBanner', () => {
	it('renders nothing when program source is healthy', () => {
		const { container } = render(ProgramHealthBanner, {
			props: { programSource: 'cam1', status: 'healthy' },
		});
		expect(container.querySelector('.program-health-banner')).toBeNull();
	});

	it('renders nothing when no program source', () => {
		const { container } = render(ProgramHealthBanner, {
			props: { programSource: '', status: 'stale' },
		});
		expect(container.querySelector('.program-health-banner')).toBeNull();
	});

	it('renders banner when program source is stale', () => {
		const { container } = render(ProgramHealthBanner, {
			props: { programSource: 'cam1', status: 'stale' },
		});
		const banner = container.querySelector('.program-health-banner');
		expect(banner).not.toBeNull();
		expect(banner?.textContent).toContain('cam1');
		expect(banner?.textContent).toContain('STALE');
	});

	it('renders banner with no_signal status', () => {
		const { container } = render(ProgramHealthBanner, {
			props: { programSource: 'cam2', status: 'no_signal' },
		});
		const banner = container.querySelector('.program-health-banner');
		expect(banner).not.toBeNull();
		expect(banner?.textContent).toContain('NO SIGNAL');
	});

	it('renders banner with offline status', () => {
		const { container } = render(ProgramHealthBanner, {
			props: { programSource: 'cam3', status: 'offline' },
		});
		const banner = container.querySelector('.program-health-banner');
		expect(banner).not.toBeNull();
		expect(banner?.textContent).toContain('cam3');
		expect(banner?.textContent).toContain('OFFLINE');
	});

	it('has role="alert" for screen readers', () => {
		const { container } = render(ProgramHealthBanner, {
			props: { programSource: 'cam1', status: 'offline' },
		});
		const banner = container.querySelector('[role="alert"]');
		expect(banner).not.toBeNull();
	});

	it('has aria-live="assertive" for urgent announcements', () => {
		const { container } = render(ProgramHealthBanner, {
			props: { programSource: 'cam1', status: 'stale' },
		});
		const banner = container.querySelector('[aria-live="assertive"]');
		expect(banner).not.toBeNull();
	});
});
