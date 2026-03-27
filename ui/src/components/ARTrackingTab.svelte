<script lang="ts">
	import type { ControlRoomState, ARTrackingSourceInfo } from '$lib/api/types';
	import {
		arTrackingAddFreeD, arTrackingGetPose,
		arTrackingSetCalibration, arTrackingSetAruco,
		apiCall,
	} from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// ── State ──
	let selectedSource = $state<string | null>(null);
	let freedAddr = $state('');
	let freedPort = $state(40000);
	let freedCamId = $state(1);
	let freedSource = $state('cam1');
	let sensorWidth = $state(36.0);
	let calFileInput: HTMLInputElement | undefined = $state();

	// Pose display state (polled from server)
	let poseData = $state<{ position: [number, number, number]; orientation: [number, number, number]; focalLength: number; focusDistance: number; confidence: number; timestamp: number; source: string } | null>(null);
	let poseError = $state(false);

	// ArUco markers
	let markers = $state<Record<string, [number, number, number]>>({});
	let newMarkerId = $state(0);

	// ── Derived ──
	const sources = $derived(crState.arTracking?.sources ?? {});
	const sourceKeys = $derived(Object.keys(sources));

	// Auto-select first source
	$effect(() => {
		if (sourceKeys.length > 0 && !selectedSource) {
			selectedSource = sourceKeys[0];
		}
		if (selectedSource && !sourceKeys.includes(selectedSource)) {
			selectedSource = sourceKeys.length > 0 ? sourceKeys[0] : null;
		}
	});

	// Poll pose data for selected source
	$effect(() => {
		poseData = null;
		poseError = false;
		if (!selectedSource) return;
		const src = selectedSource;
		async function fetchPose() {
			try {
				poseData = await arTrackingGetPose(src);
				poseError = false;
			} catch {
				poseError = true;
			}
		}
		fetchPose();
		const timer = setInterval(fetchPose, 500);
		return () => clearInterval(timer);
	});

	// ── Handlers ──
	async function handleConnectFreeD() {
		if (!freedSource.trim() || !freedAddr.trim()) {
			notify('error', 'Source name and address required');
			return;
		}
		try {
			const addr = freedPort ? `${freedAddr.trim()}:${freedPort}` : freedAddr.trim();
			await arTrackingAddFreeD(freedSource.trim(), { addr, cameraID: freedCamId });
			notify('info', `Free-D connected: ${freedSource}`);
			selectedSource = freedSource.trim();
		} catch {
			notify('error', 'Free-D connection failed');
		}
	}

	function sourceHasRunning(trackers: ARTrackingSourceInfo[]): boolean {
		return trackers.some(t => t.running);
	}

	function sourceTrackerTypes(trackers: ARTrackingSourceInfo[]): string {
		return trackers.map(t => t.type).join(', ');
	}

	async function handleImportCalibration() {
		calFileInput?.click();
	}

	async function handleCalibrationFile(e: Event) {
		const target = e.target as HTMLInputElement;
		const file = target.files?.[0];
		if (!file || !selectedSource) return;
		try {
			const text = await file.text();
			const data = JSON.parse(text);
			const focalLengthCurve: [number, number][] = data.focalLengthCurve
				?? data.focal_length_curve
				?? [[0, data.focalLength ?? data.focal_length ?? 50]];
			await arTrackingSetCalibration(selectedSource, {
				focalLengthCurve,
				sensorWidth: data.sensorWidth ?? data.sensor_width ?? sensorWidth,
				principalPoint: data.principalPoint ?? data.principal_point ?? [0.5, 0.5],
				distortion: data.distortion ?? [0, 0, 0, 0, 0],
			});
			notify('info', 'Calibration imported');
		} catch {
			notify('error', 'Failed to import calibration');
		}
		target.value = '';
	}

	function handleAddMarker() {
		const id = String(newMarkerId);
		markers = { ...markers, [id]: [0, 0, 0] };
		newMarkerId = newMarkerId + 1;
	}

	function handleRemoveMarker(id: string) {
		const next = { ...markers };
		delete next[id];
		markers = next;
	}

	function handleMarkerPosChange(id: string, axis: number, value: string) {
		const pos: [number, number, number] = [...(markers[id] ?? [0, 0, 0])];
		pos[axis] = parseFloat(value) || 0;
		markers = { ...markers, [id]: pos };
	}

	function handleApplyAruco() {
		if (Object.keys(markers).length === 0) {
			notify('error', 'Add at least one marker position');
			return;
		}
		apiCall(
			arTrackingSetAruco(markers),
			'Set ArUco marker positions',
		);
	}

	const axisLabels = ['X', 'Y', 'Z'];
</script>

<div class="tracking-tab">
	<!-- Left column -->
	<div class="tracking-rail">
		<!-- Sources -->
		<div class="rail-section">
			<div class="rail-header">SOURCES</div>
			<div class="source-list">
				{#each sourceKeys as key (key)}
					{@const trackers = sources[key]}
					<div
						class="source-row"
						class:selected={selectedSource === key}
						role="button"
						tabindex="0"
						onclick={() => selectedSource = key}
						onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") selectedSource = key; }}
					>
						<span class="source-dot" class:running={sourceHasRunning(trackers)}></span>
						<span class="source-name">{key}</span>
						<span class="source-types">{sourceTrackerTypes(trackers)}</span>
					</div>
				{/each}
				{#if sourceKeys.length === 0}
					<div class="empty-hint">No tracking sources</div>
				{/if}
			</div>
		</div>

		<!-- Free-D Config -->
		<div class="rail-section">
			<div class="rail-header">FREE-D</div>
			<div class="freed-form">
				<div class="form-row">
					<label class="form-label" for="ar-freed-source">Source</label>
					<input id="ar-freed-source" class="form-input" type="text" bind:value={freedSource} placeholder="cam1" />
				</div>
				<div class="form-row">
					<label class="form-label" for="ar-freed-addr">Address</label>
					<input id="ar-freed-addr" class="form-input" type="text" bind:value={freedAddr} placeholder="192.168.1.100" />
				</div>
				<div class="form-row">
					<label class="form-label" for="ar-freed-port">Port</label>
					<input id="ar-freed-port" class="form-input num" type="number" bind:value={freedPort} />
				</div>
				<div class="form-row">
					<label class="form-label" for="ar-freed-camid">Cam ID</label>
					<input id="ar-freed-camid" class="form-input num" type="number" bind:value={freedCamId} min="0" max="255" />
				</div>
				<button class="connect-btn" onclick={handleConnectFreeD}>Connect</button>
			</div>
		</div>

		<!-- Calibration -->
		<div class="rail-section">
			<div class="rail-header">CALIBRATION</div>
			<input bind:this={calFileInput} type="file" accept=".json" style="display:none" onchange={handleCalibrationFile} />
			<div class="form-row">
				<label class="form-label" for="ar-sensor-width">Sensor (mm)</label>
				<input id="ar-sensor-width" class="form-input num" type="number" step="0.1" bind:value={sensorWidth} />
			</div>
			<button class="import-btn" onclick={handleImportCalibration} disabled={!selectedSource}>Import Focal</button>
		</div>
	</div>

	<!-- Right detail -->
	<div class="tracking-detail">
		<!-- Pose display -->
		{#if selectedSource}
			<div class="detail-section">
				<div class="detail-header">POSE: {selectedSource}</div>
				{#if poseData}
					<div class="pose-grid">
						<span class="pose-label">Pan</span>
						<span class="pose-val">{poseData.orientation[0].toFixed(1)}&deg;</span>
						<span class="pose-label">Tilt</span>
						<span class="pose-val">{poseData.orientation[1].toFixed(1)}&deg;</span>
						<span class="pose-label">Roll</span>
						<span class="pose-val">{poseData.orientation[2].toFixed(1)}&deg;</span>
						<span class="pose-label">X</span>
						<span class="pose-val">{poseData.position[0].toFixed(2)}m</span>
						<span class="pose-label">Y</span>
						<span class="pose-val">{poseData.position[1].toFixed(2)}m</span>
						<span class="pose-label">Z</span>
						<span class="pose-val">{poseData.position[2].toFixed(2)}m</span>
					</div>
				{:else if poseError}
					<div class="empty-hint">No tracking data</div>
				{:else}
					<div class="empty-hint">Loading...</div>
				{/if}
			</div>
		{:else}
			<div class="empty-detail">Select a tracking source</div>
		{/if}

		<!-- ArUco markers -->
		<div class="detail-section aruco-section">
			<div class="detail-header">
				<span>ARUCO MARKERS</span>
				<button class="add-marker-btn" onclick={handleAddMarker}>+ Add</button>
			</div>
			{#if Object.keys(markers).length > 0}
				<div class="marker-table">
					<div class="marker-header-row">
						<span class="marker-col-id">ID</span>
						<span class="marker-col-pos">X</span>
						<span class="marker-col-pos">Y</span>
						<span class="marker-col-pos">Z</span>
						<span class="marker-col-del"></span>
					</div>
					{#each Object.entries(markers) as [id, pos] (id)}
						<div class="marker-row">
							<input class="marker-id-input" type="number" value={parseInt(id)} disabled />
							{#each [0, 1, 2] as axis}
								<input
									class="marker-pos-input"
									type="number"
									step="0.01"
									value={pos[axis]}
									oninput={(e) => handleMarkerPosChange(id, axis, (e.target as HTMLInputElement).value)}
								/>
							{/each}
							<button class="del-btn-sm" onclick={() => handleRemoveMarker(id)} title="Remove marker">&times;</button>
						</div>
					{/each}
				</div>
			{:else}
				<div class="empty-hint">No markers defined</div>
			{/if}
			<div class="marker-footer">
				<button class="apply-btn" onclick={handleApplyAruco}>Apply Markers</button>
			</div>
		</div>
	</div>
</div>

<style>
	.tracking-tab {
		display: flex;
		height: 100%;
		overflow: hidden;
	}

	/* ── Rail ── */
	.tracking-rail {
		width: 170px;
		min-width: 170px;
		border-right: 1px solid var(--border-subtle, rgba(255,255,255,0.08));
		display: flex;
		flex-direction: column;
		overflow-y: auto;
	}

	.rail-section {
		padding: 6px 8px;
		border-bottom: 1px solid rgba(255,255,255,0.04);
	}

	.rail-header {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 700;
		color: var(--text-tertiary, #666);
		letter-spacing: 0.08em;
		margin-bottom: 4px;
	}

	.source-list {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.source-row {
		display: flex;
		align-items: center;
		gap: 5px;
		padding: 3px 4px;
		border-radius: 2px;
		cursor: pointer;
		transition: background 0.1s;
	}

	.source-row:hover {
		background: rgba(255,255,255,0.04);
	}

	.source-row.selected {
		background: rgba(99, 102, 241, 0.12);
		border-left: 2px solid var(--accent-indigo, #818cf8);
	}

	.source-dot {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--text-tertiary, #666);
		flex-shrink: 0;
	}

	.source-dot.running {
		background: rgb(34, 197, 94);
		box-shadow: 0 0 4px rgba(34, 197, 94, 0.4);
	}

	.source-name {
		font-size: 10px;
		color: var(--text-primary, #eee);
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.source-types {
		font-family: var(--font-mono, monospace);
		font-size: 8px;
		color: var(--text-tertiary, #666);
	}

	.empty-hint {
		font-size: 9px;
		color: var(--text-tertiary, #666);
		font-style: italic;
		padding: 4px 0;
	}

	/* ── Form elements ── */
	.freed-form {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.form-row {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.form-label {
		font-size: 10px;
		color: var(--text-secondary, #999);
		min-width: 50px;
		flex-shrink: 0;
	}

	.form-input {
		flex: 1;
		font-family: var(--font-mono, monospace);
		font-size: 10px;
		padding: 2px 4px;
		border: 1px solid rgba(255,255,255,0.1);
		border-radius: 2px;
		background: rgba(255,255,255,0.04);
		color: var(--text-primary, #eee);
		outline: none;
	}

	.form-input:focus {
		border-color: rgba(99, 102, 241, 0.5);
	}

	.form-input.num {
		width: 60px;
		flex: none;
	}

	.connect-btn, .import-btn, .apply-btn {
		font-family: var(--font-ui, sans-serif);
		font-size: 10px;
		font-weight: 600;
		padding: 3px 8px;
		border: 1px solid rgba(99, 102, 241, 0.3);
		border-radius: 2px;
		background: rgba(99, 102, 241, 0.1);
		color: var(--accent-indigo, #818cf8);
		cursor: pointer;
		margin-top: 3px;
		transition: background 0.15s;
	}

	.connect-btn:hover, .import-btn:hover, .apply-btn:hover {
		background: rgba(99, 102, 241, 0.2);
	}

	.connect-btn:disabled, .import-btn:disabled, .apply-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	/* ── Detail ── */
	.tracking-detail {
		flex: 1;
		display: flex;
		flex-direction: column;
		overflow-y: auto;
		padding: 6px 10px;
		gap: 8px;
	}

	.detail-section {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.detail-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 700;
		color: var(--text-tertiary, #666);
		letter-spacing: 0.08em;
		padding-bottom: 2px;
		border-bottom: 1px solid rgba(255,255,255,0.04);
	}

	.empty-detail {
		flex: 1;
		display: flex;
		align-items: center;
		justify-content: center;
		font-size: 10px;
		color: var(--text-tertiary, #666);
		font-style: italic;
	}

	/* ── Pose display ── */
	.pose-grid {
		display: grid;
		grid-template-columns: auto 1fr auto 1fr;
		gap: 2px 8px;
		align-items: baseline;
	}

	.pose-label {
		font-size: 10px;
		color: var(--text-secondary, #999);
	}

	.pose-val {
		font-family: var(--font-mono, monospace);
		font-size: 11px;
		color: var(--text-primary, #eee);
		font-weight: 500;
	}

	/* ── ArUco ── */
	.aruco-section {
		border-top: 1px solid rgba(255,255,255,0.06);
		padding-top: 6px;
	}

	.add-marker-btn {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 500;
		padding: 1px 6px;
		border: 1px solid rgba(99, 102, 241, 0.3);
		border-radius: 2px;
		background: rgba(99, 102, 241, 0.1);
		color: var(--accent-indigo, #818cf8);
		cursor: pointer;
	}

	.add-marker-btn:hover {
		background: rgba(99, 102, 241, 0.2);
	}

	.marker-table {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.marker-header-row {
		display: flex;
		gap: 3px;
		padding: 1px 0;
	}

	.marker-header-row span {
		font-family: var(--font-mono, monospace);
		font-size: 8px;
		font-weight: 600;
		color: var(--text-tertiary, #666);
		text-align: center;
	}

	.marker-col-id {
		width: 36px;
	}

	.marker-col-pos {
		width: 48px;
	}

	.marker-col-del {
		width: 16px;
	}

	.marker-row {
		display: flex;
		align-items: center;
		gap: 3px;
	}

	.marker-id-input {
		width: 36px;
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		padding: 1px 2px;
		border: 1px solid rgba(255,255,255,0.08);
		border-radius: 2px;
		background: rgba(255,255,255,0.02);
		color: var(--text-secondary, #999);
		outline: none;
	}

	.marker-pos-input {
		width: 48px;
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		padding: 1px 2px;
		border: 1px solid rgba(255,255,255,0.1);
		border-radius: 2px;
		background: rgba(255,255,255,0.04);
		color: var(--text-primary, #eee);
		outline: none;
	}

	.marker-pos-input:focus {
		border-color: rgba(99, 102, 241, 0.5);
	}

	.del-btn-sm {
		font-size: 12px;
		line-height: 1;
		border: none;
		background: none;
		color: var(--text-tertiary, #666);
		cursor: pointer;
		padding: 0 2px;
	}

	.del-btn-sm:hover {
		color: var(--color-error, #ef4444);
	}

	.marker-footer {
		display: flex;
		align-items: center;
		gap: 8px;
		padding-top: 4px;
	}
</style>
