<script lang="ts">
	import type { EngineRole } from '$lib/api/types';

	interface Props {
		engineLabel?: string;
		role?: EngineRole;
	}

	let { engineLabel, role }: Props = $props();

	const visible = $derived(
		!!engineLabel &&
		!!role &&
		role !== 'standalone'
	);

	const label = $derived.by((): string => {
		if (!engineLabel) return '';
		const labelUpper = engineLabel.toUpperCase();
		switch (role) {
			case 'leader': return `${labelUpper} · LEADER`;
			case 'follower-healthy': return `${labelUpper} · FOLLOWER`;
			case 'follower-partitioned': return `${labelUpper} · PARTITIONED`;
			default: return labelUpper;
		}
	});

	const cssClass = $derived.by((): string => {
		switch (role) {
			case 'leader': return 'role-leader';
			case 'follower-healthy': return 'role-follower-healthy';
			case 'follower-partitioned': return 'role-follower-partitioned';
			default: return '';
		}
	});

	const title = $derived.by((): string => {
		switch (role) {
			case 'leader':
				return 'This engine holds authority for non-idempotent operations (recording, legacy SRT output).';
			case 'follower-healthy':
				return 'Peer is reachable; this engine is follower by label tiebreak. Recording and SRT output still work — gating only applies during partition.';
			case 'follower-partitioned':
				return 'Peer unreachable; this engine is non-leader by label tiebreak. Recording start and legacy SRT output will return HTTP 409 until the peer recovers or the control plane forces a takeover.';
			default:
				return '';
		}
	});
</script>

{#if visible}
	<span class="role-badge {cssClass}" {title} aria-live="polite" data-testid="engine-role-badge">
		<span class="dot"></span>
		{label}
	</span>
{/if}

<style>
	.role-badge {
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
		border: 1px solid transparent;
	}

	.dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.role-leader {
		background: rgba(22, 163, 74, 0.15);
		border-color: var(--tally-preview-medium);
		color: var(--tally-preview);
	}
	.role-leader .dot {
		background: var(--tally-preview);
	}

	.role-follower-healthy {
		background: rgba(113, 113, 122, 0.15);
		border-color: rgba(113, 113, 122, 0.3);
		color: var(--text-muted, #71717a);
	}
	.role-follower-healthy .dot {
		background: var(--text-muted, #71717a);
	}

	.role-follower-partitioned {
		background: var(--tally-program-dim);
		border-color: var(--tally-program-medium);
		color: var(--tally-program);
		animation: pulse-partition 1.8s ease-in-out infinite;
	}
	.role-follower-partitioned .dot {
		background: var(--tally-program);
	}

	@keyframes pulse-partition {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.55; }
	}
</style>
