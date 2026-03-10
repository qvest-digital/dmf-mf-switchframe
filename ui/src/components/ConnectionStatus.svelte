<script lang="ts">
	type ConnectionIndicatorState = 'webtransport' | 'polling' | 'disconnected';

	interface Props { state: ConnectionIndicatorState; }
	let { state }: Props = $props();

	const label = $derived(
		state === 'webtransport' ? 'LIVE'
		: state === 'polling' ? 'POLLING'
		: 'OFFLINE'
	);

	const cssClass = $derived(
		state === 'webtransport' ? 'status-live'
		: state === 'polling' ? 'status-warning'
		: 'status-error'
	);
</script>

<span class="connection-status {cssClass}">{label}</span>

<style>
	.connection-status {
		display: inline-flex;
		align-items: center;
		padding: 2px 7px;
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.06em;
		line-height: 1;
		user-select: none;
	}

	.status-live {
		background: rgba(22, 163, 74, 0.15);
		border: 1px solid var(--tally-preview-medium);
		color: var(--tally-preview);
	}

	.status-warning {
		background: var(--accent-yellow-dim);
		border: 1px solid rgba(234, 179, 8, 0.3);
		color: var(--accent-yellow);
	}

	.status-error {
		background: var(--tally-program-dim);
		border: 1px solid var(--tally-program-medium);
		color: var(--tally-program);
		animation: pulse-error 1.5s ease-in-out infinite;
	}

	@keyframes pulse-error {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}
</style>
