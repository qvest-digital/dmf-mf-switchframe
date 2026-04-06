<script lang="ts">
	import type { SyncHealth, SyncStatus } from '$lib/state/reconciler.svelte';

	interface Props {
		health: SyncHealth | null;
		onclick?: () => void;
	}

	let { health, onclick }: Props = $props();

	const label = $derived.by((): string => {
		if (!health) return '';
		const map: Record<SyncStatus, string> = {
			synced: 'SYNCED',
			lagging: 'LAGGING',
			diverged: 'DIVERGED',
			mismatch: 'MISMATCH',
			recovering: 'RECOVERING',
			'single-engine': '1 ENGINE',
			disconnected: 'OFFLINE',
		};
		return map[health.status];
	});

	const cssClass = $derived.by((): string => {
		if (!health) return '';
		const map: Record<SyncStatus, string> = {
			synced: 'sync-synced',
			lagging: 'sync-lagging',
			diverged: 'sync-diverged',
			mismatch: 'sync-mismatch',
			recovering: 'sync-recovering',
			'single-engine': 'sync-single',
			disconnected: 'sync-disconnected',
		};
		return map[health.status];
	});
</script>

{#if health}
	<button class="sync-indicator {cssClass}" {onclick} aria-live="polite" title="Dual-engine sync status — click for details">
		<span class="dot"></span>
		{label}
	</button>
{/if}

<style>
	.sync-indicator {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		padding: 2px 7px;
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.06em;
		line-height: 1;
		user-select: none;
		cursor: pointer;
		border: 1px solid transparent;
		background: none;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast),
			color var(--transition-fast);
	}

	.sync-indicator:hover {
		filter: brightness(1.2);
	}

	.dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	/* synced — green */
	.sync-synced {
		background: rgba(22, 163, 74, 0.15);
		border-color: var(--tally-preview-medium);
		color: var(--tally-preview);
	}
	.sync-synced .dot {
		background: var(--tally-preview);
	}

	/* lagging — amber */
	.sync-lagging {
		background: var(--accent-yellow-dim);
		border-color: rgba(234, 179, 8, 0.3);
		color: var(--accent-yellow);
	}
	.sync-lagging .dot {
		background: var(--accent-yellow);
	}

	/* diverged — red, static (no animation) */
	.sync-diverged {
		background: var(--tally-program-dim);
		border-color: var(--tally-program-medium);
		color: var(--tally-program);
	}
	.sync-diverged .dot {
		background: var(--tally-program);
	}

	/* mismatch — red, fast pulse (critical) */
	.sync-mismatch {
		background: var(--tally-program-dim);
		border-color: var(--tally-program-medium);
		color: var(--tally-program);
		animation: pulse-error 0.6s ease-in-out infinite;
	}
	.sync-mismatch .dot {
		background: var(--tally-program);
	}

	/* recovering — amber pulsing */
	.sync-recovering {
		background: var(--accent-yellow-dim);
		border-color: rgba(234, 179, 8, 0.3);
		color: var(--accent-yellow);
		animation: pulse-error 1.5s ease-in-out infinite;
	}
	.sync-recovering .dot {
		background: var(--accent-yellow);
	}

	/* single-engine — gray */
	.sync-single {
		background: rgba(113, 113, 122, 0.15);
		border-color: rgba(113, 113, 122, 0.3);
		color: var(--text-muted, #71717a);
	}
	.sync-single .dot {
		background: var(--text-muted, #71717a);
	}

	/* disconnected — red pulsing */
	.sync-disconnected {
		background: var(--tally-program-dim);
		border-color: var(--tally-program-medium);
		color: var(--tally-program);
		animation: pulse-error 1.5s ease-in-out infinite;
	}
	.sync-disconnected .dot {
		background: var(--tally-program);
	}

	@keyframes pulse-error {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}
</style>
