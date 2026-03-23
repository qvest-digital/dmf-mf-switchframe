<script lang="ts">
	import type { ControlRoomState, SCTE35Active } from '$lib/api/types';
	import {
		replayStop, replayPause, replayResume, replaySetSpeed,
		graphicsOff, cancelMacro, fadeToBlack,
		scte35Return, scte35Hold,
		clipPlayerPause, clipPlayerStop,
		apiCall
	} from '$lib/api/switch-api';
	import {
		ICON_PLAY, ICON_PAUSE, ICON_STOP, ICON_CLOSE,
		ICON_LAYERS, ICON_BOLT, ICON_AD_BREAK, ICON_RETURN,
		ICON_FILM, ICON_CAPTION, ICON_LOCK,
		renderIcon
	} from '$lib/icons';

	interface Props {
		state: ControlRoomState;
	}
	let { state: s }: Props = $props();

	const replayActive = $derived(
		s.replay?.state === 'playing' || s.replay?.state === 'paused'
	);
	const replayPlaying = $derived(s.replay?.state === 'playing');
	const replaySpeed = $derived(s.replay?.speed ?? 1);

	const activeGraphicsLayers = $derived(
		(s.graphics?.layers ?? []).filter(l => l.active)
	);
	const hasActiveGraphics = $derived(activeGraphicsLayers.length > 0);

	const macroRunning = $derived(s.macro?.running ?? false);
	const macroName = $derived(s.macro?.macroName ?? '');
	const macroStep = $derived((s.macro?.currentStep ?? 0) + 1);
	const macroTotal = $derived(s.macro?.steps?.length ?? 0);

	const scte35Events = $derived(
		Object.values(s.scte35?.activeEvents ?? {}).filter(e => e.isOut)
	);
	const breakEvent = $derived(scte35Events.length > 0 ? scte35Events[0] : null);

	const activeClipOnPgm = $derived(() => {
		if (!s.clipPlayers) return null;
		for (const p of s.clipPlayers) {
			if (p.state === 'playing' || p.state === 'paused') return p;
		}
		return null;
	});
	const clipPlayer = $derived(activeClipOnPgm());

	const captionAuthor = $derived(s.captions?.mode === 'author');

	const hasAnything = $derived(
		replayActive || hasActiveGraphics || macroRunning ||
		breakEvent !== null || s.ftbActive || clipPlayer !== null || captionAuthor
	);

	let breakNow = $state(Date.now());
	$effect(() => {
		if (!breakEvent) return;
		const iv = setInterval(() => { breakNow = Date.now(); }, 250);
		return () => clearInterval(iv);
	});

	function breakCountdown(evt: SCTE35Active): string {
		if (evt.held) return 'HELD';
		if (!evt.durationMs || !evt.startedAt) return '';
		const remaining = evt.durationMs - (breakNow - evt.startedAt);
		if (remaining <= 0) return '0:00';
		const totalSec = Math.ceil(remaining / 1000);
		const min = Math.floor(totalSec / 60);
		const sec = totalSec % 60;
		return `${min}:${sec.toString().padStart(2, '0')}`;
	}

	const speedOptions = [0.25, 0.5, 1];
</script>

<div class="ctx-strip" class:empty={!hasAnything}>
	{#if s.ftbActive}
		<button class="ctx-chip ftb-chip" onclick={() => apiCall(fadeToBlack(), 'FTB')}>
			<span class="chip-icon">{@html renderIcon(ICON_LOCK, 12, 'stroke')}</span>
			<span class="chip-label">FTB</span>
		</button>
	{/if}

	{#if replayActive}
		<div class="ctx-chip replay-chip">
			<span class="chip-icon">{@html renderIcon(ICON_PLAY, 12, 'stroke')}</span>
			<span class="chip-label">Replay</span>
			<div class="chip-actions">
				{#if replayPlaying}
					<button class="chip-btn" onclick={() => apiCall(replayPause(), 'Pause')} title="Pause">
						{@html renderIcon(ICON_PAUSE, 11, 'stroke')}
					</button>
				{:else}
					<button class="chip-btn" onclick={() => apiCall(replayResume(), 'Resume')} title="Resume">
						{@html renderIcon(ICON_PLAY, 11, 'stroke')}
					</button>
				{/if}
				<button class="chip-btn" onclick={() => apiCall(replayStop(), 'Stop')} title="Stop">
					{@html renderIcon(ICON_STOP, 11, 'fill')}
				</button>
				<span class="chip-sep"></span>
				{#each speedOptions as spd}
					<button
						class="speed-btn"
						class:active={Math.abs(replaySpeed - spd) < 0.01}
						onclick={() => apiCall(replaySetSpeed(spd), 'Speed')}
					>{spd}x</button>
				{/each}
			</div>
		</div>
	{/if}

	{#if hasActiveGraphics}
		{#each activeGraphicsLayers as layer}
			<div class="ctx-chip gfx-chip">
				<span class="chip-icon">{@html renderIcon(ICON_LAYERS, 12, 'stroke')}</span>
				<span class="chip-label">GFX {layer.id}</span>
				<button class="chip-btn" onclick={() => apiCall(graphicsOff(layer.id), 'GFX Off')} title="Turn off layer {layer.id}">
					{@html renderIcon(ICON_CLOSE, 11, 'stroke')}
				</button>
			</div>
		{/each}
	{/if}

	{#if macroRunning}
		<div class="ctx-chip macro-chip">
			<span class="chip-icon">{@html renderIcon(ICON_BOLT, 12, 'stroke')}</span>
			<span class="chip-label">{macroName}</span>
			<span class="chip-progress">{macroStep}/{macroTotal}</span>
			<button class="chip-btn" onclick={() => apiCall(cancelMacro(), 'Cancel')} title="Cancel macro">
				{@html renderIcon(ICON_CLOSE, 11, 'stroke')}
			</button>
		</div>
	{/if}

	{#if breakEvent}
		<div class="ctx-chip break-chip" class:held={breakEvent.held}>
			<span class="chip-icon">{@html renderIcon(ICON_AD_BREAK, 12, 'stroke')}</span>
			<span class="chip-label">AD BREAK</span>
			<span class="chip-countdown">{breakCountdown(breakEvent)}</span>
			<div class="chip-actions">
				{#if breakEvent.held}
					<button class="chip-btn" onclick={() => apiCall(scte35Return(breakEvent.eventId), 'Return')} title="Release hold">
						{@html renderIcon(ICON_RETURN, 11, 'stroke')}
					</button>
				{:else}
					<button class="chip-btn" onclick={() => apiCall(scte35Hold(breakEvent.eventId), 'Hold')} title="Hold break">
						{@html renderIcon(ICON_PAUSE, 11, 'stroke')}
					</button>
					<button class="chip-btn" onclick={() => apiCall(scte35Return(breakEvent.eventId), 'Return')} title="Return from break">
						{@html renderIcon(ICON_RETURN, 11, 'stroke')}
					</button>
				{/if}
			</div>
		</div>
	{/if}

	{#if clipPlayer}
		<div class="ctx-chip clip-chip">
			<span class="chip-icon">{@html renderIcon(ICON_FILM, 12, 'stroke')}</span>
			<span class="chip-label">{clipPlayer.clipName ?? `Clip ${clipPlayer.id}`}</span>
			<div class="chip-actions">
				{#if clipPlayer.state === 'playing'}
					<button class="chip-btn" onclick={() => apiCall(clipPlayerPause(clipPlayer.id), 'Pause')} title="Pause">
						{@html renderIcon(ICON_PAUSE, 11, 'stroke')}
					</button>
				{/if}
				<button class="chip-btn" onclick={() => apiCall(clipPlayerStop(clipPlayer.id), 'Stop')} title="Stop">
					{@html renderIcon(ICON_STOP, 11, 'fill')}
				</button>
			</div>
		</div>
	{/if}

	{#if captionAuthor}
		<div class="ctx-chip caption-chip">
			<span class="chip-icon">{@html renderIcon(ICON_CAPTION, 12, 'stroke')}</span>
			<span class="chip-label">CAPTIONS</span>
		</div>
	{/if}
</div>

<style>
	.ctx-strip {
		flex: 1;
		display: flex;
		align-items: center;
		gap: 4px;
		min-width: 0;
		overflow-x: auto;
		overflow-y: hidden;
		padding: 0 6px;
	}

	.ctx-strip::-webkit-scrollbar {
		height: 2px;
	}

	.ctx-strip::-webkit-scrollbar-thumb {
		background: var(--border-subtle);
		border-radius: 1px;
	}

	.ctx-strip.empty {
		justify-content: center;
	}

	.ctx-chip {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 3px 8px;
		border-radius: var(--radius-md);
		background: var(--bg-elevated);
		border: 1px solid var(--border-subtle);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-primary);
		white-space: nowrap;
		flex-shrink: 0;
		animation: chip-in 0.15s ease-out;
	}

	@keyframes chip-in {
		from { opacity: 0; transform: scale(0.9); }
		to { opacity: 1; transform: scale(1); }
	}

	.chip-icon {
		display: flex;
		align-items: center;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.chip-label {
		letter-spacing: 0.02em;
		max-width: 100px;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.chip-actions {
		display: flex;
		align-items: center;
		gap: 2px;
		margin-left: 2px;
	}

	.chip-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 20px;
		height: 20px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
		padding: 0;
		transition: all var(--transition-fast);
	}

	.chip-btn:hover {
		border-color: var(--border-strong);
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	.chip-sep {
		width: 1px;
		height: 14px;
		background: var(--border-subtle);
		margin: 0 2px;
		flex-shrink: 0;
	}

	.chip-progress {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.chip-countdown {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		font-weight: 700;
		min-width: 2.5em;
		text-align: right;
	}

	.speed-btn {
		padding: 1px 5px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: transparent;
		color: var(--text-secondary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		transition: all var(--transition-fast);
	}

	.speed-btn:hover {
		border-color: var(--border-strong);
		color: var(--text-primary);
	}

	.speed-btn.active {
		background: var(--accent-blue-dim, rgba(59, 130, 246, 0.15));
		border-color: var(--accent-blue, #3b82f6);
		color: var(--accent-blue, #3b82f6);
	}

	/* Chip type colors */
	.ftb-chip {
		background: var(--accent-orange);
		border-color: var(--accent-orange);
		color: #000;
		cursor: pointer;
		animation: ftb-pulse 1.5s ease-in-out infinite;
	}

	@keyframes ftb-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.7; }
	}

	.ftb-chip .chip-icon { color: #000; }

	.replay-chip {
		border-color: rgba(59, 130, 246, 0.3);
		background: rgba(59, 130, 246, 0.08);
	}
	.replay-chip .chip-icon { color: var(--accent-blue, #3b82f6); }
	.replay-chip .chip-label { color: var(--accent-blue, #3b82f6); }

	.gfx-chip {
		border-color: rgba(168, 85, 247, 0.3);
		background: rgba(168, 85, 247, 0.08);
	}
	.gfx-chip .chip-icon { color: var(--accent-purple, #a855f7); }
	.gfx-chip .chip-label { color: var(--accent-purple, #a855f7); }

	.macro-chip {
		border-color: rgba(234, 179, 8, 0.3);
		background: rgba(234, 179, 8, 0.08);
	}
	.macro-chip .chip-icon { color: var(--accent-yellow); }
	.macro-chip .chip-label { color: var(--accent-yellow); }

	.break-chip {
		border-color: rgba(220, 38, 38, 0.3);
		background: rgba(220, 38, 38, 0.1);
		animation: chip-in 0.15s ease-out, break-pulse 1.5s ease-in-out infinite;
	}
	.break-chip .chip-icon { color: var(--tally-program); }
	.break-chip .chip-label { color: var(--tally-program); }

	.break-chip.held {
		border-color: rgba(245, 158, 11, 0.3);
		background: rgba(245, 158, 11, 0.1);
		animation: chip-in 0.15s ease-out;
	}
	.break-chip.held .chip-icon { color: var(--accent-orange); }
	.break-chip.held .chip-label { color: var(--accent-orange); }

	@keyframes break-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.7; }
	}

	.clip-chip {
		border-color: rgba(34, 197, 94, 0.3);
		background: rgba(34, 197, 94, 0.08);
	}
	.clip-chip .chip-icon { color: #22c55e; }

	.caption-chip {
		border-color: rgba(99, 102, 241, 0.3);
		background: rgba(99, 102, 241, 0.08);
	}
	.caption-chip .chip-icon { color: #6366f1; }
	.caption-chip .chip-label { color: #6366f1; }
</style>
