<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import {
		setLevel as apiSetLevel,
		setTrim as apiSetTrim,
		setMute as apiSetMute,
		setAFV as apiSetAFV,
		setMasterLevel as apiSetMasterLevel,
		setEQ as apiSetEQ,
		setCompressor as apiSetCompressor,
		setSourceDelay,
		setAudioDelay as apiSetAudioDelay,
	} from '$lib/api/switch-api';
	import { throttle } from '$lib/util/throttle';
	import { sortedSourceKeys } from '$lib/util/sort-sources';
	import { updatePeakHold as _updatePeakHold, isClipActive, CLIP_THRESHOLD_DB } from '$lib/audio/peak-hold';

	interface Props {
		state: ControlRoomState;
		sourceLevels?: Record<string, { peakL: number; peakR: number }>;
		programLevels?: { peakL: number; peakR: number };
		pflActiveSource?: string | null;
		expandedKeys?: Record<string, boolean>;
		onPFLToggle?: (sourceKey: string) => void;
		onStateUpdate?: (state: ControlRoomState) => void;
		onExpandToggle?: (sourceKey: string) => void;
	}

	let { state: crState, sourceLevels = {}, programLevels = { peakL: 0, peakR: 0 }, pflActiveSource = null, expandedKeys = {}, onPFLToggle, onStateUpdate, onExpandToggle }: Props = $props();

	let ui = $state({ collapsed: false });

	/** Fire API call and apply the returned state for immediate UI feedback. */
	function applyResult(promise: Promise<ControlRoomState>) {
		promise.then(s => onStateUpdate?.(s)).catch(err => console.warn('API call failed:', err));
	}

	/** Throttled level API call -- max 20 calls/sec (50ms). Visual fader updates instantly. */
	const setLevelThrottled = throttle((source: string, level: number) => {
		applyResult(apiSetLevel(source, level));
	}, 50);

	function setLevel(source: string, level: number) {
		setLevelThrottled(source, level);
	}

	/** Throttled trim API call -- max 20 calls/sec (50ms). */
	const setTrimThrottled = throttle((source: string, level: number) => {
		applyResult(apiSetTrim(source, level));
	}, 50);

	function setMute(source: string, muted: boolean) {
		applyResult(apiSetMute(source, muted));
	}

	function setAFV(source: string, afv: boolean) {
		applyResult(apiSetAFV(source, afv));
	}

	/** Throttled master level API call -- max 20 calls/sec (50ms). */
	const setMasterLevelThrottled = throttle((level: number) => {
		applyResult(apiSetMasterLevel(level));
	}, 50);

	function setMasterLevel(level: number) {
		setMasterLevelThrottled(level);
	}

	/** Throttled EQ API call -- max 20 calls/sec (50ms). */
	const setEQThrottled = throttle((source: string, band: number, frequency: number, gain: number, q: number, enabled: boolean) => {
		applyResult(apiSetEQ(source, band, frequency, gain, q, enabled));
	}, 50);

	/** Throttled compressor API call -- max 20 calls/sec (50ms). */
	const setCompressorThrottled = throttle((source: string, threshold: number, ratio: number, attack: number, release: number, makeupGain: number) => {
		applyResult(apiSetCompressor(source, threshold, ratio, attack, release, makeupGain));
	}, 50);

	/** Throttled source delay API call -- max 20 calls/sec (50ms). */
	const setDelayThrottled = throttle((source: string, delayMs: number) => {
		applyResult(setSourceDelay(source, delayMs));
	}, 50);

	/** Throttled audio delay API call -- max 20 calls/sec (50ms). */
	const setAudioDelayThrottled = throttle((source: string, delayMs: number) => {
		applyResult(apiSetAudioDelay(source, delayMs));
	}, 50);

	/** Convert linear amplitude (0..1) to dBFS, clamped to -60. */
	function linearToDb(linear: number): number {
		if (linear <= 0) return -60;
		const db = 20 * Math.log10(linear);
		return db < -60 ? -60 : db;
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

	/**
	 * Compute the effective peak level in dBFS for a channel meter.
	 * Prefers client-side levels (linear amplitude from rAF-sampled audio
	 * decoders at 60Hz) for smooth real-time animation. Falls back to
	 * server-side peaks (dBFS, updated on state broadcasts) for sources
	 * without client-side audio decoders.
	 */
	function channelPeakDb(serverPeak: number | undefined, clientLinear: number | undefined): number {
		if (clientLinear !== undefined && clientLinear > 0) return linearToDb(clientLinear);
		const sp = serverPeak ?? -96;
		if (sp > -96) return sp;
		return -60;
	}

	/** Map gain reduction (0..20+ dB) to a percentage (0..100) for GR meter. */
	function grToPercent(gr: number): number {
		if (gr <= 0) return 0;
		if (gr >= 20) return 100;
		return (gr / 20) * 100;
	}

	function toggleExpanded(key: string) {
		onExpandToggle?.(key);
	}

	/** Per-source compressor bypass state (true = bypassed). */
	let compBypass: Record<string, boolean> = $state({});

	/** Saved compressor settings before bypass, so we can restore on re-enable. */
	let compSaved: Record<string, { threshold: number; ratio: number; attack: number; release: number; makeupGain: number }> = $state({});

	function toggleCompBypass(source: string, channel: { compressor: { threshold: number; ratio: number; attack: number; release: number; makeupGain: number } }) {
		const isBypassed = compBypass[source] ?? false;
		if (!isBypassed) {
			// Save current values and send bypassed params (ratio=1, makeupGain=0)
			compSaved[source] = {
				threshold: channel.compressor.threshold,
				ratio: channel.compressor.ratio,
				attack: channel.compressor.attack,
				release: channel.compressor.release,
				makeupGain: channel.compressor.makeupGain,
			};
			compBypass[source] = true;
			applyResult(apiSetCompressor(source, channel.compressor.threshold, 1.0, channel.compressor.attack, channel.compressor.release, 0));
		} else {
			// Restore saved values
			const saved = compSaved[source];
			if (saved) {
				applyResult(apiSetCompressor(source, saved.threshold, saved.ratio, saved.attack, saved.release, saved.makeupGain));
			}
			compBypass[source] = false;
		}
	}

	const EQ_BAND_NAMES = ['Low', 'Mid', 'High'];

	// LUFS readout values for master strip.
	let momentaryLufs = $derived(crState.momentaryLufs ?? -Infinity);
	let shortTermLufs = $derived(crState.shortTermLufs ?? -Infinity);
	let integratedLufs = $derived(crState.integratedLufs ?? -Infinity);

	/** Sorted source keys for consistent channel strip order. */
	let sortedKeys = $derived(
		crState.audioChannels != null
			? sortedSourceKeys(crState.sources).filter(k => k in crState.audioChannels!)
			: [],
	);

	// Peak hold state: non-reactive maps to avoid $effect loops.
	// Updated imperatively via getPeakHold() which is called during render.
	const _peakHolds = new Map<string, { L: number; R: number; timeL: number; timeR: number }>();
	const _clipIndicators = new Map<string, { L: number; R: number }>();

	// Trigger re-render when peak holds change (bumped in getPeakHold)
	let peakHoldTick = $state(0);

	function getPeakHold(key: string, peakLDb: number, peakRDb: number): { L: number; R: number } {
		const now = Date.now();
		const hold = _peakHolds.get(key) ?? { L: -96, R: -96, timeL: 0, timeR: 0 };

		const updated = _updatePeakHold(hold, peakLDb, peakRDb, now);
		if (updated.L !== hold.L || updated.R !== hold.R || updated.timeL !== hold.timeL || updated.timeR !== hold.timeR) {
			_peakHolds.set(key, updated);
		}

		// Clip detection at -1 dBFS
		const clip = _clipIndicators.get(key) ?? { L: 0, R: 0 };
		if (peakLDb > CLIP_THRESHOLD_DB) { clip.L = now; _clipIndicators.set(key, clip); }
		if (peakRDb > CLIP_THRESHOLD_DB) { clip.R = now; _clipIndicators.set(key, clip); }

		return { L: hold.L, R: hold.R };
	}

	function isClipped(key: string, channel: 'L' | 'R'): boolean {
		const clip = _clipIndicators.get(key);
		if (!clip) return false;
		return isClipActive(clip, channel, Date.now());
	}

	function clearClip(key: string) {
		_clipIndicators.set(key, { L: 0, R: 0 });
		peakHoldTick++;
	}
</script>

<div class="audio-mixer" class:collapsed={ui.collapsed}>
	<button class="collapse-toggle" onclick={() => ui = { ...ui, collapsed: !ui.collapsed }} title={ui.collapsed ? 'Expand audio mixer' : 'Collapse audio mixer'}>
		<span class="toggle-arrow">{ui.collapsed ? '\u25B6' : '\u25BC'}</span>
		<span class="toggle-label">AUDIO</span>
	</button>
	{#if !ui.collapsed}
	<!-- Channel strips -->
	{#each sortedKeys as key (key)}
		{@const channel = crState.audioChannels?.[key]!}
		{@const source = crState.sources[key]}
		{@const label = source?.label || key}
		{@const tally = crState.tallyState[key] ?? 'idle'}
		{@const isExpanded = expandedKeys[key] ?? false}
		{@const peakLDb = channelPeakDb(channel?.peakL, sourceLevels[key]?.peakL)}
		{@const peakRDb = channelPeakDb(channel?.peakR, sourceLevels[key]?.peakR)}
		{@const peakHold = getPeakHold(key, peakLDb, peakRDb)}
		{@const _tick = peakHoldTick}
		<div class="channel-strip" class:program={tally === 'program'} class:preview={tally === 'preview'} class:expanded={isExpanded}>
			<span class="strip-label">{label}</span>

			<div class="trim-control">
				<input
					type="range"
					class="trim-knob"
					min="-20"
					max="20"
					step="0.5"
					value={channel.trim ?? 0}
					aria-label="Trim for {label}"
					oninput={(e) => setTrimThrottled(key, parseFloat((e.target as HTMLInputElement).value))}
				/>
				<span class="trim-value">{(channel.trim ?? 0).toFixed(1)}</span>
			</div>

			<div class="meter-fader">
				<!-- Stereo VU meter (pre-fader input level, L/R) -->
				<div class="meter-wrapper" aria-hidden="true">
					<div class="db-scale">
						{#each [-6, -12, -24, -48] as db}
							<div class="db-mark" style="bottom: {dbToPercent(db)}%">
								<span class="db-label">{db}</span>
							</div>
						{/each}
					</div>
					<div class="stereo-meter">
						<div class="peak-bar left">
							<div class="peak-fill" style="height: {dbToPercent(peakLDb)}%"></div>
							<div class="peak-hold-line" style="bottom: {dbToPercent(peakHold.L)}%"></div>
							{#if isClipped(key, 'L')}
								<button class="clip-dot" onclick={() => clearClip(key)} title="Click to clear clip indicator"></button>
							{/if}
						</div>
						<div class="peak-bar right">
							<div class="peak-fill" style="height: {dbToPercent(peakRDb)}%"></div>
							<div class="peak-hold-line" style="bottom: {dbToPercent(peakHold.R)}%"></div>
							{#if isClipped(key, 'R')}
								<button class="clip-dot" onclick={() => clearClip(key)} title="Click to clear clip indicator"></button>
							{/if}
						</div>
					</div>
				</div>

				<!-- Vertical fader -->
				<input
					type="range"
					class="fader"
					min="-60"
					max="12"
					step="0.5"
					value={channel.level}
					{...{orient: "vertical"}}
					aria-label="Volume for {label}"
					oninput={(e) => setLevel(key, parseFloat((e.target as HTMLInputElement).value))}
				/>
			</div>

			<div class="strip-buttons">
				<button
					class="strip-btn pfl-btn"
					class:active={pflActiveSource === key}
					onclick={() => onPFLToggle?.(key)}
					title="Pre-Fader Listen"
				>
					PFL
				</button>
				<button
					class="strip-btn mute-btn"
					class:active={channel.muted}
					onclick={() => setMute(key, !channel.muted)}
					title="Mute"
				>
					MUTE
				</button>
				<button
					class="strip-btn afv-btn"
					class:active={channel.afv}
					onclick={() => setAFV(key, !channel.afv)}
					title="Audio Follows Video"
				>
					AFV
				</button>
				<button
					class="strip-btn eq-toggle-btn"
					class:active={isExpanded}
					onclick={() => toggleExpanded(key)}
					title="EQ & Dynamics"
				>
					EQ
				</button>
			</div>

			<span class="strip-db">{channel.level.toFixed(1)}</span>

			<!-- Expandable EQ & Compressor section -->
			{#if isExpanded && channel.eq && channel.compressor}
				<div class="eq-comp-section">
					<div class="eq-section">
						<span class="section-title">EQ</span>
						{#each [0, 1, 2] as band}
							{@const eqBand = channel.eq[band]}
							<div class="eq-band">
								<div class="eq-band-header">
									<span class="eq-band-name">{EQ_BAND_NAMES[band]}</span>
									<button
										class="eq-enable-btn"
										class:active={eqBand?.enabled}
										onclick={() => {
											if (eqBand) {
												setEQThrottled(key, band, eqBand.frequency, eqBand.gain, eqBand.q, !eqBand.enabled);
											}
										}}
										aria-label="Enable {EQ_BAND_NAMES[band]} EQ for {label}"
									>
										{eqBand?.enabled ? 'ON' : 'OFF'}
									</button>
								</div>
								<label class="eq-param">
									<span class="eq-param-label">Freq</span>
									<input
										type="range"
										class="eq-slider"
										min={band === 0 ? 80 : band === 1 ? 200 : 1000}
										max={band === 0 ? 1000 : band === 1 ? 8000 : 16000}
										step="1"
										value={eqBand?.frequency ?? (band === 0 ? 250 : band === 1 ? 1000 : 4000)}
										aria-label="{EQ_BAND_NAMES[band]} frequency for {label}"
										oninput={(e) => {
											if (eqBand) {
												setEQThrottled(key, band, parseFloat((e.target as HTMLInputElement).value), eqBand.gain, eqBand.q, eqBand.enabled);
											}
										}}
									/>
									<span class="eq-param-value">{(eqBand?.frequency ?? 0).toFixed(0)}</span>
								</label>
								<label class="eq-param">
									<span class="eq-param-label">Gain</span>
									<input
										type="range"
										class="eq-slider"
										min="-12"
										max="12"
										step="0.5"
										value={eqBand?.gain ?? 0}
										aria-label="{EQ_BAND_NAMES[band]} gain for {label}"
										oninput={(e) => {
											if (eqBand) {
												setEQThrottled(key, band, eqBand.frequency, parseFloat((e.target as HTMLInputElement).value), eqBand.q, eqBand.enabled);
											}
										}}
									/>
									<span class="eq-param-value">{(eqBand?.gain ?? 0).toFixed(1)}</span>
								</label>
								<label class="eq-param">
									<span class="eq-param-label">Q</span>
									<input
										type="range"
										class="eq-slider"
										min="0.5"
										max="4.0"
										step="0.1"
										value={eqBand?.q ?? 1.0}
										aria-label="{EQ_BAND_NAMES[band]} Q for {label}"
										oninput={(e) => {
											if (eqBand) {
												setEQThrottled(key, band, eqBand.frequency, eqBand.gain, parseFloat((e.target as HTMLInputElement).value), eqBand.enabled);
											}
										}}
									/>
									<span class="eq-param-value">{(eqBand?.q ?? 1.0).toFixed(1)}</span>
								</label>
							</div>
						{/each}
					</div>

					<div class="comp-section" class:comp-bypassed={compBypass[key]}>
						<div class="section-header">
							<span class="section-title">COMP</span>
							<button
								class="bypass-toggle"
								class:bypass-active={!compBypass[key]}
								onclick={() => toggleCompBypass(key, channel)}
								aria-label="Compressor {compBypass[key] ? 'off' : 'on'}"
							>ON</button>
						</div>
						<label class="eq-param">
							<span class="eq-param-label">Thresh</span>
							<input
								type="range"
								class="eq-slider"
								min="-40"
								max="0"
								step="0.5"
								value={channel.compressor.threshold}
								aria-label="Compressor threshold for {label}"
								oninput={(e) => {
									const c = channel.compressor;
									setCompressorThrottled(key, parseFloat((e.target as HTMLInputElement).value), c.ratio, c.attack, c.release, c.makeupGain);
								}}
							/>
							<span class="eq-param-value">{channel.compressor.threshold.toFixed(1)}</span>
						</label>
						<label class="eq-param">
							<span class="eq-param-label">Ratio</span>
							<input
								type="range"
								class="eq-slider"
								min="1"
								max="20"
								step="0.5"
								value={channel.compressor.ratio}
								aria-label="Compressor ratio for {label}"
								oninput={(e) => {
									const c = channel.compressor;
									setCompressorThrottled(key, c.threshold, parseFloat((e.target as HTMLInputElement).value), c.attack, c.release, c.makeupGain);
								}}
							/>
							<span class="eq-param-value">{channel.compressor.ratio.toFixed(1)}</span>
						</label>
						<label class="eq-param">
							<span class="eq-param-label">Attack</span>
							<input
								type="range"
								class="eq-slider"
								min="0.1"
								max="100"
								step="0.1"
								value={channel.compressor.attack}
								aria-label="Compressor attack for {label}"
								oninput={(e) => {
									const c = channel.compressor;
									setCompressorThrottled(key, c.threshold, c.ratio, parseFloat((e.target as HTMLInputElement).value), c.release, c.makeupGain);
								}}
							/>
							<span class="eq-param-value">{channel.compressor.attack.toFixed(1)}ms</span>
						</label>
						<label class="eq-param">
							<span class="eq-param-label">Release</span>
							<input
								type="range"
								class="eq-slider"
								min="10"
								max="1000"
								step="1"
								value={channel.compressor.release}
								aria-label="Compressor release for {label}"
								oninput={(e) => {
									const c = channel.compressor;
									setCompressorThrottled(key, c.threshold, c.ratio, c.attack, parseFloat((e.target as HTMLInputElement).value), c.makeupGain);
								}}
							/>
							<span class="eq-param-value">{channel.compressor.release.toFixed(0)}ms</span>
						</label>
						<label class="eq-param">
							<span class="eq-param-label">Makeup</span>
							<input
								type="range"
								class="eq-slider"
								min="0"
								max="24"
								step="0.5"
								value={channel.compressor.makeupGain}
								aria-label="Compressor makeup gain for {label}"
								oninput={(e) => {
									const c = channel.compressor;
									setCompressorThrottled(key, c.threshold, c.ratio, c.attack, c.release, parseFloat((e.target as HTMLInputElement).value));
								}}
							/>
							<span class="eq-param-value">{channel.compressor.makeupGain.toFixed(1)}</span>
						</label>
						<!-- GR meter bar -->
						<div class="gr-meter" aria-label="Gain reduction for {label}">
							<span class="gr-label">GR</span>
							<div class="gr-bar">
								<div class="gr-fill" style="width: {grToPercent(channel.gainReduction ?? 0)}%"></div>
							</div>
							<span class="gr-value">{(channel.gainReduction ?? 0).toFixed(1)}</span>
						</div>
					</div>

					<!-- Source Delay -->
					<div class="delay-section">
						<div class="section-header">
							<span class="section-title">DELAY</span>
							<span class="param-value">{crState.sources?.[key]?.delayMs ?? 0}ms</span>
						</div>
						<input
							type="range"
							class="eq-slider"
							min="0"
							max="500"
							step="1"
							value={crState.sources?.[key]?.delayMs ?? 0}
							oninput={(e) => setDelayThrottled(key, parseInt(e.currentTarget.value))}
							aria-label="Source delay"
						/>
					</div>

					<!-- Audio Delay (Lip Sync) -->
					<div class="delay-section">
						<div class="section-header">
							<span class="section-title">LIP SYNC</span>
							<span class="param-value">{channel.audioDelayMs ?? 0}ms</span>
						</div>
						<input
							type="range"
							class="eq-slider"
							min="0"
							max="500"
							step="1"
							value={channel.audioDelayMs ?? 0}
							oninput={(e) => setAudioDelayThrottled(key, parseInt(e.currentTarget.value))}
							aria-label="Audio delay for lip-sync correction"
						/>
					</div>
				</div>
			{/if}
		</div>
	{/each}

	<!-- Master strip -->
	<div class="master-strip">
		<span class="strip-label">MASTER</span>

		<div class="meter-fader">
			<!-- Program peak meter (L/R) -->
			<div class="meter-wrapper" aria-hidden="true">
				<div class="db-scale">
					{#each [-6, -12, -24, -48] as db}
						<div class="db-mark" style="bottom: {dbToPercent(db)}%">
							<span class="db-label">{db}</span>
						</div>
					{/each}
				</div>
				<div class="program-meter">
					<div class="peak-bar left">
						<div class="peak-fill" style="height: {dbToPercent(linearToDb(programLevels.peakL))}%"></div>
					</div>
					<div class="peak-bar right">
						<div class="peak-fill" style="height: {dbToPercent(linearToDb(programLevels.peakR))}%"></div>
					</div>
				</div>
			</div>

			<!-- Master fader -->
			<input
				type="range"
				class="fader"
				min="-60"
				max="12"
				step="0.5"
				value={crState.masterLevel}
				{...{orient: "vertical"}}
				aria-label="Master volume"
				oninput={(e) => setMasterLevel(parseFloat((e.target as HTMLInputElement).value))}
			/>
		</div>

		<span class="strip-db">{crState.masterLevel.toFixed(1)}</span>

		<!-- LUFS loudness readout -->
		<div class="lufs-readout">
			<div class="lufs-row">
				<span class="lufs-label">M</span>
				<span class="lufs-value" class:lufs-green={momentaryLufs >= -24 && momentaryLufs <= -14}
					class:lufs-yellow={momentaryLufs > -14 && momentaryLufs <= -10}
					class:lufs-red={momentaryLufs > -10 || (momentaryLufs < -28 && isFinite(momentaryLufs))}>
					{isFinite(momentaryLufs) ? momentaryLufs.toFixed(1) : '---'}
				</span>
			</div>
			<div class="lufs-row">
				<span class="lufs-label">S</span>
				<span class="lufs-value" class:lufs-green={shortTermLufs >= -24 && shortTermLufs <= -14}
					class:lufs-yellow={shortTermLufs > -14 && shortTermLufs <= -10}
					class:lufs-red={shortTermLufs > -10 || (shortTermLufs < -28 && isFinite(shortTermLufs))}>
					{isFinite(shortTermLufs) ? shortTermLufs.toFixed(1) : '---'}
				</span>
			</div>
			<div class="lufs-row">
				<span class="lufs-label">I</span>
				<span class="lufs-value" class:lufs-green={integratedLufs >= -24 && integratedLufs <= -14}
					class:lufs-yellow={integratedLufs > -14 && integratedLufs <= -10}
					class:lufs-red={integratedLufs > -10 || (integratedLufs < -28 && isFinite(integratedLufs))}>
					{isFinite(integratedLufs) ? integratedLufs.toFixed(1) : '---'}
				</span>
			</div>
			<span class="lufs-unit">LUFS</span>
		</div>
	</div>
	{/if}
</div>

<style>
	.audio-mixer {
		display: flex;
		gap: 3px;
		padding: 6px;
		overflow-x: auto;
		height: 100%;
	}

	.channel-strip,
	.master-strip {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 4px;
		padding: 6px 6px 5px;
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		min-width: 90px;
		transition:
			border-color var(--transition-fast),
			box-shadow var(--transition-normal);
	}

	.channel-strip.expanded {
		min-width: 180px;
	}

	.channel-strip.program {
		border-color: rgba(220, 38, 38, 0.35);
		box-shadow: 0 0 8px rgba(220, 38, 38, 0.1);
	}

	.channel-strip.preview {
		border-color: rgba(22, 163, 74, 0.35);
		box-shadow: 0 0 8px rgba(22, 163, 74, 0.1);
	}

	.master-strip {
		border-color: var(--border-strong);
		background: var(--bg-elevated);
	}

	.strip-label {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		letter-spacing: 0.04em;
		color: var(--text-primary);
		text-align: center;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		max-width: 80px;
	}

	.meter-fader {
		display: flex;
		gap: 3px;
		align-items: stretch;
		flex: 1;
		min-height: 0;
	}

	/* Meter wrapper (holds dB scale + stereo bars) */
	.meter-wrapper {
		position: relative;
		display: flex;
		align-items: stretch;
	}

	/* dB scale markings */
	.db-scale {
		position: absolute;
		top: 0;
		bottom: 0;
		left: 0;
		right: 0;
		z-index: var(--z-base);
		pointer-events: none;
	}

	.db-mark {
		position: absolute;
		left: 0;
		right: 0;
		height: 0;
		border-top: 1px solid rgba(255, 255, 255, 0.06);
	}

	.db-label {
		position: absolute;
		right: 100%;
		top: -0.35rem;
		margin-right: 2px;
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 400;
		color: var(--text-tertiary);
		opacity: 0.5;
		white-space: nowrap;
		line-height: 1;
	}

	/* Stereo meter (channel strips) */
	.stereo-meter {
		display: flex;
		gap: 1px;
		width: 9px;
		z-index: var(--z-above);
	}

	/* Program peak meter (L/R) */
	.program-meter {
		display: flex;
		gap: 2px;
		width: 22px;
		z-index: var(--z-above);
	}

	.peak-bar {
		flex: 1;
		background: var(--bg-base);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		position: relative;
		overflow: hidden;
	}

	.peak-fill {
		position: absolute;
		bottom: 0;
		left: 0;
		right: 0;
		background: linear-gradient(
			to top,
			#059669 0%,
			var(--color-success) 35%,
			#eab308 72%,
			var(--color-error) 92%
		);
		transition: height 0.06s linear;
	}

	.peak-hold-line {
		position: absolute;
		left: 0;
		right: 0;
		height: 2px;
		background: var(--text-primary);
		pointer-events: none;
	}

	.clip-dot {
		position: absolute;
		top: 1px;
		left: 50%;
		transform: translateX(-50%);
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: #ff0000;
		border: none;
		padding: 0;
		cursor: pointer;
		z-index: var(--z-above);
	}

	/* Vertical fader */
	.fader {
		writing-mode: vertical-lr;
		direction: rtl;
		width: 22px;
		height: 100%;
	}

	.fader::-webkit-slider-runnable-track {
		width: 4px;
		background: var(--bg-control);
		border-radius: var(--radius-xs);
		border: 1px solid var(--border-subtle);
	}

	.fader::-webkit-slider-thumb {
		width: 18px;
		height: 8px;
		border-radius: var(--radius-xs);
		background: linear-gradient(to bottom, #999, #666);
		border: 1px solid rgba(255, 255, 255, 0.15);
		margin-left: -8px;
		box-shadow: 0 1px 3px rgba(0, 0, 0, 0.5);
	}

	.fader::-webkit-slider-thumb:hover {
		background: linear-gradient(to bottom, #bbb, #888);
		transform: none;
	}

	.fader::-moz-range-track {
		width: 4px;
		background: var(--bg-control);
		border-radius: var(--radius-xs);
		border: 1px solid var(--border-subtle);
	}

	.fader::-moz-range-thumb {
		width: 18px;
		height: 8px;
		border-radius: var(--radius-xs);
		background: linear-gradient(to bottom, #999, #666);
		border: 1px solid rgba(255, 255, 255, 0.15);
		box-shadow: 0 1px 3px rgba(0, 0, 0, 0.5);
	}

	.fader::-moz-range-thumb:hover {
		background: linear-gradient(to bottom, #bbb, #888);
	}

	/* Strip buttons */
	.strip-buttons {
		display: flex;
		flex-direction: column;
		gap: 2px;
		width: 100%;
	}

	.strip-btn {
		padding: 2px 3px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-tertiary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.04em;
		text-align: center;
		transition:
			background var(--transition-fast),
			color var(--transition-fast),
			border-color var(--transition-fast);
	}

	.strip-btn:hover {
		background: var(--bg-hover);
		color: var(--text-secondary);
	}

	.pfl-btn.active {
		background: var(--accent-yellow-dim);
		color: var(--accent-yellow);
		border-color: rgba(234, 179, 8, 0.4);
	}

	.mute-btn.active {
		background: var(--tally-program-dim);
		color: var(--color-error);
		border-color: rgba(239, 68, 68, 0.4);
	}

	.afv-btn.active {
		background: var(--tally-preview-dim);
		color: var(--color-success);
		border-color: rgba(34, 197, 94, 0.4);
	}

	.eq-toggle-btn.active {
		background: rgba(99, 102, 241, 0.15);
		color: var(--accent-indigo);
		border-color: rgba(99, 102, 241, 0.4);
	}

	/* Trim control */
	.trim-control {
		display: flex;
		align-items: center;
		gap: 2px;
		width: 100%;
	}

	.trim-knob {
		flex: 1;
		height: 12px;
		accent-color: var(--text-secondary);
	}

	.trim-value {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		min-width: 28px;
		text-align: right;
	}

	.strip-db {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-tertiary);
	}

	/* EQ & Compressor section */
	.eq-comp-section {
		width: 100%;
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding-top: 4px;
		border-top: 1px solid var(--border-subtle);
	}

	.section-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
		text-transform: uppercase;
	}

	.eq-section,
	.comp-section {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.eq-band {
		display: flex;
		flex-direction: column;
		gap: 1px;
		padding: 2px 0;
	}

	.eq-band-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.eq-band-name {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-tertiary);
	}

	.eq-enable-btn {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		padding: 1px 4px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		background: var(--bg-elevated);
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.eq-enable-btn.active {
		background: rgba(99, 102, 241, 0.2);
		color: var(--accent-indigo);
		border-color: rgba(99, 102, 241, 0.4);
	}

	.eq-param {
		display: flex;
		align-items: center;
		gap: 2px;
	}

	.eq-param-label {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		min-width: 28px;
	}

	.eq-slider {
		flex: 1;
		height: 10px;
		accent-color: var(--text-secondary);
	}

	.eq-param-value {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		min-width: 32px;
		text-align: right;
	}

	/* Compressor bypass */
	.comp-bypassed .eq-param {
		opacity: 0.4;
		pointer-events: none;
	}

	.section-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.bypass-toggle {
		font-size: var(--text-xs);
		padding: 1px 5px;
		border-radius: var(--radius-sm);
		border: 1px solid var(--border-subtle);
		background: var(--bg-control);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 600;
		letter-spacing: 0.06em;
	}

	.bypass-active {
		background: color-mix(in srgb, var(--accent-green, #4caf50) 20%, transparent);
		color: var(--accent-green, #4caf50);
		border-color: var(--accent-green, #4caf50);
	}

	/* GR meter */
	.gr-meter {
		display: flex;
		align-items: center;
		gap: 3px;
		margin-top: 2px;
	}

	.gr-label {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		min-width: 16px;
	}

	.gr-bar {
		flex: 1;
		height: 6px;
		background: var(--bg-base);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		overflow: hidden;
	}

	.gr-fill {
		height: 100%;
		background: #f59e0b;
		transition: width 0.06s linear;
	}

	.gr-value {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		min-width: 24px;
		text-align: right;
	}

	/* Source delay section */
	.delay-section {
		display: flex;
		flex-direction: column;
		gap: 3px;
		border-top: 1px solid var(--border-subtle);
		padding-top: 6px;
		margin-top: 3px;
	}

	.param-value {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	/* Collapse toggle (visible at narrow viewports) */
	.collapse-toggle {
		display: none;
		align-items: center;
		gap: 4px;
		padding: 4px 8px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
	}

	.collapse-toggle:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.toggle-arrow {
		font-size: var(--text-xs);
	}

	@media (max-width: 1023px) {
		.collapse-toggle {
			display: flex;
		}
	}

	.audio-mixer.collapsed {
		gap: 0;
		padding: 4px;
	}

	.lufs-readout {
		display: flex;
		flex-direction: column;
		gap: 1px;
		padding: 4px;
		background: var(--bg-surface);
		border-radius: var(--radius-sm);
		font-family: var(--font-mono, monospace);
		font-size: var(--text-xs);
		min-width: 70px;
	}

	.lufs-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.lufs-label {
		color: var(--text-muted);
		font-weight: 600;
		width: 12px;
	}

	.lufs-value {
		color: var(--text-secondary);
		text-align: right;
	}

	.lufs-green {
		color: #16a34a;
	}

	.lufs-yellow {
		color: #ca8a04;
	}

	.lufs-red {
		color: #dc2626;
	}

	.lufs-unit {
		text-align: center;
		color: var(--text-muted);
		font-size: var(--text-xs);
		margin-top: 2px;
	}
</style>
