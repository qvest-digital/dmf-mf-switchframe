<script lang="ts">
	import type { SourceHealthStatus } from '$lib/api/types';

	interface Props {
		health: SourceHealthStatus | string;
		sourceLabel: string;
	}

	let { health, sourceLabel }: Props = $props();

	const statusLabels: Record<string, string> = {
		stale: 'STALE',
		no_signal: 'NO SIGNAL',
		offline: 'OFFLINE',
	};

	let displayStatus = $derived(statusLabels[health] ?? health.toUpperCase());
</script>

{#if health !== 'healthy'}
	<div class="health-alarm" role="alert" aria-live="assertive">
		<span class="alarm-text">PROGRAM: {sourceLabel} — {displayStatus}</span>
	</div>
{/if}

<style>
	@keyframes alarm-flash {
		0%, 100% {
			border-color: rgba(220, 38, 38, 1);
		}
		50% {
			border-color: rgba(220, 38, 38, 0.3);
		}
	}

	.health-alarm {
		position: absolute;
		inset: 0;
		z-index: 10;
		display: flex;
		align-items: center;
		justify-content: center;
		background: rgba(220, 38, 38, 0.85);
		border: 3px solid rgba(220, 38, 38, 1);
		border-radius: var(--radius-md);
		animation: alarm-flash 1s ease-in-out infinite;
		pointer-events: none;
	}

	.alarm-text {
		font-family: var(--font-ui);
		font-weight: 700;
		font-size: 1.1rem;
		color: #fff;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		text-shadow: 0 1px 4px rgba(0, 0, 0, 0.5);
	}
</style>
