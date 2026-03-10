<script lang="ts">
	import type { ControlRoomState, SCTE35CueRequest, SCTE35Active, SCTE35Event, SCTE35DescriptorRequest, SCTE35DescriptorInfo } from '$lib/api/types';
	import { scte35Cue, scte35Return, scte35Hold, scte35Extend, scte35Cancel, apiCall } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		state: ControlRoomState;
		onStateUpdate?: (state: ControlRoomState) => void;
	}

	let { state: crState, onStateUpdate }: Props = $props();

	// --- Quick Actions state ---
	let selectedDuration = $state(30000); // ms
	let customDurationSec = $state('');
	let autoReturn = $state(true);
	let preRollMs = $state(0);
	let extendDurationSec = $state('30');

	// --- Advanced Cue Builder state ---
	let advancedTab = $state<'splice_insert' | 'time_signal'>('splice_insert');
	let segmentationType = $state(48); // Provider Ad Start
	let upidType = $state(9); // ADI
	let upidText = $state('');
	let advancedTiming = $state<'immediate' | 'scheduled'>('immediate');
	let advancedPreRollMs = $state('2000');
	let advancedDurationSec = $state('30');

	// --- Countdown timer ---
	let now = $state(Date.now());
	let countdownInterval: ReturnType<typeof setInterval> | undefined;

	$effect(() => {
		countdownInterval = setInterval(() => { now = Date.now(); }, 250);
		return () => clearInterval(countdownInterval);
	});

	// --- Derived state ---
	const scte35 = $derived(crState.scte35);
	const activeEvents = $derived(scte35?.activeEvents ?? {});
	const activeList = $derived(Object.values(activeEvents));
	const hasActiveOut = $derived(activeList.some(e => e.isOut));
	const hasAutoReturn = $derived(activeList.some(e => e.autoReturn && !e.held));
	const hasHeld = $derived(activeList.some(e => e.held));
	const eventLog = $derived(scte35?.eventLog ?? []);

	// Status indicator
	const statusLabel = $derived.by(() => {
		if (hasHeld) return 'HELD';
		if (hasActiveOut) return 'IN BREAK';
		return 'ON AIR';
	});

	const statusClass = $derived.by(() => {
		if (hasHeld) return 'status-held';
		if (hasActiveOut) return 'status-break';
		return 'status-on-air';
	});

	// Segmentation types for time_signal
	const segmentationTypes = [
		{ value: 48, label: '0x30 - Provider Ad Start' },
		{ value: 49, label: '0x31 - Provider Ad End' },
		{ value: 50, label: '0x32 - Distributor Ad Start' },
		{ value: 51, label: '0x33 - Distributor Ad End' },
		{ value: 52, label: '0x34 - Provider PO Start' },
		{ value: 53, label: '0x35 - Provider PO End' },
		{ value: 54, label: '0x36 - Distributor PO Start' },
		{ value: 55, label: '0x37 - Distributor PO End' },
		{ value: 16, label: '0x10 - Program Start' },
		{ value: 17, label: '0x11 - Program End' },
		{ value: 34, label: '0x22 - Break Start' },
		{ value: 35, label: '0x23 - Break End' },
		{ value: 64, label: '0x40 - Unscheduled Event Start' },
		{ value: 65, label: '0x41 - Unscheduled Event End' },
	];

	const upidTypes = [
		{ value: 1, label: 'User Defined' },
		{ value: 3, label: 'Ad-ID' },
		{ value: 8, label: 'TI' },
		{ value: 9, label: 'ADI' },
		{ value: 10, label: 'EIDR' },
		{ value: 15, label: 'ADS Info' },
	];

	// --- Guide visibility ---
	const GUIDE_STORAGE_KEY = 'sf-scte35-guide-dismissed';
	let guideDismissed = $state(
		typeof localStorage !== 'undefined' && localStorage.getItem(GUIDE_STORAGE_KEY) === 'true'
	);

	function toggleGuide() {
		guideDismissed = !guideDismissed;
		if (typeof localStorage !== 'undefined') {
			localStorage.setItem(GUIDE_STORAGE_KEY, String(guideDismissed));
		}
	}

	// --- Demo sequence ---
	function handleRunDemo() {
		const req: SCTE35CueRequest = {
			commandType: 'splice_insert',
			isOut: true,
			durationMs: 60000,
			autoReturn: true,
		};
		apiCall(scte35Cue(req), 'SCTE-35 demo ad break');
		notify('info', 'Demo: 60s ad break inserted. Try HOLD, EXTEND, or RETURN while it counts down.');
	}

	// Duration presets in ms
	const durationPresets = [
		{ label: '30s', value: 30000 },
		{ label: '60s', value: 60000 },
		{ label: '90s', value: 90000 },
		{ label: '120s', value: 120000 },
	];

	const preRollPresets = [
		{ label: 'None', value: 0 },
		{ label: '2s', value: 2000 },
		{ label: '4s', value: 4000 },
		{ label: '8s', value: 8000 },
	];

	function getEffectiveDuration(): number {
		const custom = parseInt(customDurationSec);
		if (customDurationSec && !isNaN(custom) && custom > 0) {
			return custom * 1000;
		}
		return selectedDuration;
	}

	function handleAdBreak() {
		const req: SCTE35CueRequest = {
			commandType: 'splice_insert',
			isOut: true,
			durationMs: getEffectiveDuration(),
			autoReturn,
			preRollMs: preRollMs > 0 ? preRollMs : undefined,
		};
		apiCall(scte35Cue(req), 'SCTE-35 cue');
	}

	function handleReturn() {
		apiCall(scte35Return(), 'SCTE-35 return');
	}

	function handleHold(eventId: number) {
		apiCall(scte35Hold(eventId), 'SCTE-35 hold');
	}

	function handleExtend(eventId: number) {
		const secs = parseInt(extendDurationSec);
		if (isNaN(secs) || secs <= 0) {
			notify('error', 'Invalid extend duration');
			return;
		}
		apiCall(scte35Extend(eventId, secs * 1000), 'SCTE-35 extend');
	}

	function handleCancelEvent(eventId: number) {
		apiCall(scte35Cancel(eventId), 'SCTE-35 cancel');
	}

	function handleSendAdvancedCue() {
		const durationMs = parseInt(advancedDurationSec) * 1000;
		if (isNaN(durationMs) || durationMs <= 0) {
			notify('error', 'Invalid duration');
			return;
		}

		const req: SCTE35CueRequest = {
			commandType: advancedTab,
			isOut: true,
			durationMs,
			autoReturn,
			preRollMs: advancedTiming === 'scheduled' ? parseInt(advancedPreRollMs) || 2000 : undefined,
		};

		if (advancedTab === 'time_signal') {
			const descriptors: SCTE35DescriptorRequest[] = [{
				segmentationType,
				upidType,
				upid: upidText || 'UNKNOWN',
				durationMs,
			}];
			req.descriptors = descriptors;
		}

		apiCall(scte35Cue(req), 'SCTE-35 cue');
	}

	function formatCountdown(evt: SCTE35Active): string {
		if (evt.held) return 'HELD';
		if (!evt.durationMs || !evt.startedAt) return '—';
		const elapsed = now - evt.startedAt;
		const remaining = evt.durationMs - elapsed;
		if (remaining <= 0) return '0:00';
		const totalSec = Math.ceil(remaining / 1000);
		const min = Math.floor(totalSec / 60);
		const sec = totalSec % 60;
		return `${min}:${sec.toString().padStart(2, '0')}`;
	}

	function formatTimestamp(ts: number): string {
		const d = new Date(ts);
		return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
	}

	function eventTypeClass(evt: SCTE35Event): string {
		if (evt.status === 'cancelled') return 'log-cancelled';
		if (!evt.isOut) return 'log-return';
		return 'log-cue-out';
	}

	function eventTypeLabel(evt: SCTE35Event): string {
		if (evt.status === 'cancelled') return 'CANCEL';
		if (!evt.isOut) return 'RETURN';
		if (evt.commandType === 'time_signal') return 'TIME SIG';
		return 'CUE OUT';
	}

	// --- Event Detail Flyout ---
	let selectedEvent = $state<SCTE35Event | null>(null);

	function openEventDetail(evt: SCTE35Event) {
		selectedEvent = evt;
	}

	function closeEventDetail() {
		selectedEvent = null;
	}

	// Lookup maps for human-readable labels
	const segTypeNames: Record<number, string> = {
		16: 'Program Start', 17: 'Program End',
		34: 'Break Start', 35: 'Break End',
		48: 'Provider Ad Start', 49: 'Provider Ad End',
		50: 'Distributor Ad Start', 51: 'Distributor Ad End',
		52: 'Provider PO Start', 53: 'Provider PO End',
		54: 'Distributor PO Start', 55: 'Distributor PO End',
		64: 'Unscheduled Event Start', 65: 'Unscheduled Event End',
	};

	const upidTypeNames: Record<number, string> = {
		0: 'Not Used', 1: 'User Defined', 2: 'ISCI',
		3: 'Ad-ID', 4: 'UMID', 5: 'Deprecated',
		6: 'ISAN', 7: 'TID', 8: 'TI', 9: 'ADI',
		10: 'EIDR', 11: 'ATSC Content ID', 12: 'MPU',
		13: 'MID', 14: 'ADS Info', 15: 'URI',
	};

	function formatSegType(val: number): string {
		return segTypeNames[val] ?? `Unknown (${val})`;
	}

	function formatUpidType(val: number): string {
		return upidTypeNames[val] ?? `Unknown (${val})`;
	}

	function formatPts(pts: number | undefined): string {
		if (pts === undefined || pts === 0) return '—';
		const secs = pts / 90000;
		return `${pts} (${secs.toFixed(3)}s)`;
	}

	function formatFullTimestamp(ts: number): string {
		const d = new Date(ts);
		return d.toLocaleString(undefined, {
			year: 'numeric', month: 'short', day: 'numeric',
			hour: '2-digit', minute: '2-digit', second: '2-digit',
			fractionalSecondDigits: 3,
		});
	}
</script>

<div class="scte35-panel">
	<!-- Getting Started Guide -->
	{#if !guideDismissed}
		<div class="guide-banner">
			<div class="guide-header">
				<span class="guide-title">Getting Started with SCTE-35</span>
				<button class="guide-dismiss" onclick={toggleGuide} title="Dismiss guide">x</button>
			</div>
			<p class="guide-text">
				SCTE-35 signals ad breaks in MPEG-TS streams. Downstream systems use these cues to insert ads or switch content.
			</p>
			<ol class="guide-steps">
				<li>Click <strong>AD BREAK</strong> to insert a 30s ad break &mdash; watch the countdown</li>
				<li>Click <strong>HOLD</strong> to freeze a break in progress (prevents auto-return)</li>
				<li>Click <strong>EXTEND</strong> to add more time to the break</li>
				<li>Click <strong>RETURN</strong> to end the break early and return to program</li>
				<li>Use the <strong>Cue Builder</strong> for advanced signals (time_signal, segmentation descriptors)</li>
			</ol>
			<button class="demo-btn" onclick={handleRunDemo}>
				Run Demo (60s Ad Break)
			</button>
		</div>
	{/if}

	<!-- Zone 1: Quick Actions -->
	<div class="zone zone-quick">
		<div class="zone-header">
			<span class="zone-title">QUICK ACTIONS</span>
			<span class="status-badge {statusClass}">{statusLabel}</span>
			{#if guideDismissed}
				<button class="guide-show-btn" onclick={toggleGuide} title="Show getting started guide">?</button>
			{/if}
		</div>

		<div class="duration-row">
			{#each durationPresets as preset}
				<button
					class="dur-btn"
					class:active={selectedDuration === preset.value && !customDurationSec}
					onclick={() => { selectedDuration = preset.value; customDurationSec = ''; }}
				>
					{preset.label}
				</button>
			{/each}
			<input
				type="text"
				class="dur-custom"
				placeholder="Custom"
				bind:value={customDurationSec}
				title="Custom duration in seconds"
			/>
		</div>

		<div class="options-row">
			<label class="option-label">
				<input type="checkbox" bind:checked={autoReturn} />
				Auto-return
			</label>
			<div class="preroll-select">
				<span class="option-text">Pre-roll:</span>
				<select bind:value={preRollMs} class="preroll-dropdown">
					{#each preRollPresets as p}
						<option value={p.value}>{p.label}</option>
					{/each}
				</select>
			</div>
		</div>

		<div class="action-row">
			<button class="action-btn ad-break-btn" onclick={handleAdBreak}>
				AD BREAK
			</button>
			{#if hasActiveOut}
				<button class="action-btn return-btn" onclick={handleReturn}>
					RETURN
				</button>
			{/if}
		</div>

		<!-- Active events -->
		{#if activeList.length > 0}
			<div class="active-events">
				{#each activeList as evt (evt.eventId)}
					<div class="active-event" class:held={evt.held}>
						<div class="evt-header">
							<span class="evt-type">{evt.commandType === 'time_signal' ? 'TIME SIG' : 'SPLICE'}</span>
							<span class="evt-id">#{evt.eventId}</span>
							<span class="evt-countdown">{formatCountdown(evt)}</span>
						</div>
						<div class="evt-actions">
							{#if evt.autoReturn && !evt.held}
								<button class="evt-btn hold-btn" onclick={() => handleHold(evt.eventId)} title="Hold (prevent auto-return)">
									HOLD
								</button>
							{/if}
							<div class="extend-group">
								<input
									type="text"
									class="extend-input"
									bind:value={extendDurationSec}
									placeholder="30"
									title="Extend duration in seconds"
								/>
								<button class="evt-btn extend-btn" onclick={() => handleExtend(evt.eventId)} title="Extend break duration">
									EXTEND
								</button>
							</div>
							<button class="evt-btn cancel-evt-btn" onclick={() => handleCancelEvent(evt.eventId)} title="Cancel event">
								CANCEL
							</button>
						</div>
					</div>
				{/each}
			</div>
		{/if}
	</div>

	<!-- Zone 2: Advanced Cue Builder -->
	<div class="zone zone-advanced">
		<div class="zone-header">
			<span class="zone-title">CUE BUILDER</span>
		</div>

		<div class="adv-tabs">
			<button
				class="adv-tab"
				class:active={advancedTab === 'splice_insert'}
				onclick={() => { advancedTab = 'splice_insert'; }}
			>
				Splice Insert
			</button>
			<button
				class="adv-tab"
				class:active={advancedTab === 'time_signal'}
				onclick={() => { advancedTab = 'time_signal'; }}
			>
				Time Signal
			</button>
		</div>

		{#if advancedTab === 'time_signal'}
			<div class="adv-fields">
				<div class="field-row">
					<label class="field-label">Segmentation:
					<select class="field-select" bind:value={segmentationType}>
						{#each segmentationTypes as st}
							<option value={st.value}>{st.label}</option>
						{/each}
					</select>
					</label>
				</div>
				<div class="field-row">
					<label class="field-label">UPID Type:
					<select class="field-select" bind:value={upidType}>
						{#each upidTypes as ut}
							<option value={ut.value}>{ut.label}</option>
						{/each}
					</select>
					</label>
				</div>
				<div class="field-row">
					<label class="field-label">UPID:
					<input type="text" class="field-input" bind:value={upidText} placeholder="e.g. ABCD0001000H" />
					</label>
				</div>
			</div>
		{/if}

		<div class="adv-fields">
			<div class="field-row">
				<label class="field-label">Duration (s):
				<input type="text" class="field-input field-narrow" bind:value={advancedDurationSec} placeholder="30" />
				</label>
			</div>
			<div class="field-row">
				<label class="field-label">Timing:
				<select class="field-select field-narrow" bind:value={advancedTiming}>
					<option value="immediate">Immediate</option>
					<option value="scheduled">Scheduled</option>
				</select>
				</label>
			</div>
			{#if advancedTiming === 'scheduled'}
				<div class="field-row">
					<label class="field-label">Pre-roll (ms):
					<input type="text" class="field-input field-narrow" bind:value={advancedPreRollMs} placeholder="2000" />
					</label>
				</div>
			{/if}
		</div>

		<button class="action-btn send-cue-btn" onclick={handleSendAdvancedCue}>
			SEND CUE
		</button>
	</div>

	<!-- Zone 3: Event Log -->
	<div class="zone zone-log">
		<div class="zone-header">
			<span class="zone-title">EVENT LOG</span>
			<span class="log-count">{eventLog.length}</span>
		</div>
		<div class="log-list">
			{#if eventLog.length === 0}
				<div class="empty-state">No events</div>
			{:else}
				{#each eventLog.slice().reverse() as evt (evt.eventId + '-' + evt.timestamp)}
					<button class="log-item log-item-btn {eventTypeClass(evt)}" onclick={() => openEventDetail(evt)}>
						<span class="log-type-badge">{eventTypeLabel(evt)}</span>
						<span class="log-id">#{evt.eventId}</span>
						{#if evt.durationMs}
							<span class="log-duration">{(evt.durationMs / 1000).toFixed(0)}s</span>
						{/if}
						<span class="log-time">{formatTimestamp(evt.timestamp)}</span>
						<span class="log-status">{evt.status}</span>
					</button>
				{/each}
			{/if}
		</div>
	</div>

	<!-- Event Detail Flyout -->
	{#if selectedEvent}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="detail-backdrop" onclick={closeEventDetail} onkeydown={(e) => e.key === 'Escape' && closeEventDetail()}></div>
		<div class="detail-flyout">
			<div class="detail-header">
				<span class="detail-title">Event #{selectedEvent.eventId}</span>
				<button class="detail-close" onclick={closeEventDetail}>x</button>
			</div>
			<div class="detail-body">
				<div class="detail-section">
					<div class="detail-section-title">Command</div>
					<div class="detail-grid">
						<span class="detail-label">Type</span>
						<span class="detail-value">{selectedEvent.commandType}</span>
						<span class="detail-label">Direction</span>
						<span class="detail-value">{selectedEvent.isOut ? 'OUT (break start)' : 'IN (return)'}</span>
						<span class="detail-label">Status</span>
						<span class="detail-value">{selectedEvent.status}</span>
						<span class="detail-label">Event ID</span>
						<span class="detail-value detail-mono">{selectedEvent.eventId}</span>
					</div>
				</div>

				<div class="detail-section">
					<div class="detail-section-title">Timing</div>
					<div class="detail-grid">
						<span class="detail-label">Timestamp</span>
						<span class="detail-value detail-mono">{formatFullTimestamp(selectedEvent.timestamp)}</span>
						{#if selectedEvent.durationMs}
							<span class="detail-label">Duration</span>
							<span class="detail-value detail-mono">{(selectedEvent.durationMs / 1000).toFixed(1)}s ({selectedEvent.durationMs}ms)</span>
						{/if}
						<span class="detail-label">Auto-Return</span>
						<span class="detail-value">{selectedEvent.autoReturn ? 'Yes' : 'No'}</span>
						{#if selectedEvent.spliceTimePts}
							<span class="detail-label">Splice Time</span>
							<span class="detail-value detail-mono">{formatPts(selectedEvent.spliceTimePts)}</span>
						{/if}
					</div>
				</div>

				{#if selectedEvent.availNum !== undefined || selectedEvent.availsExpected !== undefined}
					<div class="detail-section">
						<div class="detail-section-title">Avail</div>
						<div class="detail-grid">
							{#if selectedEvent.availNum !== undefined}
								<span class="detail-label">Avail Num</span>
								<span class="detail-value detail-mono">{selectedEvent.availNum}</span>
							{/if}
							{#if selectedEvent.availsExpected !== undefined}
								<span class="detail-label">Avails Expected</span>
								<span class="detail-value detail-mono">{selectedEvent.availsExpected}</span>
							{/if}
						</div>
					</div>
				{/if}

				{#if selectedEvent.descriptors && selectedEvent.descriptors.length > 0}
					<div class="detail-section">
						<div class="detail-section-title">Descriptors ({selectedEvent.descriptors.length})</div>
						{#each selectedEvent.descriptors as desc, i}
							<div class="detail-descriptor">
								<div class="detail-descriptor-header">Descriptor {i + 1}</div>
								<div class="detail-grid">
									<span class="detail-label">Seg Type</span>
									<span class="detail-value">0x{desc.segmentationType.toString(16).padStart(2, '0')} &mdash; {formatSegType(desc.segmentationType)}</span>
									<span class="detail-label">Seg Event ID</span>
									<span class="detail-value detail-mono">{desc.segEventId}</span>
									<span class="detail-label">UPID Type</span>
									<span class="detail-value">{desc.upidType} &mdash; {formatUpidType(desc.upidType)}</span>
									<span class="detail-label">UPID</span>
									<span class="detail-value detail-mono">{desc.upid || '—'}</span>
									{#if desc.durationTicks}
										<span class="detail-label">Duration</span>
										<span class="detail-value detail-mono">{formatPts(desc.durationTicks)}</span>
									{/if}
									{#if desc.subSegmentNum !== undefined}
										<span class="detail-label">Sub-segment</span>
										<span class="detail-value detail-mono">{desc.subSegmentNum} / {desc.subSegmentsExpected ?? '?'}</span>
									{/if}
								</div>
							</div>
						{/each}
					</div>
				{/if}

				{#if selectedEvent.source || selectedEvent.destinationId}
					<div class="detail-section">
						<div class="detail-section-title">Routing</div>
						<div class="detail-grid">
							{#if selectedEvent.source}
								<span class="detail-label">Source</span>
								<span class="detail-value">{selectedEvent.source}</span>
							{/if}
							{#if selectedEvent.destinationId}
								<span class="detail-label">Destination</span>
								<span class="detail-value detail-mono">{selectedEvent.destinationId}</span>
							{/if}
						</div>
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.scte35-panel {
		display: grid;
		grid-template-columns: 1fr 1fr 1fr;
		gap: 6px;
		padding: 6px;
		height: 100%;
		overflow: hidden;
	}

	.zone {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 6px;
		background: var(--bg-elevated);
		border-radius: var(--radius-sm);
		overflow-y: auto;
	}

	.zone-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0 2px;
	}

	.zone-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
	}

	/* Status badge */
	.status-badge {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.04em;
		padding: 2px 6px;
		border-radius: var(--radius-sm);
	}

	.status-on-air {
		background: var(--tally-preview-light);
		color: var(--tally-preview);
		border: 1px solid var(--tally-preview-medium);
	}

	.status-break {
		background: var(--tally-program-light);
		color: var(--tally-program);
		border: 1px solid var(--tally-program-medium);
		animation: pulse-break 1.5s ease-in-out infinite;
	}

	.status-held {
		background: var(--accent-orange-light);
		color: var(--accent-orange);
		border: 1px solid var(--accent-orange-medium);
	}

	@keyframes pulse-break {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.6; }
	}

	/* Duration row */
	.duration-row {
		display: flex;
		gap: 3px;
		align-items: center;
	}

	.dur-btn {
		flex: 1;
		padding: 4px 2px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 500;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.dur-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.dur-btn.active {
		background: rgba(59, 130, 246, 0.15);
		border-color: var(--accent-blue);
		color: var(--accent-blue);
	}

	.dur-custom {
		width: 50px;
		padding: 4px 4px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		text-align: center;
	}

	.dur-custom::placeholder {
		color: var(--text-tertiary);
		font-family: var(--font-ui);
	}

	.dur-custom:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	/* Options row */
	.options-row {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}

	.option-label {
		display: flex;
		align-items: center;
		gap: 3px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.option-text {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	.preroll-select {
		display: flex;
		align-items: center;
		gap: 3px;
		margin-left: auto;
	}

	.preroll-dropdown {
		padding: 2px 4px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
	}

	/* Action buttons */
	.action-row {
		display: flex;
		gap: 4px;
	}

	.action-btn {
		flex: 1;
		padding: 6px;
		border: none;
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 700;
		cursor: pointer;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		transition: background var(--transition-fast);
	}

	.ad-break-btn {
		background: rgba(220, 38, 38, 0.25);
		color: var(--tally-program);
		border: 1px solid rgba(220, 38, 38, 0.35);
	}

	.ad-break-btn:hover {
		background: rgba(220, 38, 38, 0.4);
	}

	.return-btn {
		background: rgba(22, 163, 74, 0.25);
		color: var(--tally-preview);
		border: 1px solid rgba(22, 163, 74, 0.35);
	}

	.return-btn:hover {
		background: rgba(22, 163, 74, 0.4);
	}

	.send-cue-btn {
		background: var(--accent-blue-light);
		color: var(--accent-blue);
		border: 1px solid var(--accent-blue-medium);
		padding: 6px;
	}

	.send-cue-btn:hover {
		background: rgba(59, 130, 246, 0.35);
	}

	/* Active events */
	.active-events {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.active-event {
		padding: 4px 6px;
		background: rgba(220, 38, 38, 0.08);
		border: 1px solid var(--tally-program-light);
		border-radius: var(--radius-sm);
	}

	.active-event.held {
		background: rgba(245, 158, 11, 0.08);
		border-color: var(--accent-orange-light);
	}

	.evt-header {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 3px;
	}

	.evt-type {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-secondary);
	}

	.evt-id {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	.evt-countdown {
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		font-weight: 700;
		color: var(--tally-program);
	}

	.active-event.held .evt-countdown {
		color: var(--accent-orange);
	}

	.evt-actions {
		display: flex;
		gap: 3px;
		align-items: center;
	}

	.evt-btn {
		padding: 2px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		text-transform: uppercase;
	}

	.hold-btn {
		background: rgba(245, 158, 11, 0.15);
		color: var(--accent-orange);
		border-color: var(--accent-orange-medium);
	}

	.hold-btn:hover {
		background: var(--accent-orange-medium);
	}

	.extend-group {
		display: flex;
		align-items: center;
		gap: 2px;
	}

	.extend-input {
		width: 28px;
		padding: 2px 3px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		text-align: center;
	}

	.extend-btn {
		background: rgba(59, 130, 246, 0.15);
		color: var(--accent-blue);
		border-color: var(--accent-blue-medium);
	}

	.extend-btn:hover {
		background: var(--accent-blue-medium);
	}

	.cancel-evt-btn {
		background: var(--bg-base);
		color: var(--text-tertiary);
		margin-left: auto;
	}

	.cancel-evt-btn:hover {
		color: var(--color-error);
		background: var(--bg-hover);
	}

	/* Advanced Cue Builder */
	.adv-tabs {
		display: flex;
		gap: 0;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		overflow: hidden;
	}

	.adv-tab {
		flex: 1;
		padding: 4px 6px;
		background: var(--bg-base);
		border: none;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--text-tertiary);
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.adv-tab:first-child {
		border-right: 1px solid var(--border-default);
	}

	.adv-tab.active {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.adv-tab:hover:not(.active) {
		color: var(--text-secondary);
	}

	.adv-fields {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.field-row {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.field-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		white-space: nowrap;
		min-width: 65px;
	}

	.field-select {
		flex: 1;
		padding: 3px 4px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		min-width: 0;
	}

	.field-input {
		flex: 1;
		padding: 3px 4px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		min-width: 0;
	}

	.field-input:focus,
	.field-select:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.field-narrow {
		max-width: 80px;
	}

	/* Event Log */
	.zone-log {
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	.log-count {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		padding: 1px 4px;
		background: var(--bg-base);
		border-radius: var(--radius-sm);
	}

	.log-list {
		flex: 1;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.log-item {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 3px 4px;
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		width: 100%;
		border: 1px solid transparent;
		cursor: pointer;
		text-align: left;
		transition: border-color var(--transition-fast), background var(--transition-fast);
	}

	.log-item:hover {
		border-color: var(--border-default);
		background: var(--bg-hover);
	}

	.log-cue-out {
		background: var(--tally-program-subtle);
	}

	.log-return {
		background: var(--tally-preview-subtle);
	}

	.log-cancelled {
		background: rgba(255, 255, 255, 0.02);
		opacity: 0.6;
	}

	.log-type-badge {
		padding: 1px 4px;
		border-radius: var(--radius-xs);
		font-size: var(--text-2xs);
		font-weight: 700;
		font-family: var(--font-ui);
		white-space: nowrap;
	}

	.log-cue-out .log-type-badge {
		background: var(--tally-program-light);
		color: var(--tally-program);
	}

	.log-return .log-type-badge {
		background: var(--tally-preview-light);
		color: var(--tally-preview);
	}

	.log-cancelled .log-type-badge {
		background: rgba(255, 255, 255, 0.05);
		color: var(--text-tertiary);
	}

	.log-id {
		color: var(--text-tertiary);
	}

	.log-duration {
		color: var(--text-secondary);
	}

	.log-time {
		margin-left: auto;
		color: var(--text-tertiary);
	}

	.log-status {
		color: var(--text-tertiary);
		font-size: var(--text-2xs);
		min-width: 40px;
		text-align: right;
	}

	.empty-state {
		text-align: center;
		color: var(--text-tertiary);
		font-size: var(--text-xs);
		font-family: var(--font-ui);
		padding: 12px 4px;
	}

	/* Getting Started Guide */
	.guide-banner {
		grid-column: 1 / -1;
		padding: 8px 10px;
		background: rgba(59, 130, 246, 0.08);
		border: 1px solid var(--accent-blue-light);
		border-radius: var(--radius-sm);
	}

	.guide-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 4px;
	}

	.guide-title {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 700;
		color: var(--accent-blue);
	}

	.guide-dismiss {
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: var(--text-sm);
		padding: 0 4px;
		line-height: 1;
	}

	.guide-dismiss:hover {
		color: var(--text-primary);
	}

	.guide-text {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		margin: 0 0 6px 0;
		line-height: 1.4;
	}

	.guide-steps {
		margin: 0 0 8px 0;
		padding-left: 16px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		line-height: 1.6;
	}

	.guide-steps strong {
		color: var(--text-primary);
	}

	.demo-btn {
		padding: 5px 12px;
		background: var(--accent-blue-light);
		color: var(--accent-blue);
		border: 1px solid var(--accent-blue-medium);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.demo-btn:hover {
		background: rgba(59, 130, 246, 0.35);
	}

	.guide-show-btn {
		width: 16px;
		height: 16px;
		padding: 0;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: 50%;
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		cursor: pointer;
		display: flex;
		align-items: center;
		justify-content: center;
		line-height: 1;
	}

	.guide-show-btn:hover {
		color: var(--accent-blue);
		border-color: var(--accent-blue);
	}

	/* Responsive: stack zones vertically on narrow screens */
	@media (max-width: 1024px) {
		.scte35-panel {
			grid-template-columns: 1fr 1fr;
			grid-template-rows: auto auto;
		}

		.zone-log {
			grid-column: 1 / -1;
		}
	}

	@media (max-width: 768px) {
		.scte35-panel {
			grid-template-columns: 1fr;
		}

		.zone-log {
			grid-column: auto;
			max-height: 120px;
		}
	}

	/* Event Detail Flyout */
	.detail-backdrop {
		position: fixed;
		inset: 0;
		background: var(--overlay-medium);
		z-index: var(--z-overlay);
	}

	.detail-flyout {
		position: fixed;
		top: 0;
		right: 0;
		bottom: 0;
		width: 360px;
		max-width: 90vw;
		background: var(--bg-elevated);
		border-left: 1px solid var(--border-default);
		z-index: var(--z-overlay);
		display: flex;
		flex-direction: column;
		overflow: hidden;
		box-shadow: -4px 0 24px rgba(0, 0, 0, 0.3);
	}

	.detail-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 10px 12px;
		border-bottom: 1px solid var(--border-default);
		background: var(--bg-base);
	}

	.detail-title {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 700;
		color: var(--text-primary);
	}

	.detail-close {
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: var(--text-md);
		padding: 2px 6px;
		border-radius: var(--radius-sm);
	}

	.detail-close:hover {
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	.detail-body {
		flex: 1;
		overflow-y: auto;
		padding: 8px 12px;
		display: flex;
		flex-direction: column;
		gap: 10px;
	}

	.detail-section {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.detail-section-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		padding-bottom: 2px;
		border-bottom: 1px solid var(--border-default);
	}

	.detail-grid {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 2px 8px;
		align-items: baseline;
	}

	.detail-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.detail-value {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-primary);
		word-break: break-all;
	}

	.detail-mono {
		font-family: var(--font-mono);
	}

	.detail-descriptor {
		padding: 4px 6px;
		background: var(--bg-base);
		border-radius: var(--radius-sm);
		border: 1px solid var(--border-default);
	}

	.detail-descriptor-header {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-secondary);
		margin-bottom: 3px;
	}
</style>
