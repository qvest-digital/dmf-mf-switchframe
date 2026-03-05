import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import HealthAlarm from './HealthAlarm.svelte';

describe('HealthAlarm', () => {
	it('renders nothing when health is healthy', () => {
		const { container } = render(HealthAlarm, {
			props: { health: 'healthy', sourceLabel: 'CAM 1' },
		});
		expect(container.querySelector('.health-alarm')).toBeNull();
		expect(container.textContent).toBe('');
	});

	it('shows alarm text when health is stale', () => {
		const { container } = render(HealthAlarm, {
			props: { health: 'stale', sourceLabel: 'CAM 1' },
		});
		expect(container.textContent).toContain('STALE');
	});

	it('shows alarm text when health is no_signal', () => {
		const { container } = render(HealthAlarm, {
			props: { health: 'no_signal', sourceLabel: 'CAM 1' },
		});
		expect(container.textContent).toContain('NO SIGNAL');
	});

	it('shows alarm text when health is offline', () => {
		const { container } = render(HealthAlarm, {
			props: { health: 'offline', sourceLabel: 'CAM 1' },
		});
		expect(container.textContent).toContain('OFFLINE');
	});

	it('contains the source label in the message', () => {
		const { container } = render(HealthAlarm, {
			props: { health: 'no_signal', sourceLabel: 'CAM 3' },
		});
		expect(container.textContent).toContain('CAM 3');
	});

	it('has role="alert" attribute', () => {
		const { container } = render(HealthAlarm, {
			props: { health: 'offline', sourceLabel: 'CAM 1' },
		});
		const alert = container.querySelector('[role="alert"]');
		expect(alert).toBeTruthy();
	});

	it('has aria-live="assertive" attribute', () => {
		const { container } = render(HealthAlarm, {
			props: { health: 'offline', sourceLabel: 'CAM 1' },
		});
		const alert = container.querySelector('[aria-live="assertive"]');
		expect(alert).toBeTruthy();
	});

	it('formats the full alarm message correctly', () => {
		const { container } = render(HealthAlarm, {
			props: { health: 'no_signal', sourceLabel: 'CAM 2' },
		});
		expect(container.textContent).toContain('PROGRAM: CAM 2');
		expect(container.textContent).toContain('NO SIGNAL');
	});
});
