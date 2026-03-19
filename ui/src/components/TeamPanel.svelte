<script lang="ts">
	import { onMount } from 'svelte';
	import type { ControlRoomState, OperatorRole, OperatorInfo } from '$lib/api/types';
	import { operatorInviteTokens } from '$lib/api/switch-api';
	import { getSession } from '$lib/state/operator.svelte';
	import { notify } from '$lib/state/notifications.svelte';

	let { state: crState }: { state: ControlRoomState } = $props();

	const session = $derived(getSession());
	const isDirector = $derived(session?.role === 'director');
	const operators = $derived(crState.operators ?? []);
	const connectedOps = $derived(operators.filter((o) => o.connected));
	const offlineOps = $derived(operators.filter((o) => !o.connected));

	let inviteTokens = $state<Record<string, string>>({});
	let copiedRole = $state<string | null>(null);

	const roleLabels: Record<string, string> = {
		director: 'Director',
		audio: 'Audio',
		graphics: 'Graphics',
		captioner: 'Captioner',
		viewer: 'Viewer',
	};

	const roleLetters: Record<string, string> = {
		director: 'd',
		audio: 'a',
		graphics: 'g',
		captioner: 'c',
		viewer: 'v',
	};

	const roleColors: Record<OperatorRole, string> = {
		director: '#ef4444',
		audio: '#3b82f6',
		graphics: '#a855f7',
		captioner: '#eab308',
		viewer: '#6b7280',
	};

	const roleIcons: Record<OperatorRole, string> = {
		director: 'D',
		audio: 'A',
		graphics: 'G',
		captioner: 'C',
		viewer: 'V',
	};

	const inviteRoles = ['director', 'audio', 'graphics', 'captioner', 'viewer'];

	async function loadInviteTokens(): Promise<void> {
		try {
			inviteTokens = await operatorInviteTokens();
		} catch {
			// Non-director or no tokens configured — silently ignore
		}
	}

	async function copyInviteLink(role: string): Promise<void> {
		const token = inviteTokens[role];
		if (!token) return;
		const letter = roleLetters[role] || 'v';
		const url = `${window.location.origin}/join/${letter}/${token}`;
		try {
			await navigator.clipboard.writeText(url);
			copiedRole = role;
			setTimeout(() => {
				if (copiedRole === role) copiedRole = null;
			}, 2000);
		} catch {
			notify('error', 'Failed to copy to clipboard');
		}
	}

	onMount(() => {
		if (isDirector) {
			loadInviteTokens();
		}
	});

	// Reload tokens if role changes to director
	$effect(() => {
		if (isDirector && Object.keys(inviteTokens).length === 0) {
			loadInviteTokens();
		}
	});
</script>

<div class="team-panel">
	<div class="section">
		<div class="section-header">Connected ({connectedOps.length})</div>
		{#if connectedOps.length === 0}
			<div class="empty">No operators connected</div>
		{:else}
			{#each connectedOps as op (op.id)}
				<div class="operator-row" class:self={op.id === session?.id}>
					<span class="role-icon" style="background: {roleColors[op.role as OperatorRole]}">{roleIcons[op.role as OperatorRole]}</span>
					<span class="op-name">{op.name}</span>
					<span class="op-role">{op.role}</span>
				</div>
			{/each}
		{/if}
	</div>

	{#if offlineOps.length > 0}
		<div class="section">
			<div class="section-header">Offline ({offlineOps.length})</div>
			{#each offlineOps as op (op.id)}
				<div class="operator-row offline">
					<span class="role-icon" style="background: #444">{roleIcons[op.role as OperatorRole]}</span>
					<span class="op-name">{op.name}</span>
					<span class="op-role">{op.role}</span>
				</div>
			{/each}
		</div>
	{/if}

	{#if isDirector && Object.keys(inviteTokens).length > 0}
		<div class="section">
			<div class="section-header">Invite Links</div>
			{#each inviteRoles as role}
				{#if inviteTokens[role]}
					<div class="invite-row">
						<span class="invite-role">{roleLabels[role]}</span>
						<button class="copy-btn" onclick={() => copyInviteLink(role)}>
							{copiedRole === role ? 'Copied' : 'Copy Link'}
						</button>
					</div>
				{/if}
			{/each}
		</div>
	{/if}
</div>

<style>
	.team-panel {
		padding: 8px 12px;
		display: flex;
		flex-direction: column;
		gap: 12px;
		height: 100%;
		overflow-y: auto;
	}

	.section-header {
		font-size: var(--section-header-size);
		font-weight: var(--section-header-weight);
		letter-spacing: var(--section-header-tracking);
		color: var(--section-header-color);
		text-transform: uppercase;
		padding: 0 0 4px;
		border-bottom: 1px solid var(--border-subtle);
		margin-bottom: 4px;
	}

	.empty {
		color: var(--text-tertiary);
		font-size: var(--text-sm);
		padding: 4px 0;
	}

	.operator-row {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 0;
		font-size: var(--text-sm);
	}

	.operator-row.self {
		background: rgba(37, 99, 235, 0.08);
		margin: 0 -4px;
		padding: 4px;
		border-radius: var(--radius-sm);
	}

	.operator-row.offline {
		opacity: 0.5;
	}

	.role-icon {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 18px;
		height: 18px;
		border-radius: 3px;
		font-size: var(--text-xs);
		font-weight: 700;
		color: #fff;
		flex-shrink: 0;
	}

	.op-name {
		flex: 1;
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.op-role {
		color: var(--text-secondary);
		font-size: var(--text-xs);
	}

	.invite-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 3px 0;
	}

	.invite-role {
		font-size: var(--text-sm);
		color: var(--text-primary);
	}

	.copy-btn {
		padding: 3px 10px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-control);
		color: var(--text-secondary);
		font-size: var(--text-xs);
		font-family: var(--font-ui);
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
		min-width: 72px;
	}

	.copy-btn:hover {
		background: var(--bg-hover);
		border-color: var(--border-strong);
		color: var(--text-primary);
	}
</style>
