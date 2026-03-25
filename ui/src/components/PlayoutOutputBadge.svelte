<script lang="ts">
	import type { PlayoutChannelState, PlaylistItem } from '$lib/api/types';

	interface Props {
		channel: PlayoutChannelState;
		currentItem?: PlaylistItem;
		isIdle: boolean;
	}

	let { channel, currentItem, isIdle }: Props = $props();

	type BadgeType = 'clip' | 'live' | 'idle' | 'slate' | 'offline' | 'break';

	const badgeState = $derived.by(() => {
		if (channel.state === 'stopped' && !isIdle) return { label: 'OFF AIR', type: 'offline' as BadgeType };
		if (channel.mode === 'break') return { label: 'BREAK', type: 'break' as BadgeType };
		if (isIdle) return { label: 'IDLE LOOP', type: 'idle' as BadgeType };
		if (!currentItem) return { label: 'STANDBY', type: 'idle' as BadgeType };
		switch (currentItem.type) {
			case 'clip': return { label: `CLIP: ${currentItem.title}`, type: 'clip' as BadgeType };
			case 'live': return { label: `LIVE: ${currentItem.source ?? currentItem.title}`, type: 'live' as BadgeType };
			case 'slate': return { label: 'SLATE', type: 'slate' as BadgeType };
			default: return { label: 'OUTPUT', type: 'idle' as BadgeType };
		}
	});

	const shouldPulse = $derived(
		badgeState.type === 'live' || badgeState.type === 'offline' || badgeState.type === 'break'
	);
</script>

<div
	class="output-badge {badgeState.type}"
	class:pulse={shouldPulse}
	data-testid="output-badge"
	data-badge-type={badgeState.type}
>
	<span class="dot"></span>
	<span class="label">{badgeState.label}</span>
</div>

<style>
	.output-badge {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		height: 22px;
		padding: 0 8px;
		border-radius: 11px;
		border: 1px solid;
		max-width: 220px;
		flex-shrink: 0;
	}

	.label {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		line-height: 1;
	}

	.dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	/* ── Clip: blue ── */
	.output-badge.clip {
		background: rgba(59, 130, 246, 0.2);
		border-color: rgba(59, 130, 246, 0.5);
		color: rgba(96, 165, 250, 1);
	}
	.output-badge.clip .dot {
		background: rgba(59, 130, 246, 0.8);
	}

	/* ── Live: red + pulse ── */
	.output-badge.live {
		background: rgba(220, 38, 38, 0.2);
		border-color: var(--tally-program, #dc2626);
		color: var(--tally-program, #dc2626);
	}
	.output-badge.live .dot {
		background: var(--tally-program, #dc2626);
	}

	/* ── Idle: amber/orange ── */
	.output-badge.idle {
		background: rgba(234, 168, 23, 0.15);
		border-color: var(--accent-orange, #eaa817);
		color: var(--accent-orange, #eaa817);
	}
	.output-badge.idle .dot {
		background: var(--accent-orange, #eaa817);
	}

	/* ── Slate: gray ── */
	.output-badge.slate {
		background: var(--bg-surface, #1e1e1e);
		border-color: var(--border-default, #333);
		color: var(--text-tertiary, #888);
	}
	.output-badge.slate .dot {
		background: var(--text-tertiary, #888);
	}

	/* ── Offline: dark red + fast pulse ── */
	.output-badge.offline {
		background: rgba(220, 38, 38, 0.3);
		border-color: rgba(220, 38, 38, 0.7);
		color: rgba(248, 113, 113, 1);
	}
	.output-badge.offline .dot {
		background: rgba(248, 113, 113, 1);
	}

	/* ── Break: orange + pulse ── */
	.output-badge.break {
		background: rgba(234, 168, 23, 0.2);
		border-color: var(--accent-orange, #eaa817);
		color: var(--accent-orange, #eaa817);
	}
	.output-badge.break .dot {
		background: var(--accent-orange, #eaa817);
	}

	/* ── Pulse animation ── */
	@keyframes output-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.6; }
	}
	.pulse {
		animation: output-pulse 2s ease-in-out infinite;
	}
</style>
