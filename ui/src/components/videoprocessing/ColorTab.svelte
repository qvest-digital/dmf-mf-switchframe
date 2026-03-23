<script lang="ts">
	import type { ControlRoomState, CorrectorState, CurvePoint } from '$lib/api/types';
	import { setCorrector, resetCorrector, saveCustomLook } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import { onDestroy } from 'svelte';
	import ColorWheel from './ColorWheel.svelte';
	import CurveEditor from './CurveEditor.svelte';

	let { state: crState }: { state: ControlRoomState } = $props();

	// Local corrector state, synced from server
	let corrector = $state<CorrectorState>({
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
	});

	// Local UI values for sliders that need scaling (server uses 0-2, -1..+1, 0-1)
	let satUI = $state(100);
	let contrastUI = $state(0);
	let sharpnessUI = $state(0);

	// Guard against server state overwriting in-progress user edits.
	// After any user interaction, ignore server syncs for 1s to let the
	// debounced API call (200ms) complete and the server echo back.
	let lastUserEdit = 0;
	const SYNC_GUARD_MS = 1000;

	function markDirty() {
		lastUserEdit = Date.now();
	}

	// Sync from server state (skipped while user is actively editing)
	$effect(() => {
		const sc = crState.colorGrade?.corrector;
		if (sc) {
			if (Date.now() - lastUserEdit < SYNC_GUARD_MS) return;
			corrector = {
				...sc,
				curveMaster: sc.curveMaster ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }],
				curveR: sc.curveR ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }],
				curveG: sc.curveG ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }],
				curveB: sc.curveB ?? [{ x: 0, y: 0 }, { x: 1, y: 1 }],
			};
			satUI = Math.round(sc.saturation * 100);
			contrastUI = Math.round(sc.contrast * 100);
			sharpnessUI = Math.round(sc.sharpness * 100);
		}
	});

	// Curve channel tabs
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

	// UI display values for sliders (mapped -100..100 range for shadows/midtones/highlights)
	let shadowR = $derived(Math.round(corrector.shadows[0] * 100));
	let shadowG = $derived(Math.round(corrector.shadows[1] * 100));
	let shadowB = $derived(Math.round(corrector.shadows[2] * 100));
	let midR = $derived(Math.round(corrector.midtones[0] * 100));
	let midG = $derived(Math.round(corrector.midtones[1] * 100));
	let midB = $derived(Math.round(corrector.midtones[2] * 100));
	let highR = $derived(Math.round(corrector.highlights[0] * 100));
	let highG = $derived(Math.round(corrector.highlights[1] * 100));
	let highB = $derived(Math.round(corrector.highlights[2] * 100));

	// Debounced send to server
	let sendTimeout: ReturnType<typeof setTimeout>;
	function sendCorrector() {
		clearTimeout(sendTimeout);
		sendTimeout = setTimeout(async () => {
			try {
				await setCorrector(corrector);
			} catch (err) {
				notify('error', `Corrector update failed: ${err instanceof Error ? err.message : 'unknown'}`);
			}
		}, 200);
	}

	onDestroy(() => clearTimeout(sendTimeout));

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

	// Slider handlers for temperature, tint, etc.
	function handleSliderInput() {
		markDirty();
		sendCorrector();
	}

	// Handlers for scaled sliders (UI integer range -> server float range)
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

	// Zone (shadows/midtones/highlights) handlers
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

	async function handleReset() {
		try {
			await resetCorrector();
		} catch (err) {
			notify('error', `Reset failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function handleSave() {
		const name = prompt('Look name:');
		if (!name) return;
		try {
			await saveCustomLook(name);
			notify('info', `Saved "${name}"`);
		} catch (err) {
			notify('error', `Save failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	// Display helpers
	function fmtSlider(v: number, decimals: number = 0): string {
		return v.toFixed(decimals);
	}
</script>

<div class="color-tab">
	<!-- Top section: Wheels + Parameter sliders -->
	<div class="top-section">
		<div class="wheels-col">
			<ColorWheel label="Lift" value={corrector.lift} onchange={handleLiftChange} min={-1} max={1} />
			<ColorWheel label="Gamma" value={corrector.gamma} onchange={handleGammaChange} min={0.1} max={4} />
			<ColorWheel label="Gain" value={corrector.gain} onchange={handleGainChange} min={0} max={4} />
		</div>
		<div class="sliders-col">
			<label class="slider-row">
				<span class="slider-label">Temperature</span>
				<input type="range" min="-100" max="100" step="1"
					bind:value={corrector.temperature}
					oninput={handleSliderInput}
				/>
				<span class="slider-value">{fmtSlider(corrector.temperature)}</span>
			</label>
			<label class="slider-row">
				<span class="slider-label">Tint</span>
				<input type="range" min="-100" max="100" step="1"
					bind:value={corrector.tint}
					oninput={handleSliderInput}
				/>
				<span class="slider-value">{fmtSlider(corrector.tint)}</span>
			</label>
			<label class="slider-row">
				<span class="slider-label">Saturation</span>
				<input type="range" min="0" max="200" step="1"
					bind:value={satUI}
					oninput={handleSatInput}
				/>
				<span class="slider-value">{fmtSlider(satUI)}</span>
			</label>
			<label class="slider-row">
				<span class="slider-label">Contrast</span>
				<input type="range" min="-100" max="100" step="1"
					bind:value={contrastUI}
					oninput={handleContrastInput}
				/>
				<span class="slider-value">{fmtSlider(contrastUI)}</span>
			</label>
			<label class="slider-row">
				<span class="slider-label">Sharpness</span>
				<input type="range" min="0" max="100" step="1"
					bind:value={sharpnessUI}
					oninput={handleSharpnessInput}
				/>
				<span class="slider-value">{fmtSlider(sharpnessUI)}</span>
			</label>
		</div>
	</div>

	<!-- Zone section: Shadows / Midtones / Highlights RGB sliders -->
	<div class="zone-section">
		<div class="zone-group">
			<span class="zone-label">Shadows</span>
			<div class="zone-sliders">
				<div class="zone-ch">
					<span class="ch-label ch-r">R</span>
					<input type="range" min="-100" max="100" class="slider-r"
						value={shadowR}
						oninput={(e) => setShadow(0, Number((e.target as HTMLInputElement).value))}
					/>
				</div>
				<div class="zone-ch">
					<span class="ch-label ch-g">G</span>
					<input type="range" min="-100" max="100" class="slider-g"
						value={shadowG}
						oninput={(e) => setShadow(1, Number((e.target as HTMLInputElement).value))}
					/>
				</div>
				<div class="zone-ch">
					<span class="ch-label ch-b">B</span>
					<input type="range" min="-100" max="100" class="slider-b"
						value={shadowB}
						oninput={(e) => setShadow(2, Number((e.target as HTMLInputElement).value))}
					/>
				</div>
			</div>
		</div>
		<div class="zone-group">
			<span class="zone-label">Midtones</span>
			<div class="zone-sliders">
				<div class="zone-ch">
					<span class="ch-label ch-r">R</span>
					<input type="range" min="-100" max="100" class="slider-r"
						value={midR}
						oninput={(e) => setMidtone(0, Number((e.target as HTMLInputElement).value))}
					/>
				</div>
				<div class="zone-ch">
					<span class="ch-label ch-g">G</span>
					<input type="range" min="-100" max="100" class="slider-g"
						value={midG}
						oninput={(e) => setMidtone(1, Number((e.target as HTMLInputElement).value))}
					/>
				</div>
				<div class="zone-ch">
					<span class="ch-label ch-b">B</span>
					<input type="range" min="-100" max="100" class="slider-b"
						value={midB}
						oninput={(e) => setMidtone(2, Number((e.target as HTMLInputElement).value))}
					/>
				</div>
			</div>
		</div>
		<div class="zone-group">
			<span class="zone-label">Highlights</span>
			<div class="zone-sliders">
				<div class="zone-ch">
					<span class="ch-label ch-r">R</span>
					<input type="range" min="-100" max="100" class="slider-r"
						value={highR}
						oninput={(e) => setHighlight(0, Number((e.target as HTMLInputElement).value))}
					/>
				</div>
				<div class="zone-ch">
					<span class="ch-label ch-g">G</span>
					<input type="range" min="-100" max="100" class="slider-g"
						value={highG}
						oninput={(e) => setHighlight(1, Number((e.target as HTMLInputElement).value))}
					/>
				</div>
				<div class="zone-ch">
					<span class="ch-label ch-b">B</span>
					<input type="range" min="-100" max="100" class="slider-b"
						value={highB}
						oninput={(e) => setHighlight(2, Number((e.target as HTMLInputElement).value))}
					/>
				</div>
			</div>
		</div>
	</div>

	<!-- Curves section -->
	<div class="curves-section">
		<div class="curves-header">
			<span class="section-title">Curves</span>
			<div class="curve-tabs">
				<button class="curve-tab" class:active={curveChannel === 'master'} onclick={() => curveChannel = 'master'}>Master</button>
				<button class="curve-tab curve-tab-r" class:active={curveChannel === 'r'} onclick={() => curveChannel = 'r'}>R</button>
				<button class="curve-tab curve-tab-g" class:active={curveChannel === 'g'} onclick={() => curveChannel = 'g'}>G</button>
				<button class="curve-tab curve-tab-b" class:active={curveChannel === 'b'} onclick={() => curveChannel = 'b'}>B</button>
			</div>
		</div>
		<div class="curve-canvas">
			<CurveEditor
				points={activeCurvePoints}
				onchange={handleCurveChange}
				color={curveColors[curveChannel]}
			/>
		</div>
	</div>

	<!-- Footer: Reset + Save -->
	<div class="footer">
		<button class="btn-action" onclick={handleReset}>Reset All</button>
		<button class="btn-action btn-save" onclick={handleSave}>Save as Look</button>
	</div>
</div>

<style>
	.color-tab {
		display: flex;
		flex-direction: column;
		gap: 8px;
		padding: 8px;
		height: 100%;
		overflow-y: auto;
	}

	/* Top section: wheels + sliders */
	.top-section {
		display: flex;
		gap: 12px;
		flex-shrink: 0;
	}

	.wheels-col {
		display: flex;
		gap: 8px;
		align-items: flex-start;
		flex-shrink: 0;
	}

	.sliders-col {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 4px;
		min-width: 0;
		justify-content: center;
	}

	.slider-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.slider-label {
		font-size: 11px;
		color: var(--text-secondary, #999);
		width: 72px;
		flex-shrink: 0;
	}

	.slider-row input[type="range"] {
		flex: 1;
		min-width: 0;
		accent-color: var(--accent-yellow, #d4a017);
		height: 14px;
	}

	.slider-value {
		font-size: 10px;
		font-variant-numeric: tabular-nums;
		color: var(--text-primary, #eee);
		width: 32px;
		text-align: right;
		flex-shrink: 0;
	}

	/* Zone section: Shadows/Midtones/Highlights */
	.zone-section {
		display: flex;
		gap: 12px;
		flex-shrink: 0;
	}

	.zone-group {
		flex: 1;
		min-width: 0;
	}

	.zone-label {
		font-size: 11px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		color: var(--text-secondary, #999);
		display: block;
		margin-bottom: 4px;
	}

	.zone-sliders {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.zone-ch {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.ch-label {
		font-size: 9px;
		font-weight: 600;
		width: 10px;
		flex-shrink: 0;
	}

	.ch-r { color: #c66; }
	.ch-g { color: #6c6; }
	.ch-b { color: #66c; }

	.zone-ch input[type="range"] {
		flex: 1;
		min-width: 0;
		height: 12px;
	}

	.slider-r { accent-color: #c66; }
	.slider-g { accent-color: #6c6; }
	.slider-b { accent-color: #66c; }

	/* Curves section */
	.curves-section {
		flex-shrink: 0;
	}

	.curves-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 4px;
	}

	.section-title {
		font-size: 11px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		color: var(--text-secondary, #999);
	}

	.curve-tabs {
		display: flex;
		gap: 2px;
	}

	.curve-tab {
		padding: 2px 8px;
		background: var(--surface-secondary, #2a2a2a);
		border: 1px solid var(--border-color, #444);
		border-radius: 3px;
		color: var(--text-secondary, #999);
		cursor: pointer;
		font-size: 10px;
		font-weight: 500;
		transition: background 0.15s, border-color 0.15s, color 0.15s;
	}

	.curve-tab:hover {
		background: var(--surface-hover, #333);
		color: var(--text-primary, #eee);
	}

	.curve-tab.active {
		background: var(--accent-yellow-bg, #3a3520);
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

	.curve-canvas {
		display: flex;
		justify-content: center;
	}

	/* Footer */
	.footer {
		display: flex;
		justify-content: space-between;
		flex-shrink: 0;
		padding-top: 4px;
		border-top: 1px solid var(--border-color, #333);
	}

	.btn-action {
		padding: 4px 12px;
		background: var(--surface-secondary, #2a2a2a);
		border: 1px solid var(--border-color, #444);
		border-radius: 3px;
		color: var(--text-secondary, #999);
		cursor: pointer;
		font-size: 11px;
		transition: background 0.15s, color 0.15s;
	}

	.btn-action:hover {
		background: var(--surface-hover, #333);
		color: var(--text-primary, #eee);
	}

	.btn-save {
		color: var(--accent-yellow, #d4a017);
		border-color: var(--accent-yellow, #d4a017);
	}

	.btn-save:hover {
		background: var(--accent-yellow-bg, #3a3520);
	}
</style>
