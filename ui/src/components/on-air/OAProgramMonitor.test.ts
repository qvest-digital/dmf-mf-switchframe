import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OAProgramMonitor from './OAProgramMonitor.svelte';

function defaultProps(overrides: Record<string, unknown> = {}) {
	return {
		programSource: 'SRT Feed 1',
		...overrides,
	};
}

describe('OAProgramMonitor', () => {
	it('renders a canvas element', () => {
		const { container } = render(OAProgramMonitor, { props: defaultProps() });
		const canvas = container.querySelector('canvas');
		expect(canvas).toBeTruthy();
	});

	it('canvas has 1920x1080 attributes', () => {
		const { container } = render(OAProgramMonitor, { props: defaultProps() });
		const canvas = container.querySelector('canvas');
		expect(canvas?.getAttribute('width')).toBe('1920');
		expect(canvas?.getAttribute('height')).toBe('1080');
	});

	it('shows program source label', () => {
		render(OAProgramMonitor, { props: defaultProps() });
		expect(screen.getByText('SRT Feed 1')).toBeTruthy();
	});

	it('shows updated source label', () => {
		render(OAProgramMonitor, { props: defaultProps({ programSource: 'Camera 3' }) });
		expect(screen.getByText('Camera 3')).toBeTruthy();
	});

	it('engine toggle shown in dual-engine mode', () => {
		const { container } = render(OAProgramMonitor, {
			props: defaultProps({
				isDualEngine: true,
				activeEngine: 'A',
				authoritativeEngine: 'A',
			}),
		});
		const toggle = container.querySelector('.engine-toggle');
		expect(toggle).toBeTruthy();
	});

	it('engine toggle hidden in single-engine mode', () => {
		const { container } = render(OAProgramMonitor, {
			props: defaultProps({ isDualEngine: false }),
		});
		const toggle = container.querySelector('.engine-toggle');
		expect(toggle).toBeFalsy();
	});

	it('authority star shown on correct engine (A)', () => {
		render(OAProgramMonitor, {
			props: defaultProps({
				isDualEngine: true,
				activeEngine: 'A',
				authoritativeEngine: 'A',
			}),
		});
		const btnA = screen.getByRole('button', { name: /A/ });
		const btnB = screen.getByRole('button', { name: /B/ });
		expect(btnA.textContent).toContain('\u2605');
		expect(btnB.textContent).not.toContain('\u2605');
	});

	it('authority star shown on correct engine (B)', () => {
		render(OAProgramMonitor, {
			props: defaultProps({
				isDualEngine: true,
				activeEngine: 'B',
				authoritativeEngine: 'B',
			}),
		});
		const btnA = screen.getByRole('button', { name: /A/ });
		const btnB = screen.getByRole('button', { name: /B/ });
		expect(btnB.textContent).toContain('\u2605');
		expect(btnA.textContent).not.toContain('\u2605');
	});

	it('engine toggle callback fires with correct engine', async () => {
		const fn = vi.fn();
		render(OAProgramMonitor, {
			props: defaultProps({
				isDualEngine: true,
				activeEngine: 'A',
				authoritativeEngine: 'A',
				onEngineToggle: fn,
			}),
		});
		const btnB = screen.getByRole('button', { name: /B/ });
		await fireEvent.click(btnB);
		expect(fn).toHaveBeenCalledOnce();
		expect(fn).toHaveBeenCalledWith('B');
	});

	it('calls onCanvasReady when mounted', () => {
		const fn = vi.fn();
		render(OAProgramMonitor, {
			props: defaultProps({ onCanvasReady: fn }),
		});
		expect(fn).toHaveBeenCalledOnce();
		expect(fn.mock.calls[0][0]).toBeInstanceOf(HTMLCanvasElement);
	});

	it('has red tally border on canvas', () => {
		const { container } = render(OAProgramMonitor, { props: defaultProps() });
		const canvas = container.querySelector('.oa-program-canvas');
		expect(canvas).toBeTruthy();
	});

	it('active engine button has active class', () => {
		const { container } = render(OAProgramMonitor, {
			props: defaultProps({
				isDualEngine: true,
				activeEngine: 'B',
				authoritativeEngine: 'A',
			}),
		});
		const buttons = container.querySelectorAll('.engine-btn');
		expect(buttons[0].classList.contains('active')).toBe(false);
		expect(buttons[1].classList.contains('active')).toBe(true);
	});
});
