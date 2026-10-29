<script lang="ts">
	import { holdReload } from '$lib/util/activity';
	import type { ControlRoomState, CorrectorState, CurvePoint } from '$lib/api/types';
	import {
		setCorrector, resetCorrector, saveCustomLook,
		setSourceCorrector, resetSourceCorrector,
	} from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import { sortedSourceKeys } from '$lib/util/sort-sources';
	import {
		ICON_CHEVRON_DOWN,
		ICON_CHEVRON_RIGHT,
		ICON_SAVE,
		ICON_CLOSE,
		renderIcon,
	} from '$lib/icons';
	import { onDestroy } from 'svelte';
	import ColorWheel from './ColorWheel.svelte';
	import CurveEditor from './CurveEditor.svelte';

	let { state: crState }: { state: ControlRoomState } = $props();

	// ── Source selection ──
	let selectedSource = $state<string>('program');

	// Derive sorted source list (non-virtual only)
	let sourceEntries = $derived.by(() => {
		const keys = sortedSourceKeys(crState.sources);
		return keys
			.filter(k => !crState.sources[k]?.isVirtual)
			.map(k => ({ key: k, label: crState.sources[k]?.label || k }));
	});

	function hasCorrectionForSource(key: string): boolean {
		return crState.sources[key]?.corrector != null;
	}

	// ── Default corrector factory ──
	function defaultCorrector(): CorrectorState {
		return {
			lift: [0, 0, 0],
			gamma: [1, 1, 1],
			gain: [1, 1, 1],
			temperature: 0,
			tint: 0,
			saturation: 1,
			contrast: 0,
			shadows: [0, 0, 0],
			midtones: [0, 0, 0],
			highlights: [0, 0, 0],
			curveMaster: [{ x: 0, y: 0 }, { x: 1, y: 1 }],
			curveR: [{ x: 0, y: 0 }, { x: 1, y: 1 }],
			curveG: [{ x: 0, y: 0 }, { x: 1, y: 1 }],
			curveB: [{ x: 0, y: 0 }, { x: 1, y: 1 }],
			sharpness: 0,
		};
	}

	// ── Local corrector state ──
	let corrector = $state<CorrectorState>(defaultCorrector());

	// Local UI values for sliders that need scaling
	let satUI = $state(100);
	let contrastUI = $state(0);
	let sharpnessUI = $state(0);

	// Guard against server state overwriting in-progress user edits
	let lastUserEdit = 0;
	const SYNC_GUARD_MS = 1000;

	function markDirty() {
		lastUserEdit = Date.now();
	}

	// Sync corrector UI values from a corrector state
	function syncUIFromCorrector(c: CorrectorState) {
		corrector = {
			...c,
			curveMaster: c.curveMaster ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }],
			curveR: c.curveR ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }],
			curveG: c.curveG ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }],
			curveB: c.curveB ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }],
		};
		satUI = Math.round(c.saturation * 100);
		contrastUI = Math.round(c.contrast * 100);
		sharpnessUI = Math.round(c.sharpness * 100);
	}

	// Switch source selection
	function selectSource(key: string) {
		clearTimeout(sendTimeout);
		if (key !== 'program') {
			saveLookExpanded = false;
			lookNameDraft = '';
		}
		selectedSource = key;
		if (key === 'program') {
			syncUIFromCorrector(crState.colorGrade?.corrector ?? defaultCorrector());
		} else {
			const src = crState.sources[key];
			syncUIFromCorrector(src?.corrector ?? defaultCorrector());
		}
	}

	// Sync from server state (skipped while user is actively editing)
	$effect(() => {
		if (Date.now() - lastUserEdit < SYNC_GUARD_MS) return;
		if (selectedSource === 'program') {
			const sc = crState.colorGrade?.corrector;
			if (sc) syncUIFromCorrector(sc);
		} else {
			const src = crState.sources[selectedSource];
			const sc = src?.corrector;
			if (sc) syncUIFromCorrector(sc);
			// If source corrector was reset (deleted), revert to defaults
			else if (src && !sc) syncUIFromCorrector(defaultCorrector());
		}
	});

	// ── Curve channel tabs ──
	type CurveChannel = 'master' | 'r' | 'g' | 'b';
	let curveChannel = $state<CurveChannel>('master');

	const curveColors: Record<CurveChannel, string> = {
		master: '#ccc',
		r: '#e55',
		g: '#5e5',
		b: '#55e',
	};

	let activeCurvePoints = $derived.by(() => {
		switch (curveChannel) {
			case 'master': return corrector.curveMaster ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }];
			case 'r': return corrector.curveR ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }];
			case 'g': return corrector.curveG ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }];
			case 'b': return corrector.curveB ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }];
		}
	});

	// ── UI display values for zone sliders ──
	let shadowR = $derived(Math.round(corrector.shadows[0] * 100));
	let shadowG = $derived(Math.round(corrector.shadows[1] * 100));
	let shadowB = $derived(Math.round(corrector.shadows[2] * 100));
	let midR = $derived(Math.round(corrector.midtones[0] * 100));
	let midG = $derived(Math.round(corrector.midtones[1] * 100));
	let midB = $derived(Math.round(corrector.midtones[2] * 100));
	let highR = $derived(Math.round(corrector.highlights[0] * 100));
	let highG = $derived(Math.round(corrector.highlights[1] * 100));
	let highB = $derived(Math.round(corrector.highlights[2] * 100));

	// ── Zones disclosure + save look panel ──
	let zonesOpen = $state(false);
	let saveLookExpanded = $state(false);
	let lookNameDraft = $state('');
	$effect(() => { if (saveLookExpanded) return holdReload('unsaved look'); });

	// ── Debounced send to server ──
	let sendTimeout: ReturnType<typeof setTimeout>;
	function sendCorrector() {
		clearTimeout(sendTimeout);
		sendTimeout = setTimeout(async () => {
			try {
				if (selectedSource === 'program') {
					await setCorrector(corrector);
				} else {
					await setSourceCorrector(selectedSource, corrector);
				}
			} catch (err) {
				notify('error', `Corrector update failed: ${err instanceof Error ? err.message : 'unknown'}`);
			}
		}, 200);
	}

	onDestroy(() => clearTimeout(sendTimeout));

	// ── Wheel handlers ──
	function handleLiftChange(rgb: [number, number, number]) {
		markDirty();
		corrector.lift = rgb;
		sendCorrector();
	}

	function handleGammaChange(rgb: [number, number, number]) {
		markDirty();
		corrector.gamma = rgb;
		sendCorrector();
	}

	function handleGainChange(rgb: [number, number, number]) {
		markDirty();
		corrector.gain = rgb;
		sendCorrector();
	}

	// ── Slider handlers ──
	function handleSliderInput() {
		markDirty();
		sendCorrector();
	}

	function handleSatInput() {
		markDirty();
		corrector.saturation = satUI / 100;
		sendCorrector();
	}

	function handleContrastInput() {
		markDirty();
		corrector.contrast = contrastUI / 100;
		sendCorrector();
	}

	function handleSharpnessInput() {
		markDirty();
		corrector.sharpness = sharpnessUI / 100;
		sendCorrector();
	}

	// ── Zone handlers ──
	function setShadow(channel: 0 | 1 | 2, uiVal: number) {
		markDirty();
		corrector.shadows[channel] = uiVal / 100;
		sendCorrector();
	}

	function setMidtone(channel: 0 | 1 | 2, uiVal: number) {
		markDirty();
		corrector.midtones[channel] = uiVal / 100;
		sendCorrector();
	}

	function setHighlight(channel: 0 | 1 | 2, uiVal: number) {
		markDirty();
		corrector.highlights[channel] = uiVal / 100;
		sendCorrector();
	}

	function handleCurveChange(pts: CurvePoint[]) {
		markDirty();
		switch (curveChannel) {
			case 'master': corrector.curveMaster = pts; break;
			case 'r': corrector.curveR = pts; break;
			case 'g': corrector.curveG = pts; break;
			case 'b': corrector.curveB = pts; break;
		}
		sendCorrector();
	}

	// ── Reset / Save ──
	async function handleReset() {
		try {
			if (selectedSource === 'program') {
				await resetCorrector();
			} else {
				await resetSourceCorrector(selectedSource);
			}
			syncUIFromCorrector(defaultCorrector());
		} catch (err) {
			notify('error', `Reset failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	function openSaveLook() {
		saveLookExpanded = true;
		lookNameDraft = '';
	}

	function cancelSaveLook() {
		saveLookExpanded = false;
		lookNameDraft = '';
	}

	async function confirmSaveLook() {
		const name = lookNameDraft.trim();
		if (!name) return;
		try {
			await saveCustomLook(name);
			notify('info', `Saved "${name}"`);
			saveLookExpanded = false;
			lookNameDraft = '';
		} catch (err) {
			notify('error', `Save failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	function fmtSlider(v: number): string {
		return v.toFixed(0);
	}

	function toggleZones() {
		zonesOpen = !zonesOpen;
	}
</script>

<div class="color-tab">
	<!-- Column 1: Source List -->
	<div class="source-list">
		<div class="source-list-scroll">
			{#each sourceEntries as src}
				<button
					class="source-btn"
					class:active={selectedSource === src.key}
					class:corrected={hasCorrectionForSource(src.key)}
					onclick={() => selectSource(src.key)}
				>
					{#if hasCorrectionForSource(src.key)}
						<span class="correction-dot"></span>
					{/if}
					<span class="source-label">{src.label}</span>
				</button>
			{/each}
		</div>
		<div class="source-divider"></div>
		<button
			class="source-btn pgm-btn"
			class:active={selectedSource === 'program'}
			onclick={() => selectSource('program')}
		>PGM</button>
	</div>

	<!-- Column 2: Controls (wheels, sliders, zones) -->
	<div class="controls-col">
		<div class="controls-fixed">
			<div class="wheels-row">
				<ColorWheel label="LIFT" size={70} value={corrector.lift} onchange={handleLiftChange} min={-1} max={1} />
				<ColorWheel label="GAMMA" size={70} value={corrector.gamma} onchange={handleGammaChange} min={0.1} max={4} />
				<ColorWheel label="GAIN" size={70} value={corrector.gain} onchange={handleGainChange} min={0} max={4} />
			</div>
		<div class="sliders-grid">
			<div class="slider-row">
				<span class="slider-label">Temp</span>
				<input type="range" min="-100" max="100" step="1"
					bind:value={corrector.temperature}
					oninput={handleSliderInput}
				/>
				<span class="slider-value">{fmtSlider(corrector.temperature)}</span>
			</div>
			<div class="slider-row">
				<span class="slider-label">Sat</span>
				<input type="range" min="0" max="200" step="1"
					bind:value={satUI}
					oninput={handleSatInput}
				/>
				<span class="slider-value">{fmtSlider(satUI)}</span>
			</div>
			<div class="slider-row">
				<span class="slider-label">Tint</span>
				<input type="range" min="-100" max="100" step="1"
					bind:value={corrector.tint}
					oninput={handleSliderInput}
				/>
				<span class="slider-value">{fmtSlider(corrector.tint)}</span>
			</div>
			<div class="slider-row">
				<span class="slider-label">Cont</span>
				<input type="range" min="-100" max="100" step="1"
					bind:value={contrastUI}
					oninput={handleContrastInput}
				/>
				<span class="slider-value">{fmtSlider(contrastUI)}</span>
			</div>
			<div class="slider-row">
				<span class="slider-label">Sharp</span>
				<input type="range" min="0" max="100" step="1"
					bind:value={sharpnessUI}
					oninput={handleSharpnessInput}
				/>
				<span class="slider-value">{fmtSlider(sharpnessUI)}</span>
			</div>
		</div>
		</div>

		<div class="controls-scroll">
			<div class="zones-disclosure">
				<button
					type="button"
					class="zones-disclosure-trigger"
					onclick={toggleZones}
					aria-expanded={zonesOpen}
				>
					<span class="zones-chevron" aria-hidden="true">
						{@html renderIcon(zonesOpen ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 12)}
					</span>
					<span class="zones-disclosure-title">COLOR ZONES</span>
				</button>
				{#if zonesOpen}
					<div class="zones-panel">
						<div class="zone-col">
							<span class="zone-title">SHD</span>
							<div class="zone-ch"><span class="ch-label ch-r">R</span><input type="range" min="-100" max="100" class="slider-r" value={shadowR} oninput={(e) => setShadow(0, Number((e.target as HTMLInputElement).value))} /></div>
							<div class="zone-ch"><span class="ch-label ch-g">G</span><input type="range" min="-100" max="100" class="slider-g" value={shadowG} oninput={(e) => setShadow(1, Number((e.target as HTMLInputElement).value))} /></div>
							<div class="zone-ch"><span class="ch-label ch-b">B</span><input type="range" min="-100" max="100" class="slider-b" value={shadowB} oninput={(e) => setShadow(2, Number((e.target as HTMLInputElement).value))} /></div>
						</div>
						<div class="zone-col">
							<span class="zone-title">MID</span>
							<div class="zone-ch"><span class="ch-label ch-r">R</span><input type="range" min="-100" max="100" class="slider-r" value={midR} oninput={(e) => setMidtone(0, Number((e.target as HTMLInputElement).value))} /></div>
							<div class="zone-ch"><span class="ch-label ch-g">G</span><input type="range" min="-100" max="100" class="slider-g" value={midG} oninput={(e) => setMidtone(1, Number((e.target as HTMLInputElement).value))} /></div>
							<div class="zone-ch"><span class="ch-label ch-b">B</span><input type="range" min="-100" max="100" class="slider-b" value={midB} oninput={(e) => setMidtone(2, Number((e.target as HTMLInputElement).value))} /></div>
						</div>
						<div class="zone-col">
							<span class="zone-title">HI</span>
							<div class="zone-ch"><span class="ch-label ch-r">R</span><input type="range" min="-100" max="100" class="slider-r" value={highR} oninput={(e) => setHighlight(0, Number((e.target as HTMLInputElement).value))} /></div>
							<div class="zone-ch"><span class="ch-label ch-g">G</span><input type="range" min="-100" max="100" class="slider-g" value={highG} oninput={(e) => setHighlight(1, Number((e.target as HTMLInputElement).value))} /></div>
							<div class="zone-ch"><span class="ch-label ch-b">B</span><input type="range" min="-100" max="100" class="slider-b" value={highB} oninput={(e) => setHighlight(2, Number((e.target as HTMLInputElement).value))} /></div>
						</div>
					</div>
				{/if}
			</div>
		</div>

		<div class="controls-footer">
			<div class="footer-row">
				<button type="button" class="btn-reset" onclick={handleReset}>Reset</button>
				{#if selectedSource === 'program'}
					{#if !saveLookExpanded}
						<button type="button" class="btn-save" onclick={openSaveLook}>Save as Look</button>
					{/if}
				{/if}
			</div>
			{#if selectedSource === 'program' && saveLookExpanded}
				<div class="save-inline">
					<input
						class="save-name-input"
						type="text"
						placeholder="Look name"
						bind:value={lookNameDraft}
						onkeydown={(e) => e.key === 'Enter' && confirmSaveLook()}
					/>
					<button type="button" class="icon-btn icon-btn-save" onclick={confirmSaveLook} aria-label="Save look" title="Save">
						{@html renderIcon(ICON_SAVE, 14, 'fill')}
					</button>
					<button type="button" class="icon-btn icon-btn-cancel" onclick={cancelSaveLook} aria-label="Cancel" title="Cancel">
						{@html renderIcon(ICON_CLOSE, 14)}
					</button>
				</div>
			{/if}
		</div>
	</div>

	<!-- Column 3: Curves -->
	<div class="curves-col">
		<div class="curve-channel-tabs">
			<button type="button" class="curve-tab" class:active={curveChannel === 'master'} onclick={() => curveChannel = 'master'}>M</button>
			<button type="button" class="curve-tab curve-tab-r" class:active={curveChannel === 'r'} onclick={() => curveChannel = 'r'}>R</button>
			<button type="button" class="curve-tab curve-tab-g" class:active={curveChannel === 'g'} onclick={() => curveChannel = 'g'}>G</button>
			<button type="button" class="curve-tab curve-tab-b" class:active={curveChannel === 'b'} onclick={() => curveChannel = 'b'}>B</button>
		</div>
		<CurveEditor
			size={160}
			points={activeCurvePoints}
			onchange={handleCurveChange}
			color={curveColors[curveChannel]}
		/>
	</div>
</div>

<style>
	/* ── 3-Column Grid Layout ── */
	.color-tab {
		display: grid;
		grid-template-columns: 100px 1fr 180px;
		height: 100%;
		gap: 4px;
		padding: 6px;
		overflow: hidden;
		font-family: var(--font-ui, system-ui, sans-serif);
	}

	/* ── Column 1: Source List ── */
	.source-list {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		border-right: 1px solid var(--border-subtle, var(--border-default, #333));
		padding-right: 4px;
	}

	.source-list-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.source-btn {
		display: flex;
		align-items: center;
		gap: 4px;
		width: 100%;
		padding: 4px 8px;
		background: transparent;
		border: none;
		border-left: 2px solid transparent;
		color: var(--text-secondary, #999);
		cursor: pointer;
		font-size: var(--text-xs, 11px);
		text-align: left;
		transition: background var(--transition-fast, 0.1s), border-color var(--transition-fast, 0.1s), color var(--transition-fast, 0.1s);
		border-radius: 0 var(--radius-xs, 2px) var(--radius-xs, 2px) 0;
		flex-shrink: 0;
		font-family: inherit;
	}

	.source-btn:hover {
		background: var(--bg-hover, var(--bg-surface, #333));
		color: var(--text-primary, #eee);
	}

	.source-btn.active {
		border-left-color: var(--accent-yellow, #d4a017);
		background: var(--bg-elevated, #3a3520);
		color: var(--text-primary, #eee);
	}

	.source-btn.corrected .source-label {
		color: var(--accent-yellow, #d4a017);
	}

	.source-btn.active.corrected .source-label {
		color: var(--text-primary, #eee);
	}

	.correction-dot {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--accent-yellow, #d4a017);
		flex-shrink: 0;
	}

	.source-label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.source-divider {
		height: 1px;
		background: var(--border-subtle, var(--border-default, #333));
		margin: 4px 0;
		flex-shrink: 0;
	}

	.pgm-btn {
		font-weight: 600;
		letter-spacing: 0.5px;
		flex-shrink: 0;
	}

	/* ── Column 2: Controls ── */
	.controls-col {
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
		overflow: hidden;
		gap: 4px;
	}

	.controls-fixed {
		flex-shrink: 0;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.wheels-row {
		display: flex;
		flex-direction: row;
		flex-wrap: nowrap;
		gap: 6px;
		align-items: flex-start;
		justify-content: flex-start;
	}

	.sliders-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
		gap: 2px 8px;
		align-items: center;
		min-width: 0;
	}

	.slider-row {
		display: grid;
		grid-template-columns: 30px 1fr 24px;
		align-items: center;
		gap: 3px;
		min-height: 18px;
		min-width: 0;
	}

	.slider-label {
		font-size: var(--text-2xs, 10px);
		text-align: right;
		color: var(--text-secondary, #999);
		font-variant-numeric: tabular-nums;
	}

	.slider-row input[type="range"] {
		width: 100%;
		min-width: 0;
		height: 2px;
		accent-color: var(--accent-yellow, #d4a017);
	}

	.slider-value {
		font-size: var(--text-2xs, 10px);
		text-align: right;
		font-variant-numeric: tabular-nums;
		font-family: var(--font-mono, ui-monospace, monospace);
		color: var(--text-primary, #eee);
	}

	.controls-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
	}

	/* ── COLOR ZONES disclosure ── */
	.zones-disclosure {
		border: 1px solid var(--border-subtle, var(--border-default, #333));
		border-radius: var(--radius-sm, 3px);
		background: var(--bg-surface, var(--bg-base, #1a1a1a));
		overflow: hidden;
	}

	.zones-disclosure-trigger {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		padding: 4px 8px;
		background: var(--bg-elevated, #252525);
		border: none;
		cursor: pointer;
		font-family: inherit;
		font-size: var(--text-2xs, 10px);
		font-weight: 600;
		letter-spacing: 0.06em;
		color: var(--text-secondary, #999);
		text-align: left;
		transition: background var(--transition-fast, 0.1s), color var(--transition-fast, 0.1s);
	}

	.zones-disclosure-trigger:hover {
		background: var(--bg-hover, #2e2e2e);
		color: var(--text-primary, #eee);
	}

	.zones-chevron {
		display: flex;
		align-items: center;
		justify-content: center;
		color: var(--text-tertiary, #777);
		flex-shrink: 0;
	}

	.zones-disclosure-trigger:hover .zones-chevron {
		color: var(--text-secondary, #999);
	}

	.zones-disclosure-title {
		flex: 1;
	}

	.zones-panel {
		padding: 4px 6px 6px;
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		gap: 6px;
		border-top: 1px solid var(--border-subtle, var(--border-default, #333));
		background: var(--bg-base, #141414);
	}

	.zone-col {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}

	.zone-title {
		font-size: var(--text-2xs, 9px);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--text-tertiary, #888);
		margin-bottom: 1px;
	}

	.zone-ch {
		display: flex;
		align-items: center;
		gap: 3px;
		min-width: 0;
	}

	.zone-ch input[type="range"] {
		flex: 1;
		min-width: 0;
		height: 2px;
	}

	.ch-label {
		font-size: var(--text-2xs, 9px);
		font-weight: 600;
		width: 10px;
		flex-shrink: 0;
		font-variant-numeric: tabular-nums;
	}

	.ch-r { color: #e74c3c; }
	.ch-g { color: #2ecc71; }
	.ch-b { color: #3498db; }

	.slider-r { accent-color: #e74c3c; }
	.slider-g { accent-color: #2ecc71; }
	.slider-b { accent-color: #3498db; }

	/* ── Controls Footer ── */
	.controls-footer {
		flex-shrink: 0;
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding-top: 6px;
		border-top: 1px solid var(--border-subtle, var(--border-default, #333));
	}

	.footer-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}

	.btn-reset,
	.btn-save {
		padding: 4px 10px;
		background: var(--bg-surface, #2a2a2a);
		border: 1px solid var(--border-default, #444);
		border-radius: var(--radius-sm, 3px);
		color: var(--text-secondary, #999);
		cursor: pointer;
		font-size: var(--text-2xs, 10px);
		font-family: inherit;
		transition: background var(--transition-fast, 0.15s), color var(--transition-fast, 0.15s), border-color var(--transition-fast, 0.15s);
	}

	.btn-reset:hover,
	.btn-save:hover {
		background: var(--bg-hover, #333);
		color: var(--text-primary, #eee);
	}

	.btn-save {
		color: var(--accent-yellow, #d4a017);
		border-color: var(--accent-yellow, #d4a017);
	}

	.btn-save:hover {
		background: var(--bg-elevated, #3a3520);
	}

	.save-inline {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-shrink: 0;
	}

	.save-name-input {
		flex: 1;
		min-width: 0;
		padding: 4px 8px;
		font-size: var(--text-xs, 11px);
		font-family: var(--font-ui, inherit);
		color: var(--text-primary, #eee);
		background: var(--bg-base, #1a1a1a);
		border: 1px solid var(--border-default, #444);
		border-radius: var(--radius-sm, 3px);
		outline: none;
		transition: border-color var(--transition-fast, 0.1s);
	}

	.save-name-input:focus {
		border-color: var(--accent-blue, #4a9eff);
	}

	.save-name-input::placeholder {
		color: var(--text-tertiary, #666);
	}

	.icon-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 28px;
		height: 28px;
		padding: 0;
		flex-shrink: 0;
		border-radius: var(--radius-sm, 3px);
		border: 1px solid var(--border-default, #444);
		background: var(--bg-surface, #2a2a2a);
		color: var(--text-secondary, #999);
		cursor: pointer;
		transition: background var(--transition-fast, 0.1s), color var(--transition-fast, 0.1s), border-color var(--transition-fast, 0.1s);
	}

	.icon-btn:hover {
		background: var(--bg-hover, #333);
		color: var(--text-primary, #eee);
	}

	.icon-btn-save {
		color: var(--color-success, #5cb85c);
		border-color: var(--color-success, #5cb85c);
	}

	.icon-btn-save:hover {
		background: rgba(92, 184, 92, 0.15);
		color: var(--color-success, #6fcf6f);
	}

	.icon-btn-cancel:hover {
		border-color: var(--text-tertiary, #666);
	}

	/* ── Column 3: Curves ── */
	.curves-col {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 4px;
		min-height: 0;
		overflow: hidden;
		border-left: 1px solid var(--border-subtle, var(--border-default, #333));
		padding-left: 4px;
	}

	.curve-channel-tabs {
		display: flex;
		gap: 2px;
		flex-shrink: 0;
	}

	.curve-tab {
		padding: 2px 8px;
		background: var(--bg-surface, #2a2a2a);
		border: 1px solid var(--border-default, #444);
		border-radius: var(--radius-sm, 3px);
		color: var(--text-secondary, #999);
		cursor: pointer;
		font-size: var(--text-2xs, 10px);
		font-weight: 500;
		font-family: inherit;
		transition: background var(--transition-fast, 0.15s), border-color var(--transition-fast, 0.15s), color var(--transition-fast, 0.15s);
	}

	.curve-tab:hover {
		background: var(--bg-hover, #333);
		color: var(--text-primary, #eee);
	}

	.curve-tab.active {
		background: var(--bg-elevated, #3a3520);
		border-color: var(--accent-yellow, #d4a017);
		color: var(--accent-yellow, #d4a017);
	}

	.curve-tab-r.active {
		background: #3a2020;
		border-color: #e55;
		color: #e55;
	}

	.curve-tab-g.active {
		background: #203a20;
		border-color: #5e5;
		color: #5e5;
	}

	.curve-tab-b.active {
		background: #20203a;
		border-color: #55e;
		color: #55e;
	}
</style>
