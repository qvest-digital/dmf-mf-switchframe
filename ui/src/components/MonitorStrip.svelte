<script lang="ts">
	import type { PFLMode } from '$lib/audio/pfl-toggle';
	import { monitor, setMonitorMuted, setMonitorVolume } from '$lib/audio/monitor.svelte';

	/**
	 * The local monitor: volume and mute of what this browser plays. The
	 * program mix the engine sends out is not touched.
	 */
	interface Props {
		/** Label of the channel soloed with PFL/AFL, if any. */
		soloSource?: string | null;
		soloMode?: PFLMode;
	}
	let { soloSource = null, soloMode = 'off' }: Props = $props();

	/** Fader floor; the bottom of the travel is silence. */
	const MIN_DB = -60;

	const volumeDb = $derived(monitor.volume > 0 ? Math.max(MIN_DB, 20 * Math.log10(monitor.volume)) : MIN_DB);
	const volumeLabel = $derived(monitor.volume > 0 && volumeDb > MIN_DB ? volumeDb.toFixed(1) : '-inf');

	function onFader(db: number) {
		setMonitorVolume(db <= MIN_DB ? 0 : Math.pow(10, db / 20));
	}
</script>

<div class="monitor-strip" title="Your monitor: what this browser plays. The program mix is not affected.">
	<span class="strip-label">MONITOR</span>
	<span class="strip-sub">this browser</span>

	{#if soloSource && soloMode !== 'off'}
		<span class="solo" data-testid="monitor-solo">{soloMode.toUpperCase()} {soloSource}</span>
	{:else}
		<span class="solo idle">PGM</span>
	{/if}

	<input
		type="range"
		class="fader"
		min={MIN_DB}
		max="0"
		step="0.5"
		value={volumeDb}
		{...{ orient: 'vertical' }}
		aria-label="Monitor volume"
		oninput={(e) => onFader(parseFloat((e.target as HTMLInputElement).value))}
	/>
	<span class="strip-db">{volumeLabel}</span>

	<button
		class="monitor-mute-btn"
		class:active={monitor.muted}
		aria-pressed={monitor.muted}
		onclick={() => setMonitorMuted(!monitor.muted)}
		title={monitor.muted ? 'Unmute your monitor' : 'Mute your monitor (program keeps going out)'}
	>MUTE</button>
	{#if monitor.dimmed || monitor.commsActive}
		<span class="dim-note">DIM</span>
	{/if}
</div>

<style>
	.monitor-strip {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 2px;
		padding: 4px 4px 3px;
		min-width: 72px;
		background: var(--bg-panel);
		border: 1px dashed var(--border-strong);
		border-radius: var(--radius-sm);
		overflow: hidden;
	}

	.strip-label {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		letter-spacing: 0.04em;
		color: var(--text-secondary);
	}

	.strip-sub {
		font-size: 8px;
		color: var(--text-tertiary);
	}

	.solo {
		font-family: var(--font-mono);
		font-size: 8px;
		font-weight: 700;
		color: var(--accent-amber, #f59e0b);
		max-width: 68px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.solo.idle {
		color: var(--text-tertiary);
		font-weight: 600;
	}

	.fader {
		writing-mode: vertical-lr;
		direction: rtl;
		width: 22px;
		flex: 1;
		min-height: 60px;
	}

	.strip-db {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-secondary);
	}

	.monitor-mute-btn {
		width: 100%;
		padding: 1px 2px;
		border: 1px solid var(--border-default);
		border-radius: 2px;
		background: var(--bg-elevated);
		color: var(--text-tertiary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 8px;
		font-weight: 600;
		letter-spacing: 0.04em;
	}

	.monitor-mute-btn.active {
		background: #dc2626;
		border-color: #dc2626;
		color: #fff;
	}

	.dim-note {
		font-size: 8px;
		font-weight: 700;
		color: var(--accent-amber, #f59e0b);
	}
</style>
