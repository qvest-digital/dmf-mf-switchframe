import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import EngineRoleBadge from './EngineRoleBadge.svelte';

describe('EngineRoleBadge', () => {
	it('renders nothing for standalone role', () => {
		const { queryByTestId } = render(EngineRoleBadge, {
			props: { engineLabel: '', role: 'standalone' },
		});
		expect(queryByTestId('engine-role-badge')).toBeNull();
	});

	it('renders nothing when engine label is absent', () => {
		const { queryByTestId } = render(EngineRoleBadge, {
			props: { role: 'leader' },
		});
		expect(queryByTestId('engine-role-badge')).toBeNull();
	});

	it('shows "A · LEADER" for leader role', () => {
		const { getByTestId } = render(EngineRoleBadge, {
			props: { engineLabel: 'a', role: 'leader' },
		});
		const badge = getByTestId('engine-role-badge');
		expect(badge.textContent).toContain('A · LEADER');
	});

	it('shows "B · FOLLOWER" for follower-healthy role', () => {
		const { getByTestId } = render(EngineRoleBadge, {
			props: { engineLabel: 'b', role: 'follower-healthy' },
		});
		expect(getByTestId('engine-role-badge').textContent).toContain('B · FOLLOWER');
	});

	it('shows "A · PARTITIONED" and carries the error CSS class for follower-partitioned role', () => {
		const { getByTestId } = render(EngineRoleBadge, {
			props: { engineLabel: 'a', role: 'follower-partitioned' },
		});
		const badge = getByTestId('engine-role-badge');
		expect(badge.textContent).toContain('A · PARTITIONED');
		expect(badge.className).toContain('role-follower-partitioned');
	});

	it('sets a tooltip that names the 409 consequence for partitioned engines', () => {
		const { getByTestId } = render(EngineRoleBadge, {
			props: { engineLabel: 'a', role: 'follower-partitioned' },
		});
		const title = getByTestId('engine-role-badge').getAttribute('title') ?? '';
		expect(title).toContain('409');
	});
});
