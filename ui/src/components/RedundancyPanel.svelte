<script lang="ts">
	import type { SyncHealth, SyncStatus } from '$lib/state/reconciler.svelte';

	interface Props {
		health: SyncHealth;
		visible: boolean;
		onclose: () => void;
		onforcereconcile?: () => void;
	}

	let { health, visible, onclose, onforcereconcile }: Props = $props();

	function fmtRelativeTime(ts: number): string {
		if (ts === 0) return 'never';
		const elapsed = Date.now() - ts;
		if (elapsed < 1000) return 'just now';
		if (elapsed < 60_000) return `${Math.floor(elapsed / 1000)}s ago`;
		if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)}m ago`;
		return `${Math.floor(elapsed / 3_600_000)}h ago`;
	}

	const statusLabel = $derived.by((): string => {
		const map: Record<SyncStatus, string> = {
			nominal: 'NOMINAL',
			lagging: 'LAGGING',
			diverged: 'DIVERGED',
			'single-engine': 'SINGLE ENGINE',
			disconnected: 'NO ENGINES',
		};
		return map[health.status];
	});

	const statusClass = $derived.by((): string => {
		const map: Record<SyncStatus, string> = {
			nominal: 'badge-nominal',
			lagging: 'badge-lagging',
			diverged: 'badge-error',
			'single-engine': 'badge-muted',
			disconnected: 'badge-error',
		};
		return map[health.status];
	});
</script>

<div class="redundancy-panel" class:visible role="complementary" aria-label="Redundancy status">
	<div class="panel-header">
		<span class="panel-title">REDUNDANCY</span>
		<button class="close-btn" onclick={onclose} aria-label="Close redundancy panel">&times;</button>
	</div>

	<div class="panel-body">
		<!-- Sync status badge -->
		<section class="section">
			<div class="section-label">SYNC STATUS</div>
			<div class="status-badge {statusClass}">{statusLabel}</div>
		</section>

		<!-- Per-engine health table -->
		<section class="section">
			<div class="section-label">ENGINE HEALTH</div>
			<table class="engine-table">
				<thead>
					<tr>
						<th></th>
						<th>Connected</th>
						<th>Last Seen</th>
						<th>Cmd Seq</th>
						<th>RTT</th>
					</tr>
				</thead>
				<tbody>
					<tr class:authoritative={health.authoritative === 'A'}>
						<td class="engine-name">
							{#if health.authoritative === 'A'}
								<span class="auth-star" title="Authoritative">&#9733;</span>
							{/if}
							Engine A
						</td>
						<td>
							<span class="conn-dot" class:connected={health.engineA.connected}></span>
						</td>
						<td class="mono">{fmtRelativeTime(health.engineA.lastSeen)}</td>
						<td class="mono">{health.engineA.lastCommandSeq}</td>
						<td class="mono muted">—</td>
					</tr>
					<tr class:authoritative={health.authoritative === 'B'}>
						<td class="engine-name">
							{#if health.authoritative === 'B'}
								<span class="auth-star" title="Authoritative">&#9733;</span>
							{/if}
							Engine B
						</td>
						<td>
							<span class="conn-dot" class:connected={health.engineB.connected}></span>
						</td>
						<td class="mono">{fmtRelativeTime(health.engineB.lastSeen)}</td>
						<td class="mono">{health.engineB.lastCommandSeq}</td>
						<td class="mono muted">—</td>
					</tr>
				</tbody>
			</table>
		</section>

		<!-- Divergences -->
		<section class="section">
			<div class="section-label">DIVERGENCES</div>
			{#if health.divergences.length === 0}
				<div class="empty-msg">No divergences detected</div>
			{:else}
				<ul class="divergence-list">
					{#each health.divergences as div}
						<li class="divergence-item">
							<span class="div-field">{div.field}</span>
							<span class="div-values">
								<span class="div-engine">A=</span><span class="div-val">{JSON.stringify(div.engineA)}</span>
								<span class="div-sep"> / </span>
								<span class="div-engine">B=</span><span class="div-val">{JSON.stringify(div.engineB)}</span>
							</span>
						</li>
					{/each}
				</ul>
			{/if}
		</section>

		<!-- Force reconcile -->
		<section class="section section-action">
			<button
				class="reconcile-btn"
				onclick={() => onforcereconcile?.()}
				title="Force state reconciliation from authoritative engine"
			>
				FORCE RECONCILE
			</button>
			<div class="action-hint">Re-synchronizes standby engine from authoritative state</div>
		</section>
	</div>
</div>

<style>
	.redundancy-panel {
		position: fixed;
		top: 0;
		right: 0;
		bottom: 0;
		width: 340px;
		background: rgba(9, 9, 11, 0.96);
		border-left: 1px solid var(--border-subtle);
		z-index: var(--z-fullscreen);
		transform: translateX(100%);
		transition: transform 200ms ease;
		display: flex;
		flex-direction: column;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--text-secondary);
		overflow: hidden;
	}

	.redundancy-panel.visible {
		transform: translateX(0);
	}

	/* Header */
	.panel-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 10px 16px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}

	.panel-title {
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: var(--text-sm);
		color: var(--text-primary);
		letter-spacing: 0.5px;
	}

	.close-btn {
		background: none;
		border: none;
		color: var(--text-muted, var(--text-secondary));
		cursor: pointer;
		font-size: 18px;
		line-height: 1;
		padding: 2px 4px;
		border-radius: var(--radius-sm);
		transition: color var(--transition-fast);
	}

	.close-btn:hover {
		color: var(--text-primary);
	}

	/* Body */
	.panel-body {
		flex: 1;
		overflow-y: auto;
		padding: 12px 16px;
		display: flex;
		flex-direction: column;
		gap: 20px;
	}

	/* Section */
	.section {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.section-label {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 700;
		letter-spacing: 0.1em;
		color: var(--text-muted, var(--text-secondary));
		text-transform: uppercase;
	}

	/* Status badge */
	.status-badge {
		display: inline-flex;
		align-items: center;
		padding: 4px 12px;
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: 13px;
		font-weight: 700;
		letter-spacing: 0.08em;
		border: 1px solid transparent;
	}

	.badge-nominal {
		background: rgba(22, 163, 74, 0.15);
		border-color: var(--tally-preview-medium);
		color: var(--tally-preview);
	}

	.badge-lagging {
		background: var(--accent-yellow-dim);
		border-color: rgba(234, 179, 8, 0.3);
		color: var(--accent-yellow);
	}

	.badge-error {
		background: var(--tally-program-dim);
		border-color: var(--tally-program-medium);
		color: var(--tally-program);
	}

	.badge-muted {
		background: rgba(113, 113, 122, 0.15);
		border-color: rgba(113, 113, 122, 0.3);
		color: var(--text-muted, #71717a);
	}

	/* Engine table */
	.engine-table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-xs);
	}

	.engine-table th {
		text-align: left;
		color: var(--text-muted, var(--text-secondary));
		font-weight: 600;
		padding: 3px 6px 3px 0;
		border-bottom: 1px solid var(--border-subtle);
		white-space: nowrap;
	}

	.engine-table td {
		padding: 5px 6px 5px 0;
		vertical-align: middle;
		border-bottom: 1px solid rgba(255,255,255,0.04);
		white-space: nowrap;
	}

	.engine-table tr.authoritative td {
		color: var(--text-primary);
	}

	.engine-name {
		font-family: var(--font-ui);
		font-weight: 600;
		color: var(--text-secondary);
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.auth-star {
		color: var(--accent-yellow, #eab308);
		font-size: 10px;
	}

	.conn-dot {
		display: inline-block;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--tally-program, #dc2626);
	}

	.conn-dot.connected {
		background: var(--tally-preview, #16a34a);
	}

	.mono {
		font-family: var(--font-mono);
	}

	.muted {
		color: var(--text-muted, var(--text-secondary));
		opacity: 0.5;
	}

	/* Divergences */
	.empty-msg {
		font-size: var(--text-xs);
		color: var(--text-muted, var(--text-secondary));
		opacity: 0.6;
		font-style: italic;
	}

	.divergence-list {
		list-style: none;
		padding: 0;
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.divergence-item {
		background: rgba(220, 38, 38, 0.08);
		border: 1px solid rgba(220, 38, 38, 0.2);
		border-radius: var(--radius-sm);
		padding: 6px 8px;
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.div-field {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 700;
		color: var(--tally-program, #dc2626);
	}

	.div-values {
		font-size: var(--text-xs);
		color: var(--text-secondary);
		word-break: break-all;
	}

	.div-engine {
		color: var(--text-muted, var(--text-secondary));
		opacity: 0.7;
	}

	.div-val {
		font-family: var(--font-mono);
	}

	.div-sep {
		opacity: 0.4;
	}

	/* Action section */
	.section-action {
		margin-top: auto;
		padding-top: 8px;
		border-top: 1px solid var(--border-subtle);
	}

	.reconcile-btn {
		width: 100%;
		padding: 8px 12px;
		border: 1px solid rgba(234, 179, 8, 0.4);
		border-radius: var(--radius-sm);
		background: rgba(234, 179, 8, 0.1);
		color: var(--accent-yellow, #eab308);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 700;
		font-size: var(--text-sm);
		letter-spacing: 0.06em;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast);
	}

	.reconcile-btn:hover {
		background: rgba(234, 179, 8, 0.2);
		border-color: rgba(234, 179, 8, 0.6);
	}

	.action-hint {
		font-size: 10px;
		color: var(--text-muted, var(--text-secondary));
		opacity: 0.55;
		text-align: center;
		font-family: var(--font-ui);
	}
</style>
