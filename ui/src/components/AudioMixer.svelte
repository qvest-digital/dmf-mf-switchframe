<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import {
		setLevel as apiSetLevel,
		setTrim as apiSetTrim,
		setMute as apiSetMute,
		setAFV as apiSetAFV,
		setPhaseInvert as apiSetPhaseInvert,
		setBalance as apiSetBalance,
		setMasterLevel as apiSetMasterLevel,
		setHPF as apiSetHPF,
		setEQ as apiSetEQ,
		setCompressor as apiSetCompressor,
		setGate as apiSetGate,
		setSourceDelay,
		setAudioDelay as apiSetAudioDelay,
	} from '$lib/api/switch-api';
	import { throttle } from '$lib/util/throttle';
	import { sortedSourceKeys } from '$lib/util/sort-sources';
	import { updatePeakHold as _updatePeakHold, isClipActive, CLIP_THRESHOLD_DB } from '$lib/audio/peak-hold';
	import type { PFLMode } from '$lib/audio/pfl';

	interface Props {
		state: ControlRoomState;
		sourceLevels?: Record<string, { peakL: number; peakR: number }>;
		programLevels?: { peakL: number; peakR: number };
		pflActiveSource?: string | null;
		pflMode?: PFLMode;
		pflManager?: { setAFLGain(sourceKey: string, linearGain: number): void } | null;
		selectedChannel?: string | null;
		audioDrift?: { channel: string; deltaDb: number } | null;
		onPFLToggle?: (sourceKey: string) => void;
		onStateUpdate?: (state: ControlRoomState) => void;
		onChannelSelect?: (sourceKey: string | null) => void;
	}

	let { state: crState, sourceLevels = {}, programLevels = { peakL: 0, peakR: 0 }, pflActiveSource = null, pflMode = 'off' as PFLMode, pflManager = null, selectedChannel = null, audioDrift = null, onPFLToggle, onStateUpdate, onChannelSelect }: Props = $props();

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
		// Update AFL gain in real time as fader moves
		if (pflActiveSource === source && pflMode === 'afl' && pflManager) {
			pflManager.setAFLGain(source, Math.pow(10, level / 20));
		}
	}

	// Keep AFL gain in sync with server-side fader level updates
	$effect(() => {
		if (pflActiveSource && pflMode === 'afl' && pflManager) {
			const channel = crState.audioChannels?.[pflActiveSource];
			if (channel) {
				pflManager.setAFLGain(pflActiveSource, Math.pow(10, channel.level / 20));
			}
		}
	});

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

	function setPhaseInvert(source: string, invert: boolean) {
		applyResult(apiSetPhaseInvert(source, invert));
	}

	/** Throttled balance API call -- max 20 calls/sec (50ms). */
	const setBalanceThrottled = throttle((source: string, balance: number) => {
		applyResult(apiSetBalance(source, balance));
	}, 50);

	/** Throttled master level API call -- max 20 calls/sec (50ms). */
	const setMasterLevelThrottled = throttle((level: number) => {
		applyResult(apiSetMasterLevel(level));
	}, 50);

	function setMasterLevel(level: number) {
		setMasterLevelThrottled(level);
	}

	/** Throttled HPF API call -- max 20 calls/sec (50ms). */
	const setHPFThrottled = throttle((source: string, frequency: number, enabled: boolean) => {
		applyResult(apiSetHPF(source, frequency, enabled));
	}, 50);

	/** Throttled EQ API call -- max 20 calls/sec (50ms). */
	const setEQThrottled = throttle((source: string, band: number, frequency: number, gain: number, q: number, enabled: boolean) => {
		applyResult(apiSetEQ(source, band, frequency, gain, q, enabled));
	}, 50);

	/** Throttled compressor API call -- max 20 calls/sec (50ms). */
	const setCompressorThrottled = throttle((source: string, threshold: number, ratio: number, attack: number, release: number, makeupGain: number) => {
		applyResult(apiSetCompressor(source, threshold, ratio, attack, release, makeupGain));
	}, 50);

	/** Throttled gate API call -- max 20 calls/sec (50ms). */
	const setGateThrottled = throttle((source: string, threshold: number, range: number, attack: number, release: number, hold: number, enabled: boolean) => {
		applyResult(apiSetGate(source, threshold, range, attack, release, hold, enabled));
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

	function toggleSelect(key: string) {
		onChannelSelect?.(selectedChannel === key ? null : key);
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
	{#if !ui.collapsed}
	<div class="mixer-strips">
	<button class="collapse-toggle" onclick={() => ui = { ...ui, collapsed: !ui.collapsed }} title={ui.collapsed ? 'Expand audio mixer' : 'Collapse audio mixer'}>
		<span class="toggle-arrow">{ui.collapsed ? '\u25B6' : '\u25BC'}</span>
		<span class="toggle-label">AUDIO</span>
		{#if audioDrift}
			<span class="drift-badge" title={`Engine A and Engine B audio levels differ by >${audioDrift.deltaDb}dB on ${audioDrift.channel}`}>&#9888; drift</span>
		{/if}
	</button>
	<!-- Channel strips -->
	{#each sortedKeys as key (key)}
		{@const channel = crState.audioChannels?.[key]!}
		{@const source = crState.sources[key]}
		{@const label = source?.label || key}
		{@const tally = crState.tallyState[key] ?? 'idle'}
		{@const peakLDb = channelPeakDb(channel?.peakL, sourceLevels[key]?.peakL)}
		{@const peakRDb = channelPeakDb(channel?.peakR, sourceLevels[key]?.peakR)}
		{@const isPfl = pflActiveSource === key && pflMode === 'pfl'}
		{@const isAfl = pflActiveSource === key && pflMode === 'afl'}
		{@const peakHold = getPeakHold(key, peakLDb, peakRDb)}
		{@const _tick = peakHoldTick}
		<div class="channel-strip" class:program={tally === 'program'} class:preview={tally === 'preview'} class:selected={selectedChannel === key}>
			{#if tally === 'program' || tally === 'preview'}
				<div class="tally-bar {tally}"></div>
			{/if}
			<span class="strip-label">{label}</span>
			{#if channel.hpfEnabled}
				<span class="strip-hpf-indicator">{channel.hpfFrequency ?? 80}Hz</span>
			{/if}

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
					class:pfl-active={isPfl}
					class:afl-active={isAfl}
					onclick={() => onPFLToggle?.(key)}
					title={isPfl ? 'After-Fader Listen' : isAfl ? 'Monitor off' : 'Pre-Fader Listen'}
				>
					{isAfl ? 'AFL' : 'PFL'}
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
					class="strip-btn phase-btn"
					class:active={channel.phaseInvert}
					onclick={() => setPhaseInvert(key, !channel.phaseInvert)}
					title="Phase invert"
				>
					&Oslash;
				</button>
				<button
					class="strip-btn hpf-btn"
					class:active={channel.hpfEnabled}
					onclick={() => setHPFThrottled(key, channel.hpfFrequency ?? 80, !channel.hpfEnabled)}
					title="High-pass filter"
				>
					{channel.hpfEnabled ? (channel.hpfFrequency ?? 80) : 'HPF'}
				</button>
				<button
					class="strip-btn eq-toggle-btn"
					class:active={selectedChannel === key}
					onclick={() => toggleSelect(key)}
					title="EQ & Dynamics"
				>
					EQ
				</button>
			</div>

			<span class="strip-db">{channel.level.toFixed(1)}</span>
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

		{#if crState.limiter}
			<div class="master-limiter">
				<div class="gr-meter master-gr">
					<div class="gr-fill" style="width: {Math.min((crState.limiter.gainReduction) / 12 * 100, 100)}%"></div>
					<span class="gr-label">LIM {crState.limiter.gainReduction > 0.1 ? '-' + crState.limiter.gainReduction.toFixed(1) : '0.0'}</span>
				</div>
			</div>
		{/if}
	</div>
	</div>

	{#if selectedChannel && crState.audioChannels?.[selectedChannel]}
		{@const ch = crState.audioChannels[selectedChannel]}
		<div class="channel-detail">
			<!-- Channel header -->
			<div class="detail-section detail-header">
				<span class="detail-channel-name">{crState.sources[selectedChannel]?.label || selectedChannel}</span>
				<button class="detail-close" onclick={() => onChannelSelect?.(null)} title="Close detail panel">&times;</button>
			</div>

			<!-- HPF -->
			<div class="detail-section detail-hpf">
				<div class="detail-section-header">
					<span class="detail-section-label">HPF</span>
					<button
						class="detail-toggle-btn"
						class:active={ch.hpfEnabled}
						onclick={() => setHPFThrottled(selectedChannel, ch.hpfFrequency ?? 80, !ch.hpfEnabled)}
					>{ch.hpfEnabled ? 'IN' : 'OUT'}</button>
				</div>
				<div class="hpf-param" class:disabled={!ch.hpfEnabled}>
					<label class="eq-param">
						<span class="eq-param-label">FREQ</span>
						<input type="range" class="detail-slider"
							min="20" max="500" step="1"
							value={ch.hpfFrequency ?? 80}
							oninput={(e) => setHPFThrottled(selectedChannel, parseFloat(e.currentTarget.value), ch.hpfEnabled ?? false)}
						/>
						<span class="eq-param-value">{ch.hpfFrequency ?? 80}Hz</span>
					</label>
				</div>
			</div>

			<!-- Gate -->
			<div class="detail-section detail-gate">
				<div class="detail-section-header">
					<span class="detail-section-label">GATE</span>
					<button
						class="detail-toggle-btn"
						class:active={ch.gateEnabled}
						onclick={() => {
							const g = ch.gate;
							setGateThrottled(selectedChannel, g?.threshold ?? -40, g?.range ?? -60, g?.attack ?? 0.5, g?.release ?? 100, g?.hold ?? 50, !ch.gateEnabled);
						}}
					>{ch.gateEnabled ? 'IN' : 'OUT'}</button>
				</div>
				<div class="gate-params" class:disabled={!ch.gateEnabled}>
					<div class="eq-param">
						<span class="eq-param-label">THRESH</span>
						<input type="range" class="detail-slider" min="-60" max="0" step="0.5"
							value={ch.gate?.threshold ?? -40}
							oninput={(e) => {
								const g = ch.gate;
								setGateThrottled(selectedChannel, parseFloat(e.currentTarget.value), g?.range ?? -60, g?.attack ?? 0.5, g?.release ?? 100, g?.hold ?? 50, ch.gateEnabled ?? false);
							}}
						/>
						<span class="eq-param-value">{(ch.gate?.threshold ?? -40).toFixed(0)}</span>
					</div>
					<div class="eq-param">
						<span class="eq-param-label">RANGE</span>
						<input type="range" class="detail-slider" min="-80" max="-20" step="1"
							value={ch.gate?.range ?? -60}
							oninput={(e) => {
								const g = ch.gate;
								setGateThrottled(selectedChannel, g?.threshold ?? -40, parseFloat(e.currentTarget.value), g?.attack ?? 0.5, g?.release ?? 100, g?.hold ?? 50, ch.gateEnabled ?? false);
							}}
						/>
						<span class="eq-param-value">{(ch.gate?.range ?? -60).toFixed(0)}</span>
					</div>
					<div class="eq-param">
						<span class="eq-param-label">ATK</span>
						<input type="range" class="detail-slider" min="0.01" max="10" step="0.01"
							value={ch.gate?.attack ?? 0.5}
							oninput={(e) => {
								const g = ch.gate;
								setGateThrottled(selectedChannel, g?.threshold ?? -40, g?.range ?? -60, parseFloat(e.currentTarget.value), g?.release ?? 100, g?.hold ?? 50, ch.gateEnabled ?? false);
							}}
						/>
						<span class="eq-param-value">{(ch.gate?.attack ?? 0.5).toFixed(1)}</span>
					</div>
					<div class="eq-param">
						<span class="eq-param-label">REL</span>
						<input type="range" class="detail-slider" min="10" max="500" step="1"
							value={ch.gate?.release ?? 100}
							oninput={(e) => {
								const g = ch.gate;
								setGateThrottled(selectedChannel, g?.threshold ?? -40, g?.range ?? -60, g?.attack ?? 0.5, parseFloat(e.currentTarget.value), g?.hold ?? 50, ch.gateEnabled ?? false);
							}}
						/>
						<span class="eq-param-value">{(ch.gate?.release ?? 100).toFixed(0)}</span>
					</div>
					<div class="eq-param">
						<span class="eq-param-label">HOLD</span>
						<input type="range" class="detail-slider" min="0" max="500" step="1"
							value={ch.gate?.hold ?? 50}
							oninput={(e) => {
								const g = ch.gate;
								setGateThrottled(selectedChannel, g?.threshold ?? -40, g?.range ?? -60, g?.attack ?? 0.5, g?.release ?? 100, parseFloat(e.currentTarget.value), ch.gateEnabled ?? false);
							}}
						/>
						<span class="eq-param-value">{(ch.gate?.hold ?? 50).toFixed(0)}</span>
					</div>
				</div>
				{#if ch.gateEnabled && (ch.gateReduction ?? 0) > 0.1}
					<div class="gr-meter">
						<div class="gr-fill" style="width: {Math.min((ch.gateReduction ?? 0) / 80 * 100, 100)}%"></div>
						<span class="gr-label">GR {(ch.gateReduction ?? 0) > 0 ? '-' + (ch.gateReduction ?? 0).toFixed(1) : '0.0'}</span>
					</div>
				{/if}
			</div>

			<!-- 3-band EQ -->
			<div class="detail-section detail-eq">
				<span class="detail-section-label">EQ</span>
				<div class="eq-bands">
					{#each [0, 1, 2] as bandIdx}
						{@const band = ch.eq[bandIdx]}
						<div class="eq-band">
							<button
								class="eq-band-toggle"
								class:active={band.enabled}
								onclick={() => setEQThrottled(selectedChannel, bandIdx, band.frequency, band.gain, band.q, !band.enabled)}
							>{EQ_BAND_NAMES[bandIdx]}</button>
							<div class="eq-param">
								<span class="eq-param-label">FREQ</span>
								<input type="range"
									class="detail-slider"
									min={bandIdx === 0 ? 80 : bandIdx === 1 ? 200 : 1000}
									max={bandIdx === 0 ? 1000 : bandIdx === 1 ? 8000 : 16000}
									step="1"
									value={band.frequency}
									oninput={(e) => setEQThrottled(selectedChannel, bandIdx, parseFloat(e.currentTarget.value), band.gain, band.q, band.enabled)}
								/>
								<span class="eq-param-value">{band.frequency >= 1000 ? (band.frequency / 1000).toFixed(1) + 'k' : band.frequency}</span>
							</div>
							<div class="eq-param">
								<span class="eq-param-label">GAIN</span>
								<input type="range"
									class="detail-slider"
									min="-12"
									max="12"
									step="0.5"
									value={band.gain}
									oninput={(e) => setEQThrottled(selectedChannel, bandIdx, band.frequency, parseFloat(e.currentTarget.value), band.q, band.enabled)}
								/>
								<span class="eq-param-value">{band.gain > 0 ? '+' : ''}{band.gain.toFixed(1)}</span>
							</div>
							<div class="eq-param">
								<span class="eq-param-label">Q</span>
								<input type="range"
									class="detail-slider"
									min="0.5"
									max="4.0"
									step="0.1"
									value={band.q}
									oninput={(e) => setEQThrottled(selectedChannel, bandIdx, band.frequency, band.gain, parseFloat(e.currentTarget.value), band.enabled)}
								/>
								<span class="eq-param-value">{band.q.toFixed(1)}</span>
							</div>
						</div>
					{/each}
				</div>
			</div>

			<!-- Compressor -->
			<div class="detail-section detail-comp">
				<div class="comp-header">
					<span class="detail-section-label">COMP</span>
					<button
						class="comp-bypass-btn"
						class:active={!(compBypass[selectedChannel] ?? false)}
						onclick={() => toggleCompBypass(selectedChannel, ch)}
					>{compBypass[selectedChannel] ? 'OUT' : 'IN'}</button>
				</div>
				<div class="comp-params" class:bypassed={compBypass[selectedChannel] ?? false}>
					<div class="comp-param">
						<span class="eq-param-label">THRESH</span>
						<input type="range" class="detail-slider" min="-40" max="0" step="0.5" value={ch.compressor.threshold}
							oninput={(e) => setCompressorThrottled(selectedChannel, parseFloat(e.currentTarget.value), ch.compressor.ratio, ch.compressor.attack, ch.compressor.release, ch.compressor.makeupGain)}
						/>
						<span class="eq-param-value">{ch.compressor.threshold.toFixed(0)}</span>
					</div>
					<div class="comp-param">
						<span class="eq-param-label">RATIO</span>
						<input type="range" class="detail-slider" min="1" max="20" step="0.5" value={ch.compressor.ratio}
							oninput={(e) => setCompressorThrottled(selectedChannel, ch.compressor.threshold, parseFloat(e.currentTarget.value), ch.compressor.attack, ch.compressor.release, ch.compressor.makeupGain)}
						/>
						<span class="eq-param-value">{ch.compressor.ratio.toFixed(1)}</span>
					</div>
					<div class="comp-param">
						<span class="eq-param-label">ATK</span>
						<input type="range" class="detail-slider" min="0.1" max="100" step="0.1" value={ch.compressor.attack}
							oninput={(e) => setCompressorThrottled(selectedChannel, ch.compressor.threshold, ch.compressor.ratio, parseFloat(e.currentTarget.value), ch.compressor.release, ch.compressor.makeupGain)}
						/>
						<span class="eq-param-value">{ch.compressor.attack.toFixed(1)}</span>
					</div>
					<div class="comp-param">
						<span class="eq-param-label">REL</span>
						<input type="range" class="detail-slider" min="10" max="1000" step="1" value={ch.compressor.release}
							oninput={(e) => setCompressorThrottled(selectedChannel, ch.compressor.threshold, ch.compressor.ratio, ch.compressor.attack, parseFloat(e.currentTarget.value), ch.compressor.makeupGain)}
						/>
						<span class="eq-param-value">{ch.compressor.release.toFixed(0)}</span>
					</div>
					<div class="comp-param">
						<span class="eq-param-label">MAKEUP</span>
						<input type="range" class="detail-slider" min="0" max="24" step="0.5" value={ch.compressor.makeupGain}
							oninput={(e) => setCompressorThrottled(selectedChannel, ch.compressor.threshold, ch.compressor.ratio, ch.compressor.attack, ch.compressor.release, parseFloat(e.currentTarget.value))}
						/>
						<span class="eq-param-value">{ch.compressor.makeupGain.toFixed(1)}</span>
					</div>
				</div>
				<div class="gr-meter">
					<div class="gr-fill" style="width: {grToPercent(ch.gainReduction)}%"></div>
					<span class="gr-label">GR {ch.gainReduction > 0 ? '-' + ch.gainReduction.toFixed(1) : '0.0'}</span>
				</div>
			</div>

			<!-- Balance -->
			<div class="detail-section detail-bal">
				<span class="detail-section-label">BAL</span>
				<div class="balance-control">
					<span class="bal-label-l">L</span>
					<input type="range" class="detail-slider balance-slider"
						min="-1" max="1" step="0.01"
						value={ch.balance ?? 0}
						oninput={(e) => setBalanceThrottled(selectedChannel, parseFloat(e.currentTarget.value))}
					/>
					<span class="bal-label-r">R</span>
				</div>
				<span class="balance-value">
					{#if (ch.balance ?? 0) === 0}C
					{:else if (ch.balance ?? 0) < 0}L{Math.round(Math.abs(ch.balance ?? 0) * 100)}
					{:else}R{Math.round((ch.balance ?? 0) * 100)}
					{/if}
				</span>
			</div>

			<!-- Delays -->
			<div class="detail-section detail-delays">
				<span class="detail-section-label">DELAY</span>
				<div class="delay-param">
					<span class="eq-param-label">VIDEO</span>
					<input type="range" class="detail-slider" min="0" max="500" step="1"
						value={crState.sources?.[selectedChannel]?.delayMs ?? 0}
						oninput={(e) => setDelayThrottled(selectedChannel, parseFloat(e.currentTarget.value))}
					/>
					<span class="eq-param-value">{crState.sources?.[selectedChannel]?.delayMs ?? 0}ms</span>
				</div>
				<div class="delay-param">
					<span class="eq-param-label">AUDIO</span>
					<input type="range" class="detail-slider" min="0" max="500" step="1"
						value={ch.audioDelayMs ?? 0}
						oninput={(e) => setAudioDelayThrottled(selectedChannel, parseFloat(e.currentTarget.value))}
					/>
					<span class="eq-param-value">{ch.audioDelayMs ?? 0}ms</span>
				</div>
			</div>
		</div>
	{/if}
	{:else}
	<button class="collapse-toggle" onclick={() => ui = { ...ui, collapsed: !ui.collapsed }} title={ui.collapsed ? 'Expand audio mixer' : 'Collapse audio mixer'}>
		<span class="toggle-arrow">{ui.collapsed ? '\u25B6' : '\u25BC'}</span>
		<span class="toggle-label">AUDIO</span>
		{#if audioDrift}
			<span class="drift-badge" title={`Engine A and Engine B audio levels differ by >${audioDrift.deltaDb}dB on ${audioDrift.channel}`}>&#9888; drift</span>
		{/if}
	</button>
	{/if}
</div>

<style>
	.audio-mixer {
		display: flex;
		flex-direction: column;
		height: 100%;
	}

	.mixer-strips {
		display: flex;
		gap: 3px;
		padding: 6px;
		overflow-x: auto;
		flex: 1;
		min-height: 0;
	}

	.channel-strip,
	.master-strip {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 2px;
		padding: 4px 4px 3px;
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		min-width: 80px;
		overflow: hidden;
		transition:
			border-color var(--transition-fast),
			box-shadow var(--transition-normal);
	}

	.channel-strip.program {
		border-color: #dc2626;
		border-width: 2px;
		box-shadow: 0 0 12px rgba(220, 38, 38, 0.3), inset 0 0 8px rgba(220, 38, 38, 0.06);
	}

	.channel-strip.preview {
		border-color: #16a34a;
		border-width: 2px;
		box-shadow: 0 0 12px rgba(22, 163, 74, 0.3), inset 0 0 8px rgba(22, 163, 74, 0.06);
	}

	.tally-bar {
		width: 100%;
		height: 3px;
		border-radius: 1px;
		flex-shrink: 0;
	}
	.tally-bar.program {
		background: #dc2626;
	}
	.tally-bar.preview {
		background: #16a34a;
	}

	.channel-strip.selected {
		background: rgba(99, 102, 241, 0.05);
		border-bottom: 2px solid rgba(99, 102, 241, 0.5);
	}

	.master-strip {
		border-color: var(--border-strong);
		background: var(--bg-elevated);
	}

	.strip-label {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		letter-spacing: 0.04em;
		color: var(--text-primary);
		text-align: center;
		line-height: 1;
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
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 1px;
		width: 100%;
	}

	.strip-btn {
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

	.pfl-btn.pfl-active {
		background: #eab308;
		border-color: #eab308;
		color: black;
	}

	.pfl-btn.afl-active {
		background: #f59e0b;
		border-color: #f59e0b;
		color: black;
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

	.phase-btn.active {
		background: var(--tally-program-dim);
		border-color: rgba(239, 68, 68, 0.4);
		color: var(--color-error);
	}

	.hpf-btn {
		font-size: 0.6rem;
	}

	.hpf-btn.active {
		background: rgba(20, 184, 166, 0.15);
		color: rgb(20, 184, 166);
		border-color: rgba(20, 184, 166, 0.4);
	}

	.eq-toggle-btn.active {
		background: rgba(99, 102, 241, 0.15);
		color: var(--accent-indigo);
		border-color: rgba(99, 102, 241, 0.4);
	}

	/* Trim control — small, set-and-forget like a hardware trim pot */
	.trim-control {
		display: flex;
		align-items: center;
		gap: 1px;
		width: 100%;
		height: 14px;
		flex-shrink: 0;
	}

	.trim-knob {
		flex: 1;
		height: 6px;
		accent-color: var(--text-tertiary);
	}

	.trim-value {
		font-family: var(--font-mono);
		font-size: 8px;
		color: var(--text-tertiary);
		min-width: 22px;
		text-align: right;
	}

	.strip-db {
		font-family: var(--font-mono);
		font-size: 9px;
		font-weight: 500;
		color: var(--text-tertiary);
		line-height: 1;
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

	.drift-badge {
		display: inline-flex;
		align-items: center;
		padding: 1px 5px;
		background: rgba(245, 158, 11, 0.15);
		color: #f59e0b;
		font-size: 10px;
		font-weight: 600;
		letter-spacing: 0.03em;
		border: 1px solid rgba(245, 158, 11, 0.3);
		border-radius: 3px;
		line-height: 1.2;
		cursor: help;
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

	.channel-detail {
		display: flex;
		align-items: stretch;
		gap: 0;
		padding: 0;
		border-top: 1px solid var(--border-default);
		background: rgba(255, 255, 255, 0.02);
		min-height: 105px;
		overflow-x: auto;
		overflow-y: auto;
		flex-shrink: 0;
	}

	.detail-section {
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: 4px 8px;
		flex-shrink: 0;
	}

	.detail-section + .detail-section {
		border-left: 1px solid var(--border-subtle);
	}

	.detail-section-label {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 600;
		text-transform: uppercase;
		color: var(--text-tertiary);
		letter-spacing: 0.06em;
	}

	.detail-header {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 4px;
		width: 48px;
		min-width: 48px;
		max-width: 48px;
		flex-shrink: 0;
	}

	.detail-channel-name {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 600;
		color: var(--text-primary);
		max-width: 44px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		text-align: center;
	}

	.detail-close {
		background: none;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		color: var(--text-tertiary);
		font-size: 14px;
		line-height: 1;
		padding: 1px 5px;
		cursor: pointer;
	}

	.detail-close:hover {
		color: var(--text-primary);
		border-color: var(--border-default);
	}

	/* EQ section */
	.detail-eq {
		min-width: 200px;
	}

	.eq-bands {
		display: flex;
		gap: 4px;
	}

	.eq-band {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 60px;
	}

	.eq-band-toggle {
		font-family: var(--font-mono);
		font-size: 9px;
		font-weight: 700;
		padding: 1px 4px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		background: rgba(255, 255, 255, 0.04);
		color: var(--text-tertiary);
		cursor: pointer;
		text-align: center;
	}

	.eq-band-toggle.active {
		background: rgba(99, 102, 241, 0.15);
		border-color: rgba(99, 102, 241, 0.4);
		color: rgb(99, 102, 241);
	}

	.eq-param {
		display: flex;
		align-items: center;
		gap: 2px;
	}

	.eq-param-label {
		font-family: var(--font-ui);
		font-size: 9px;
		text-transform: uppercase;
		color: var(--text-tertiary);
		min-width: 18px;
		flex-shrink: 0;
	}

	.eq-param-value {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-secondary);
		text-align: right;
		min-width: 28px;
		flex-shrink: 0;
	}

	.detail-slider {
		flex: 1;
		height: 8px;
		accent-color: rgb(99, 102, 241);
		min-width: 32px;
	}

	/* Compressor section */
	.detail-comp {
		min-width: 180px;
	}

	.comp-header {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.comp-bypass-btn {
		font-family: var(--font-mono);
		font-size: 9px;
		font-weight: 700;
		padding: 1px 4px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		background: rgba(255, 255, 255, 0.04);
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.comp-bypass-btn.active {
		background: rgba(99, 102, 241, 0.15);
		border-color: rgba(99, 102, 241, 0.4);
		color: rgb(99, 102, 241);
	}

	.comp-params {
		display: flex;
		gap: 6px;
	}

	.comp-params.bypassed {
		opacity: 0.35;
		pointer-events: none;
	}

	.comp-param {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 1px;
	}

	.comp-param .detail-slider {
		writing-mode: horizontal-tb;
		width: 40px;
		min-width: 32px;
	}

	.gr-meter {
		position: relative;
		height: 6px;
		background: var(--bg-base);
		border-radius: var(--radius-xs);
		overflow: hidden;
		margin-top: 2px;
	}

	.gr-fill {
		position: absolute;
		top: 0;
		right: 0;
		height: 100%;
		background: #f59e0b;
		border-radius: var(--radius-xs);
		transition: width 0.1s linear;
	}

	.gr-label {
		position: absolute;
		right: 2px;
		top: -1px;
		font-family: var(--font-mono);
		font-size: 7px;
		color: var(--text-tertiary);
		line-height: 1;
		pointer-events: none;
	}

	/* Delays section */
	.detail-delays {
		min-width: 120px;
	}

	.delay-param {
		display: flex;
		align-items: center;
		gap: 2px;
	}

	.delay-param .eq-param-value {
		min-width: 36px;
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

	/* HPF indicator on strip */
	.strip-hpf-indicator {
		font-family: var(--font-mono);
		font-size: 8px;
		color: rgba(20, 184, 166, 0.8);
		margin-top: -2px;
	}

	/* Detail panel toggle buttons (HPF IN/OUT, Gate IN/OUT) */
	.disabled {
		opacity: 0.35;
		pointer-events: none;
	}

	.detail-section-header {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 2px;
	}

	.detail-toggle-btn {
		font-family: var(--font-mono);
		font-size: 9px;
		font-weight: 600;
		padding: 1px 6px;
		border: 1px solid rgba(255,255,255,0.1);
		border-radius: 2px;
		background: rgba(255,255,255,0.04);
		color: var(--text-tertiary);
		cursor: pointer;
	}
	.detail-toggle-btn.active {
		background: rgba(99, 102, 241, 0.15);
		border-color: rgba(99, 102, 241, 0.4);
		color: rgb(165, 167, 252);
	}

	/* Gate params layout */
	.gate-params {
		display: flex;
		gap: 4px;
	}

	.gate-params .eq-param {
		flex-direction: column;
		align-items: center;
		gap: 1px;
	}

	.gate-params .detail-slider {
		writing-mode: horizontal-tb;
		width: 40px;
		min-width: 32px;
	}

	.gate-params .eq-param-label {
		min-width: unset;
	}

	/* Balance controls */
	.balance-control {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.bal-label-l, .bal-label-r {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
	}

	.balance-slider {
		accent-color: rgb(99, 102, 241);
	}

	.balance-value {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-secondary);
		text-align: center;
		min-width: 24px;
	}

	/* Master limiter GR */
	.master-limiter {
		margin-top: 4px;
	}

	.master-gr .gr-fill {
		background: var(--status-crit);
	}
</style>
