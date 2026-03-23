<script lang="ts">
	import type { ControlRoomState, SCTE35CueRequest, SCTE35Active, SCTE35Event, SCTE35DescriptorRequest } from '$lib/api/types';
	import { scte35Cue, scte35Return, scte35Hold, scte35Extend, scte35Cancel, apiCall } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import {
		ICON_BROADCAST,
		ICON_AD_BREAK,
		ICON_RETURN,
		ICON_HELP,
		ICON_CLOSE,
		ICON_CHEVRON_DOWN,
		ICON_CHEVRON_RIGHT,
		ICON_WARNING,
		ICON_INFO,
		renderIcon,
	} from '$lib/icons';

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

	// --- Event log inline detail ---
	let expandedLogKey = $state<string | null>(null);

	function logEntryKey(evt: SCTE35Event): string {
		return `${evt.eventId}-${evt.timestamp}`;
	}

	function toggleLogDetail(evt: SCTE35Event) {
		const k = logEntryKey(evt);
		expandedLogKey = expandedLogKey === k ? null : k;
	}

	function isLogExpanded(evt: SCTE35Event): boolean {
		return expandedLogKey === logEntryKey(evt);
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
	{#if !guideDismissed}
		<div class="tip-banner" role="status">
			<span class="tip-banner__icon" aria-hidden="true">{@html renderIcon(ICON_INFO, 14)}</span>
			<p class="tip-banner__text">
				SCTE-35 cues ad breaks in MPEG-TS. Use <strong>AD BREAK</strong> / <strong>HOLD</strong> / <strong>EXTEND</strong> / <strong>RETURN</strong>; <strong>Cue Builder</strong> for time_signal and descriptors.
			</p>
			<button type="button" class="tip-banner__demo" onclick={handleRunDemo}>
				{@html renderIcon(ICON_BROADCAST, 12)}
				<span>Demo 60s</span>
			</button>
			<button type="button" class="tip-banner__close" onclick={toggleGuide} title="Dismiss tip" aria-label="Dismiss tip">
				{@html renderIcon(ICON_CLOSE, 14)}
			</button>
		</div>
	{/if}

	<div class="scte35-columns">
		<!-- Zone 1: Quick Actions -->
		<div class="zone zone-quick">
			<div class="zone-header">
				<span class="zone-header__left">
					<span class="zone-header__icon" aria-hidden="true">{@html renderIcon(ICON_BROADCAST, 12)}</span>
					<span class="zone-title">QUICK ACTIONS</span>
				</span>
				<span class="status-badge {statusClass}">{statusLabel}</span>
				{#if guideDismissed}
					<button type="button" class="guide-help-btn" onclick={toggleGuide} title="Show SCTE-35 tip" aria-label="Show SCTE-35 tip">
						{@html renderIcon(ICON_HELP, 12)}
					</button>
				{/if}
			</div>

			<div class="zone-body">
				<div class="chips-row">
					{#each durationPresets as preset}
						<button
							type="button"
					class="dur-chip dur-btn"
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

				<div class="inline-options">
					<label class="inline-options__check">
						<input type="checkbox" bind:checked={autoReturn} />
						<span>Auto-return</span>
					</label>
					<label class="inline-options__preroll">
						<span class="inline-options__label">Pre-roll:</span>
						<select bind:value={preRollMs} class="preroll-dropdown">
							{#each preRollPresets as p}
								<option value={p.value}>{p.label}</option>
							{/each}
						</select>
					</label>
				</div>

				<div class="action-row action-row--primary">
					<button type="button" class="action-btn ad-break-btn" onclick={handleAdBreak}>
						<span class="action-btn__icon" aria-hidden="true">{@html renderIcon(ICON_AD_BREAK, 15, 'fill')}</span>
						AD BREAK
					</button>
					{#if hasActiveOut}
						<button type="button" class="action-btn return-btn" onclick={handleReturn}>
							<span class="action-btn__icon" aria-hidden="true">{@html renderIcon(ICON_RETURN, 15, 'fill')}</span>
							RETURN
						</button>
					{/if}
				</div>

				{#if activeList.length > 0}
					<div class="active-events">
						{#each activeList as evt (evt.eventId)}
							<div class="active-card" class:held={evt.held}>
								<div class="active-card__main">
									<span class="active-card__type">{evt.commandType === 'time_signal' ? 'TIME SIG' : 'SPLICE'}</span>
									<span class="active-card__id">#{evt.eventId}</span>
									<span class="active-card__cd evt-countdown">{formatCountdown(evt)}</span>
								</div>
								<div class="active-card__actions">
									{#if evt.autoReturn && !evt.held}
										<button type="button" class="evt-btn hold-btn" onclick={() => handleHold(evt.eventId)} title="Hold (prevent auto-return)">
											HOLD
										</button>
									{/if}
									<span class="extend-inline">
										<input
											type="text"
											class="extend-input"
											bind:value={extendDurationSec}
											placeholder="30"
											title="Extend duration in seconds"
										/>
										<button type="button" class="evt-btn extend-btn" onclick={() => handleExtend(evt.eventId)} title="Extend break duration">
											EXTEND
										</button>
									</span>
									<button type="button" class="evt-btn cancel-evt-btn" onclick={() => handleCancelEvent(evt.eventId)} title="Cancel event">
										CANCEL
									</button>
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</div>
		</div>

		<!-- Zone 2: Cue Builder -->
		<div class="zone zone-advanced">
			<div class="zone-header">
				<span class="zone-title">CUE BUILDER</span>
			</div>

			<div class="adv-tabs">
				<button
					type="button"
					class="adv-tab"
					class:active={advancedTab === 'splice_insert'}
					onclick={() => { advancedTab = 'splice_insert'; }}
				>
					Splice Insert
				</button>
				<button
					type="button"
					class="adv-tab"
					class:active={advancedTab === 'time_signal'}
					onclick={() => { advancedTab = 'time_signal'; }}
				>
					Time Signal
				</button>
			</div>

			<div class="zone-body zone-body--builder">
				{#if advancedTab === 'time_signal'}
					<div class="builder-grid builder-grid--2">
						<label class="field-block field-label">
							<span class="field-block__label">Segmentation:</span>
							<select class="field-control" bind:value={segmentationType}>
								{#each segmentationTypes as st}
									<option value={st.value}>{st.label}</option>
								{/each}
							</select>
						</label>
						<label class="field-block field-label">
							<span class="field-block__label">UPID Type:</span>
							<select class="field-control" bind:value={upidType}>
								{#each upidTypes as ut}
									<option value={ut.value}>{ut.label}</option>
								{/each}
							</select>
						</label>
						<label class="field-block field-block--full field-label">
							<span class="field-block__label">UPID:</span>
							<input type="text" class="field-control" bind:value={upidText} placeholder="e.g. ABCD0001000H" />
						</label>
					</div>
				{/if}

				<div class="builder-grid builder-grid--2">
					<label class="field-block field-label">
						<span class="field-block__label">Duration (s):</span>
						<input type="text" class="field-control" bind:value={advancedDurationSec} placeholder="30" />
					</label>
					<label class="field-block field-label">
						<span class="field-block__label">Timing:</span>
						<select class="field-control" bind:value={advancedTiming}>
							<option value="immediate">Immediate</option>
							<option value="scheduled">Scheduled</option>
						</select>
					</label>
					{#if advancedTiming === 'scheduled'}
						<label class="field-block field-block--full field-label">
							<span class="field-block__label">Pre-roll (ms):</span>
							<input type="text" class="field-control" bind:value={advancedPreRollMs} placeholder="2000" />
						</label>
					{/if}
				</div>
			</div>

			<div class="zone-footer">
				<button type="button" class="action-btn send-cue-btn" onclick={handleSendAdvancedCue}>
					SEND CUE
				</button>
			</div>
		</div>

		<!-- Zone 3: Event Log -->
		<div class="zone zone-log">
			<div class="zone-header">
				<span class="zone-title">EVENT LOG</span>
				<span class="log-count">{eventLog.length}</span>
			</div>
			<div class="zone-body zone-body--log">
				{#if eventLog.length === 0}
					<div class="empty-state">No events</div>
				{:else}
					{#each eventLog.slice().reverse() as evt (logEntryKey(evt))}
						<div class="log-entry {eventTypeClass(evt)}">
							<button
								type="button"
								class="log-row"
								onclick={() => toggleLogDetail(evt)}
								aria-expanded={isLogExpanded(evt)}
							>
								<span class="log-time-badge">{formatTimestamp(evt.timestamp)}</span>
								{#if evt.status === 'cancelled'}
									<span class="log-warn-ic" aria-hidden="true">{@html renderIcon(ICON_WARNING, 11, 'fill')}</span>
								{/if}
								<span class="log-type-badge">{eventTypeLabel(evt)}</span>
								{#if evt.durationMs}
									<span class="log-dur-badge">{(evt.durationMs / 1000).toFixed(0)}s</span>
								{/if}
								<span class="log-id">#{evt.eventId}</span>
								<span class="log-status-pill">{evt.status}</span>
								<span class="log-chevron" aria-hidden="true">
									{#if isLogExpanded(evt)}
										{@html renderIcon(ICON_CHEVRON_DOWN, 12)}
									{:else}
										{@html renderIcon(ICON_CHEVRON_RIGHT, 12)}
									{/if}
								</span>
							</button>
							{#if isLogExpanded(evt)}
								<div class="log-detail">
									<div class="log-detail__title">Event #{evt.eventId}</div>
									<div class="detail-section">
										<div class="detail-section-title">Command</div>
										<div class="detail-grid">
											<span class="detail-label">Type</span>
											<span class="detail-value">{evt.commandType}</span>
											<span class="detail-label">Direction</span>
											<span class="detail-value">{evt.isOut ? 'OUT (break start)' : 'IN (return)'}</span>
											<span class="detail-label">Status</span>
											<span class="detail-value">{evt.status}</span>
											<span class="detail-label">Event ID</span>
											<span class="detail-value detail-mono">{evt.eventId}</span>
										</div>
									</div>

									<div class="detail-section">
										<div class="detail-section-title">Timing</div>
										<div class="detail-grid">
											<span class="detail-label">Timestamp</span>
											<span class="detail-value detail-mono">{formatFullTimestamp(evt.timestamp)}</span>
											{#if evt.durationMs}
												<span class="detail-label">Duration</span>
												<span class="detail-value detail-mono">{(evt.durationMs / 1000).toFixed(1)}s ({evt.durationMs}ms)</span>
											{/if}
											<span class="detail-label">Auto-Return</span>
											<span class="detail-value">{evt.autoReturn ? 'Yes' : 'No'}</span>
											{#if evt.spliceTimePts}
												<span class="detail-label">Splice Time</span>
												<span class="detail-value detail-mono">{formatPts(evt.spliceTimePts)}</span>
											{/if}
										</div>
									</div>

									{#if evt.availNum !== undefined || evt.availsExpected !== undefined}
										<div class="detail-section">
											<div class="detail-section-title">Avail</div>
											<div class="detail-grid">
												{#if evt.availNum !== undefined}
													<span class="detail-label">Avail Num</span>
													<span class="detail-value detail-mono">{evt.availNum}</span>
												{/if}
												{#if evt.availsExpected !== undefined}
													<span class="detail-label">Avails Expected</span>
													<span class="detail-value detail-mono">{evt.availsExpected}</span>
												{/if}
											</div>
										</div>
									{/if}

									{#if evt.descriptors && evt.descriptors.length > 0}
										<div class="detail-section">
											<div class="detail-section-title">Descriptors ({evt.descriptors.length})</div>
											{#each evt.descriptors as desc, i}
												<div class="detail-descriptor">
													<div class="detail-descriptor-header">Descriptor {i + 1}</div>
													<div class="detail-grid">
														<span class="detail-label">Seg Type</span>
														<span class="detail-value">0x{desc.segmentationType.toString(16).padStart(2, '0')} — {formatSegType(desc.segmentationType)}</span>
														<span class="detail-label">Seg Event ID</span>
														<span class="detail-value detail-mono">{desc.segEventId}</span>
														<span class="detail-label">UPID Type</span>
														<span class="detail-value">{desc.upidType} — {formatUpidType(desc.upidType)}</span>
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

									{#if evt.source || evt.destinationId}
										<div class="detail-section">
											<div class="detail-section-title">Routing</div>
											<div class="detail-grid">
												{#if evt.source}
													<span class="detail-label">Source</span>
													<span class="detail-value">{evt.source}</span>
												{/if}
												{#if evt.destinationId}
													<span class="detail-label">Destination</span>
													<span class="detail-value detail-mono">{evt.destinationId}</span>
												{/if}
											</div>
										</div>
									{/if}
								</div>
							{/if}
						</div>
					{/each}
				{/if}
			</div>
		</div>
	</div>
</div>

<style>
	.scte35-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		overflow: hidden;
		padding: 6px;
		gap: 6px;
		font-family: var(--font-ui);
	}

	.tip-banner {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 5px 8px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		min-height: 0;
	}

	.tip-banner__icon {
		flex-shrink: 0;
		display: flex;
		color: var(--accent-blue);
	}

	.tip-banner__text {
		flex: 1;
		min-width: 0;
		margin: 0;
		font-size: var(--text-2xs);
		line-height: 1.35;
		color: var(--text-secondary);
	}

	.tip-banner__text strong {
		color: var(--text-primary);
		font-weight: 600;
	}

	.tip-banner__demo {
		flex-shrink: 0;
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 3px 8px;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--accent-blue);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.tip-banner__demo:hover {
		background: var(--bg-hover);
		border-color: var(--accent-blue);
	}

	.tip-banner__close {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		width: 26px;
		height: 26px;
		padding: 0;
		background: transparent;
		border: 1px solid transparent;
		border-radius: var(--radius-xs);
		color: var(--text-tertiary);
		cursor: pointer;
		transition: color var(--transition-fast), background var(--transition-fast);
	}

	.tip-banner__close:hover {
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	.scte35-columns {
		flex: 1;
		min-height: 0;
		display: grid;
		grid-template-columns: 1fr 1fr 1fr;
		gap: 6px;
		align-items: stretch;
	}

	.zone {
		display: flex;
		flex-direction: column;
		min-height: 0;
		overflow: hidden;
		padding: 6px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.zone-header {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 6px;
		padding-bottom: 6px;
		margin-bottom: 2px;
		border-bottom: 1px solid var(--border-subtle);
	}

	.zone-header__left {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		min-width: 0;
	}

	.zone-header__icon {
		display: flex;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.zone-title {
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
		white-space: nowrap;
	}

	.guide-help-btn {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		width: 22px;
		height: 22px;
		padding: 0;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-tertiary);
		cursor: pointer;
		transition: color var(--transition-fast), border-color var(--transition-fast);
	}

	.guide-help-btn:hover {
		color: var(--accent-blue);
		border-color: var(--accent-blue);
	}

	.zone-body {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.zone-body--builder {
		gap: 8px;
	}

	.zone-footer {
		flex-shrink: 0;
		padding-top: 6px;
		border-top: 1px solid var(--border-subtle);
		margin-top: 4px;
	}

	/* Status badge */
	.status-badge {
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.04em;
		padding: 2px 6px;
		border-radius: var(--radius-xs);
		white-space: nowrap;
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
		50% { opacity: 0.65; }
	}

	.action-row {
		display: flex;
		gap: 4px;
	}

	.action-row--primary {
		flex-shrink: 0;
	}

	.action-btn {
		flex: 1;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 5px;
		padding: 7px 6px;
		border-radius: var(--radius-sm);
		font-size: var(--text-xs);
		font-weight: 700;
		cursor: pointer;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.action-btn__icon {
		display: flex;
		flex-shrink: 0;
	}

	.ad-break-btn {
		background: color-mix(in srgb, var(--color-error) 22%, var(--bg-surface));
		color: var(--color-error);
		border: 1px solid color-mix(in srgb, var(--color-error) 45%, var(--border-strong));
	}

	.ad-break-btn:hover {
		background: color-mix(in srgb, var(--color-error) 32%, var(--bg-surface));
	}

	.return-btn {
		background: color-mix(in srgb, var(--color-success) 22%, var(--bg-surface));
		color: var(--color-success);
		border: 1px solid color-mix(in srgb, var(--color-success) 45%, var(--border-strong));
	}

	.return-btn:hover {
		background: color-mix(in srgb, var(--color-success) 32%, var(--bg-surface));
	}

	.send-cue-btn {
		width: 100%;
		background: var(--bg-control);
		color: var(--accent-blue);
		border: 1px solid var(--border-default);
	}

	.send-cue-btn:hover {
		background: var(--bg-hover);
		border-color: var(--accent-blue);
	}

	.chips-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 4px;
	}

	.dur-chip {
		padding: 3px 8px;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-secondary);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast), color var(--transition-fast);
	}

	.dur-chip:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.dur-chip.active {
		background: color-mix(in srgb, var(--accent-blue) 18%, var(--bg-surface));
		border-color: var(--accent-blue);
		color: var(--accent-blue);
	}

	.dur-custom {
		width: 36px;
		padding: 3px 4px;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
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

	.inline-options {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 10px 14px;
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.inline-options__check {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		cursor: pointer;
		white-space: nowrap;
	}

	.inline-options__preroll {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		margin-left: auto;
	}

	.inline-options__label {
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.preroll-dropdown {
		padding: 2px 6px;
		min-width: 4.5rem;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
	}

	/* Active events */
	.active-events {
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin-top: 2px;
	}

	.active-card {
		padding: 4px 6px;
		background: color-mix(in srgb, var(--color-error) 8%, var(--bg-surface));
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
	}

	.active-card.held {
		background: color-mix(in srgb, var(--accent-orange) 10%, var(--bg-surface));
		border-color: color-mix(in srgb, var(--accent-orange) 35%, var(--border-default));
	}

	.active-card__main {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 4px;
		font-size: var(--text-2xs);
	}

	.active-card__type {
		font-weight: 700;
		color: var(--text-secondary);
	}

	.active-card__id {
		font-family: var(--font-mono);
		color: var(--text-tertiary);
	}

	.active-card__cd {
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 700;
		color: var(--color-error);
	}

	.active-card.held .active-card__cd {
		color: var(--accent-orange);
	}

	.active-card__actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 4px;
	}

	.evt-btn {
		padding: 2px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		text-transform: uppercase;
		background: var(--bg-control);
		color: var(--text-secondary);
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.hold-btn {
		background: color-mix(in srgb, var(--accent-orange) 14%, var(--bg-surface));
		color: var(--accent-orange);
		border-color: color-mix(in srgb, var(--accent-orange) 40%, var(--border-default));
	}

	.hold-btn:hover {
		background: color-mix(in srgb, var(--accent-orange) 24%, var(--bg-surface));
	}

	.extend-inline {
		display: inline-flex;
		align-items: center;
		gap: 3px;
	}

	.extend-input {
		width: 28px;
		padding: 2px 3px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		text-align: center;
	}

	.extend-btn {
		background: color-mix(in srgb, var(--accent-blue) 14%, var(--bg-surface));
		color: var(--accent-blue);
		border-color: color-mix(in srgb, var(--accent-blue) 35%, var(--border-default));
	}

	.extend-btn:hover {
		background: color-mix(in srgb, var(--accent-blue) 22%, var(--bg-surface));
	}

	.cancel-evt-btn {
		margin-left: auto;
		color: var(--text-tertiary);
	}

	.cancel-evt-btn:hover {
		color: var(--color-error);
		background: var(--bg-hover);
	}

	/* Cue builder */
	.adv-tabs {
		flex-shrink: 0;
		display: flex;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		overflow: hidden;
		margin-bottom: 4px;
	}

	.adv-tab {
		flex: 1;
		padding: 4px 6px;
		background: var(--bg-control);
		border: none;
		font-size: var(--text-2xs);
		font-weight: 600;
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

	.builder-grid {
		display: grid;
		gap: 6px 8px;
	}

	.builder-grid--2 {
		grid-template-columns: 1fr 1fr;
	}

	.field-block {
		display: flex;
		flex-direction: column;
		gap: 3px;
		min-width: 0;
	}

	.field-block--full {
		grid-column: 1 / -1;
	}

	.field-block__label {
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.field-control {
		width: 100%;
		padding: 4px 6px;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		min-width: 0;
		box-sizing: border-box;
	}

	.field-control:is(input) {
		font-family: var(--font-mono);
	}

	.field-control:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	/* Event log */
	.log-count {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		padding: 1px 5px;
		background: var(--bg-control);
		border-radius: var(--radius-xs);
		border: 1px solid var(--border-subtle);
	}

	.zone-body--log {
		gap: 3px;
	}

	.log-entry {
		border-radius: var(--radius-xs);
		border: 1px solid var(--border-subtle);
		overflow: hidden;
		background: var(--bg-surface);
	}

	.log-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 4px 6px;
		width: 100%;
		padding: 4px 6px;
		margin: 0;
		background: transparent;
		border: none;
		cursor: pointer;
		text-align: left;
		font-family: var(--font-ui);
		transition: background var(--transition-fast);
	}

	.log-row:hover {
		background: var(--bg-hover);
	}

	.log-cue-out .log-row {
		background: color-mix(in srgb, var(--tally-program) 8%, transparent);
	}

	.log-return .log-row {
		background: color-mix(in srgb, var(--tally-preview) 8%, transparent);
	}

	.log-cancelled {
		opacity: 0.72;
	}

	.log-time-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 600;
		padding: 1px 4px;
		background: var(--bg-control);
		border-radius: var(--radius-xs);
		color: var(--text-secondary);
		border: 1px solid var(--border-subtle);
		white-space: nowrap;
	}

	.log-warn-ic {
		display: flex;
		color: var(--accent-orange);
		flex-shrink: 0;
	}

	.log-type-badge {
		display: inline-flex;
		align-items: center;
		padding: 1px 5px;
		border-radius: var(--radius-xs);
		font-size: var(--text-2xs);
		font-weight: 700;
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
		background: var(--bg-control);
		color: var(--text-tertiary);
	}

	.log-dur-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		white-space: nowrap;
	}

	.log-id {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.log-status-pill {
		margin-left: auto;
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		text-transform: lowercase;
		white-space: nowrap;
	}

	.log-chevron {
		display: flex;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.log-detail {
		padding: 6px 8px 8px;
		border-top: 1px solid var(--border-subtle);
		background: var(--bg-base);
		max-height: 240px;
		overflow-y: auto;
	}

	.log-detail__title {
		font-size: var(--text-xs);
		font-weight: 700;
		color: var(--text-primary);
		margin-bottom: 6px;
		font-family: var(--font-ui);
	}

	.empty-state {
		text-align: center;
		color: var(--text-tertiary);
		font-size: var(--text-xs);
		padding: 16px 8px;
	}

	.detail-section {
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin-bottom: 8px;
	}

	.detail-section:last-child {
		margin-bottom: 0;
	}

	.detail-section-title {
		font-size: var(--text-2xs);
		font-weight: 700;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		padding-bottom: 2px;
		border-bottom: 1px solid var(--border-subtle);
	}

	.detail-grid {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 2px 8px;
		align-items: baseline;
	}

	.detail-label {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.detail-value {
		font-size: var(--text-2xs);
		color: var(--text-primary);
		word-break: break-word;
	}

	.detail-mono {
		font-family: var(--font-mono);
	}

	.detail-descriptor {
		padding: 4px 6px;
		background: var(--bg-surface);
		border-radius: var(--radius-xs);
		border: 1px solid var(--border-subtle);
		margin-top: 4px;
	}

	.detail-descriptor-header {
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-secondary);
		margin-bottom: 3px;
	}

	@media (max-width: 1024px) {
		.scte35-columns {
			grid-template-columns: 1fr 1fr;
		}

		.zone-log {
			grid-column: 1 / -1;
			min-height: 140px;
		}
	}

	@media (max-width: 768px) {
		.scte35-columns {
			grid-template-columns: 1fr;
		}

		.zone-log {
			grid-column: auto;
			min-height: 160px;
		}

		.tip-banner {
			flex-wrap: wrap;
		}

		.tip-banner__text {
			flex-basis: 100%;
			order: 3;
		}
	}
</style>
