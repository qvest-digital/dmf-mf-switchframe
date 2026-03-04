<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import {
		setLevel as apiSetLevel,
		setMute as apiSetMute,
		setAFV as apiSetAFV,
		setMasterLevel as apiSetMasterLevel,
		fireAndForget,
	} from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
		pflActiveSource?: string | null;
		onPFLToggle?: (sourceKey: string) => void;
	}

	let { state, pflActiveSource = null, onPFLToggle }: Props = $props();

	function setLevel(source: string, level: number) {
		fireAndForget(apiSetLevel(source, level));
	}

	function setMute(source: string, muted: boolean) {
		fireAndForget(apiSetMute(source, muted));
	}

	function setAFV(source: string, afv: boolean) {
		fireAndForget(apiSetAFV(source, afv));
	}

	function setMasterLevel(level: number) {
		fireAndForget(apiSetMasterLevel(level));
	}

	/**
	 * Map a dB value (-60..+12) to a percentage (0..100) for meter/fader display.
	 * Values below -60 clamp to 0%, above +12 clamp to 100%.
	 */
	function dbToPercent(db: number): number {
		const min = -60;
		const max = 12;
		if (db <= min) return 0;
		if (db >= max) return 100;
		return ((db - min) / (max - min)) * 100;
	}

	/** Sorted source keys for consistent channel strip order. */
	let sortedKeys = $derived(
		state.audioChannels != null
			? Object.keys(state.audioChannels).sort()
			: [],
	);
</script>

<div class="audio-mixer">
	<!-- Channel strips -->
	{#each sortedKeys as key (key)}
		{@const channel = state.audioChannels?.[key]!}
		{@const source = state.sources[key]}
		{@const label = source?.label || key}
		{@const tally = state.tallyState[key] ?? 'idle'}
		<div class="channel-strip" class:program={tally === 'program'} class:preview={tally === 'preview'}>
			<span class="strip-label">{label}</span>

			<div class="meter-fader">
				<!-- VU meter bar -->
				<div class="vu-meter">
					<div class="vu-fill" style="height: {dbToPercent(channel.level)}%"></div>
				</div>

				<!-- Vertical fader -->
				<input
					type="range"
					class="fader"
					min="-60"
					max="12"
					step="0.5"
					value={channel.level}
					orient="vertical"
					oninput={(e) => setLevel(key, parseFloat((e.target as HTMLInputElement).value))}
				/>
			</div>

			<div class="strip-buttons">
				<button
					class="pfl-btn"
					class:active={pflActiveSource === key}
					onclick={() => onPFLToggle?.(key)}
					title="Pre-Fader Listen"
				>
					PFL
				</button>
				<button
					class="mute-btn"
					class:active={channel.muted}
					onclick={() => setMute(key, !channel.muted)}
					title="Mute"
				>
					MUTE
				</button>
				<button
					class="afv-btn"
					class:active={channel.afv}
					onclick={() => setAFV(key, !channel.afv)}
					title="Audio Follows Video"
				>
					AFV
				</button>
			</div>

			<span class="strip-db">{channel.level.toFixed(1)} dB</span>
		</div>
	{/each}

	<!-- Master strip -->
	<div class="master-strip">
		<span class="strip-label">MASTER</span>

		<div class="meter-fader">
			<!-- Program peak meter (L/R) -->
			<div class="program-meter">
				<div class="peak-bar left">
					<div class="peak-fill" style="height: {dbToPercent(state.programPeak[0])}%"></div>
				</div>
				<div class="peak-bar right">
					<div class="peak-fill" style="height: {dbToPercent(state.programPeak[1])}%"></div>
				</div>
			</div>

			<!-- Master fader -->
			<input
				type="range"
				class="fader"
				min="-60"
				max="12"
				step="0.5"
				value={state.masterLevel}
				orient="vertical"
				oninput={(e) => setMasterLevel(parseFloat((e.target as HTMLInputElement).value))}
			/>
		</div>

		<span class="strip-db">{state.masterLevel.toFixed(1)} dB</span>
	</div>
</div>

<style>
	.audio-mixer {
		display: flex;
		gap: 0.25rem;
		padding: 0.5rem;
		background: var(--bg-secondary);
		border-top: 1px solid #333;
		overflow-x: auto;
	}

	.channel-strip,
	.master-strip {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.25rem;
		padding: 0.5rem;
		background: #1a1a1a;
		border: 1px solid #333;
		border-radius: 4px;
		min-width: 64px;
	}

	.channel-strip.program { border-color: var(--tally-program); }
	.channel-strip.preview { border-color: var(--tally-preview); }

	.master-strip {
		border-color: #666;
		background: #222;
	}

	.strip-label {
		font-family: monospace;
		font-size: 0.7rem;
		font-weight: bold;
		color: var(--text-primary);
		text-align: center;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		max-width: 64px;
	}

	.meter-fader {
		display: flex;
		gap: 0.25rem;
		align-items: stretch;
		height: 120px;
	}

	/* VU meter */
	.vu-meter {
		width: 8px;
		background: #0a0a0a;
		border: 1px solid #333;
		border-radius: 2px;
		position: relative;
		overflow: hidden;
	}

	.vu-fill {
		position: absolute;
		bottom: 0;
		left: 0;
		right: 0;
		background: linear-gradient(to top, #00cc00 0%, #cccc00 70%, #cc0000 90%);
		transition: height 0.05s linear;
	}

	/* Program peak meter (L/R) */
	.program-meter {
		display: flex;
		gap: 2px;
		width: 20px;
	}

	.peak-bar {
		flex: 1;
		background: #0a0a0a;
		border: 1px solid #333;
		border-radius: 2px;
		position: relative;
		overflow: hidden;
	}

	.peak-fill {
		position: absolute;
		bottom: 0;
		left: 0;
		right: 0;
		background: linear-gradient(to top, #00cc00 0%, #cccc00 70%, #cc0000 90%);
		transition: height 0.05s linear;
	}

	/* Vertical fader */
	.fader {
		writing-mode: vertical-lr;
		direction: rtl;
		width: 24px;
		height: 100%;
		cursor: pointer;
		accent-color: #888;
	}

	/* Strip buttons */
	.strip-buttons {
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
		width: 100%;
	}

	.strip-buttons button {
		padding: 0.2rem 0.25rem;
		border: 1px solid #444;
		border-radius: 3px;
		background: #1a1a1a;
		color: #888;
		cursor: pointer;
		font-family: monospace;
		font-size: 0.6rem;
		font-weight: bold;
		text-align: center;
	}

	.pfl-btn.active { background: #665500; color: #ffcc00; border-color: #ffcc00; }
	.mute-btn.active { background: #440000; color: #ff4444; border-color: #ff4444; }
	.afv-btn.active { background: #004400; color: #44ff44; border-color: #44ff44; }

	.strip-db {
		font-family: monospace;
		font-size: 0.6rem;
		color: var(--text-secondary);
	}
</style>
