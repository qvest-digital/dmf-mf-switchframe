<script lang="ts">
	import type { ControlRoomState, OperatorRole } from '$lib/api/types';
	import { operatorInviteTokens } from '$lib/api/switch-api';
	import { getSession } from '$lib/state/operator.svelte';
	import { notify } from '$lib/state/notifications.svelte';
	import {
		ICON_CHECK,
		ICON_COPY,
		ICON_LINK,
		ICON_USER,
		ICON_USERS,
		renderIcon,
	} from '$lib/icons';

	let { state: crState }: { state: ControlRoomState } = $props();

	const session = $derived(getSession());
	const isDirector = $derived(session?.role === 'director');
	const operators = $derived(crState.operators ?? []);
	const connectedOps = $derived(operators.filter((o) => o.connected));
	const offlineOps = $derived(operators.filter((o) => !o.connected));

	let inviteTokens = $state<Record<string, string>>({});
	let copiedRole = $state<string | null>(null);

	const roleLabels: Record<OperatorRole, string> = {
		director: 'Director',
		audio: 'Audio',
		graphics: 'Graphics',
		captioner: 'Captioner',
		viewer: 'Viewer',
	};

	const roleLetters: Record<OperatorRole, string> = {
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

	const inviteRoles: OperatorRole[] = ['director', 'audio', 'graphics', 'captioner', 'viewer'];

	const hasInviteLinks = $derived(isDirector && Object.keys(inviteTokens).length > 0);

	function labelForRole(role: string): string {
		return roleLabels[role as OperatorRole] ?? role;
	}

	function colorForRole(role: string): string {
		return roleColors[role as OperatorRole] ?? roleColors.viewer;
	}

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
		const letter = roleLetters[role as OperatorRole] ?? 'v';
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

	// Fetched once per director session. An engine with no invite tokens
	// answers with an empty map, so keying the fetch on the map being empty
	// refetched on every answer, without end.
	let loadedFor: string | null = null;
	$effect(() => {
		const id = isDirector ? session?.id ?? null : null;
		if (id && id !== loadedFor) {
			loadedFor = id;
			inviteTokens = {};
			loadInviteTokens();
		}
	});
</script>

<div class="team-panel">
	<div class="team-col team-col--operators">
		<div class="col-chrome">
			<div class="section-heading col-heading">
				<span class="col-heading-icon" aria-hidden="true">{@html renderIcon(ICON_USERS, 13)}</span>
				<span>Operators</span>
			</div>
		</div>
		<div class="team-col-scroll">
			{#if operators.length === 0}
				<div class="empty-block">
					<span class="empty-icon" aria-hidden="true">{@html renderIcon(ICON_USER, 13)}</span>
					<span class="empty-text">No operators connected</span>
				</div>
			{:else}
				<div class="subsection">
					<div class="subsection-header">Connected ({connectedOps.length})</div>
					{#if connectedOps.length === 0}
						<div class="empty-inline">No operators connected</div>
					{:else}
						{#each connectedOps as op (op.id)}
							<div class="operator-row" class:self={op.id === session?.id}>
								<span
									class="role-badge"
									style="background: {colorForRole(op.role)}"
									title={labelForRole(op.role)}
								>{roleIcons[op.role as OperatorRole]}</span>
								<span class="status-dot status-dot--online" title="Online"></span>
								<span class="op-name">{op.name}</span>
								<span class="op-role">{labelForRole(op.role)}</span>
							</div>
						{/each}
					{/if}
				</div>
				{#if offlineOps.length > 0}
					<div class="subsection">
						<div class="subsection-header">Offline ({offlineOps.length})</div>
						{#each offlineOps as op (op.id)}
							<div class="operator-row operator-row--offline">
								<span
									class="role-badge"
									style="background: {colorForRole(op.role)}"
									title={labelForRole(op.role)}
								>{roleIcons[op.role as OperatorRole]}</span>
								<span class="status-dot status-dot--offline" title="Offline"></span>
								<span class="op-name">{op.name}</span>
								<span class="op-role">{labelForRole(op.role)}</span>
							</div>
						{/each}
					</div>
				{/if}
			{/if}
		</div>
	</div>

	<div class="team-col team-col--invite">
		<div class="col-chrome">
			<div class="section-heading col-heading">
				<span class="col-heading-icon" aria-hidden="true">{@html renderIcon(ICON_LINK, 13)}</span>
				<span>Invite links</span>
			</div>
		</div>
		<div class="team-col-scroll">
			{#if hasInviteLinks}
				<div class="invite-list">
					{#each inviteRoles as role}
						{#if inviteTokens[role]}
							<div class="invite-row">
								<span
									class="role-badge"
									style="background: {roleColors[role]}"
									title={roleLabels[role]}
								>{roleIcons[role]}</span>
								<span class="invite-label">{roleLabels[role]}</span>
								<button
									type="button"
									class="copy-icon-btn"
									class:copy-icon-btn--done={copiedRole === role}
									onclick={() => copyInviteLink(role)}
									aria-label="Copy invite link for {roleLabels[role]}"
									title="Copy link"
								>
									{#if copiedRole === role}
										<span class="copy-icon-svg copy-icon-svg--check" aria-hidden="true"
											>{@html renderIcon(ICON_CHECK, 13)}</span
										>
									{:else}
										<span class="copy-icon-svg" aria-hidden="true"
											>{@html renderIcon(ICON_COPY, 13)}</span
										>
									{/if}
								</button>
							</div>
						{/if}
					{/each}
				</div>
			{:else if isDirector}
				<div class="empty-block empty-block--muted">
					<span class="empty-text">No invite tokens available</span>
				</div>
			{:else}
				<div class="empty-block empty-block--muted">
					<span class="empty-text">Visible to director</span>
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.team-panel {
		display: flex;
		flex: 1;
		height: 100%;
		min-height: 0;
		overflow: hidden;
		gap: 0;
		padding: 6px 10px;
		font-family: var(--font-ui);
	}

	.team-col {
		flex: 1 1 0;
		min-width: 0;
		min-height: 0;
		display: flex;
		flex-direction: column;
	}

	.team-col--operators {
		border-right: 1px solid var(--border-subtle);
		padding-right: 10px;
	}

	.team-col--invite {
		padding-left: 10px;
	}

	.col-chrome {
		flex-shrink: 0;
	}

	.team-col-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		padding-top: 4px;
	}

	.section-heading,
	.subsection-header {
		font-size: var(--text-2xs);
		font-weight: 600;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-secondary);
		border-bottom: 1px solid var(--border-subtle);
		padding-bottom: 4px;
		margin-bottom: 6px;
	}

	.col-heading {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 0;
		border-bottom: 1px solid var(--border-default);
		padding-bottom: 6px;
	}

	.col-heading-icon {
		display: inline-flex;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.col-heading-icon :global(svg) {
		display: block;
	}

	.subsection {
		margin-bottom: 10px;
	}

	.subsection:last-child {
		margin-bottom: 0;
	}

	.invite-list {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.subsection-header {
		margin-bottom: 4px;
	}

	.empty-block {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 8px 2px;
		color: var(--text-tertiary);
		font-size: var(--text-xs);
	}

	.empty-block--muted {
		padding-top: 4px;
	}

	.empty-icon {
		display: inline-flex;
		opacity: 0.65;
		flex-shrink: 0;
	}

	.empty-icon :global(svg) {
		display: block;
	}

	.empty-text {
		line-height: 1.3;
	}

	.empty-inline {
		color: var(--text-tertiary);
		font-size: var(--text-xs);
		padding: 2px 0 4px;
		line-height: 1.3;
	}

	.operator-row {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 3px 4px;
		font-size: var(--text-xs);
		border-radius: var(--radius-xs);
		min-height: 22px;
	}

	.operator-row.self {
		background: var(--bg-elevated);
		box-shadow: inset 0 0 0 1px rgba(37, 99, 235, 0.25);
	}

	.operator-row--offline {
		opacity: 0.72;
	}

	.role-badge {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 16px;
		height: 16px;
		border-radius: var(--radius-xs);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: #fff;
		flex-shrink: 0;
		line-height: 1;
	}

	.status-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.status-dot--online {
		background: #22c55e;
		box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.35);
	}

	.status-dot--offline {
		background: var(--text-tertiary);
		opacity: 0.85;
	}

	.op-name {
		flex: 1;
		min-width: 0;
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-weight: 500;
	}

	.op-role {
		flex-shrink: 0;
		color: var(--text-tertiary);
		font-size: var(--text-2xs);
		max-width: 52%;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		text-align: right;
	}

	.invite-row {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 3px 2px;
		min-height: 24px;
		border-radius: var(--radius-xs);
	}

	.invite-row:hover {
		background: var(--bg-hover);
	}

	.invite-label {
		flex: 1;
		min-width: 0;
		font-size: var(--text-xs);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.copy-icon-btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 26px;
		height: 26px;
		padding: 0;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		color: var(--text-secondary);
		cursor: pointer;
		flex-shrink: 0;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast),
			color var(--transition-fast);
	}

	.copy-icon-btn:hover {
		background: var(--bg-hover);
		border-color: var(--border-default);
		color: var(--text-primary);
	}

	.copy-icon-btn--done {
		color: #22c55e;
		border-color: rgba(34, 197, 94, 0.45);
		background: var(--bg-elevated);
	}

	.copy-icon-svg {
		display: inline-flex;
		pointer-events: none;
	}

	.copy-icon-svg :global(svg) {
		display: block;
	}

	.copy-icon-svg--check :global(svg) {
		stroke: currentColor;
	}
</style>
