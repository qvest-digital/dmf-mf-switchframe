import { describe, it, expect, vi } from 'vitest';
import { createPrismConnection } from './connection';

describe('PrismConnection', () => {
	it('should create connection with correct config', () => {
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: vi.fn(),
		});
		expect(conn).toBeDefined();
		expect(conn.state).toBe('disconnected');
	});

	it('should call onControlState when control data arrives', () => {
		const onControlState = vi.fn();
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState,
			onConnectionChange: vi.fn(),
		});

		// Simulate control track data
		const state = { seq: 1, programSource: 'cam1' };
		const data = new TextEncoder().encode(JSON.stringify(state));
		conn._handleControlData(data);

		expect(onControlState).toHaveBeenCalledWith(data);
	});

	it('should track connection state', () => {
		const states: string[] = [];
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: (s) => states.push(s),
		});

		conn._setConnectionState('connecting');
		conn._setConnectionState('connected');
		expect(states).toEqual(['connecting', 'connected']);
	});

	it('should expose current state via getter', () => {
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: vi.fn(),
		});

		expect(conn.state).toBe('disconnected');
		conn._setConnectionState('connecting');
		expect(conn.state).toBe('connecting');
		conn._setConnectionState('connected');
		expect(conn.state).toBe('connected');
		conn._setConnectionState('error');
		expect(conn.state).toBe('error');
	});

	it('should not call onConnectionChange for same state', () => {
		const onConnectionChange = vi.fn();
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange,
		});

		conn._setConnectionState('connecting');
		conn._setConnectionState('connecting');
		expect(onConnectionChange).toHaveBeenCalledTimes(1);
	});

	it('should disconnect and reset state', () => {
		const states: string[] = [];
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: (s) => states.push(s),
		});

		conn._setConnectionState('connected');
		conn.disconnect();
		expect(conn.state).toBe('disconnected');
		expect(states).toContain('disconnected');
	});
});
