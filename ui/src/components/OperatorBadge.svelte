<script lang="ts">
	import type { ControlRoomState, OperatorRole } from '$lib/api/types';
	import { getSession } from '$lib/state/operator.svelte';

	let { state: crState }: { state: ControlRoomState } = $props();

	let expanded = $state(false);
	let wrapperEl = $state<HTMLDivElement>();

	const session = $derived(getSession());
	const operators = $derived(crState.operators ?? []);
	const connectedCount = $derived(operators.filter(o => o.connected).length);

	const roleIcons: Record<OperatorRole, string> = {
		director: 'D',
		audio: 'A',
		graphics: 'G',
		viewer: 'V',
	};

	const roleColors: Record<OperatorRole, string> = {
		director: '#ef4444',
		audio: '#3b82f6',
		graphics: '#a855f7',
		viewer: '#6b7280',
	};
</script>

<svelte:window onclick={(e) => {
	if (expanded && wrapperEl && !(e.target instanceof Node && wrapperEl.contains(e.target))) {
		expanded = false;
	}
}} />

{#if session}
	<div class="badge-wrapper" bind:this={wrapperEl}>
		<button
			class="badge"
			onclick={() => { expanded = !expanded; }}
			aria-expanded={expanded}
			aria-label="Operator: {session.name}"
		>
			<span class="role-icon" style="background: {roleColors[session.role]}">{roleIcons[session.role]}</span>
			<span class="name">{session.name}</span>
			{#if connectedCount > 1}
				<span class="count">{connectedCount}</span>
			{/if}
		</button>

		{#if expanded}
			<div class="dropdown" role="menu">
				<div class="dropdown-header">Connected Operators</div>
				{#each operators.filter(o => o.connected) as op}
					<div class="op-row" class:self={op.id === session.id}>
						<span class="op-icon" style="background: {roleColors[op.role as OperatorRole]}">{roleIcons[op.role as OperatorRole]}</span>
						<span class="op-name">{op.name}</span>
						<span class="op-role">{op.role}</span>
					</div>
				{/each}
				{#if operators.filter(o => !o.connected).length > 0}
					<div class="dropdown-header">Offline</div>
					{#each operators.filter(o => !o.connected) as op}
						<div class="op-row offline">
							<span class="op-icon" style="background: #444">{roleIcons[op.role as OperatorRole]}</span>
							<span class="op-name">{op.name}</span>
							<span class="op-role">{op.role}</span>
						</div>
					{/each}
				{/if}
			</div>
		{/if}
	</div>
{/if}

<style>
	.badge-wrapper {
		position: relative;
	}

	.badge {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 8px;
		border: 1px solid var(--border-subtle, #444);
		border-radius: 4px;
		background: var(--bg-base, #111);
		color: var(--text-primary, #eee);
		font-size: 12px;
		cursor: pointer;
	}

	.badge:hover {
		border-color: var(--text-secondary, #888);
	}

	.role-icon, .op-icon {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 18px;
		height: 18px;
		border-radius: 3px;
		font-size: 10px;
		font-weight: 700;
		color: #fff;
		flex-shrink: 0;
	}

	.name {
		max-width: 100px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.count {
		background: var(--border-subtle, #555);
		border-radius: 8px;
		padding: 1px 5px;
		font-size: 10px;
		color: var(--text-secondary, #ccc);
	}

	.dropdown {
		position: absolute;
		top: calc(100% + 4px);
		right: 0;
		background: var(--bg-surface, #1e1e1e);
		border: 1px solid var(--border-subtle, #444);
		border-radius: 6px;
		min-width: 200px;
		z-index: 100;
		overflow: hidden;
	}

	.dropdown-header {
		padding: 6px 10px;
		font-size: 10px;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		color: var(--text-secondary, #888);
		border-bottom: 1px solid var(--border-subtle, #333);
	}

	.op-row {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 6px 10px;
		font-size: 12px;
	}

	.op-row.self {
		background: rgba(37, 99, 235, 0.1);
	}

	.op-row.offline {
		opacity: 0.5;
	}

	.op-name {
		flex: 1;
		color: var(--text-primary, #eee);
	}

	.op-role {
		color: var(--text-secondary, #888);
		font-size: 11px;
	}
</style>
