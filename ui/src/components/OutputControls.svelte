<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { getConfirmMode, setConfirmMode } from '$lib/state/preferences.svelte';
	import Clock from './Clock.svelte';
	import RecordingControl from './RecordingControl.svelte';
	import ConnectionStatus from './ConnectionStatus.svelte';
	import SyncIndicator from './SyncIndicator.svelte';
	import EngineRoleBadge from './EngineRoleBadge.svelte';
	import SRTHealthDot from './SRTHealthDot.svelte';
	import { computeOutputHealth } from '$lib/util/srt-health';
	import { formatBadgeText } from '$lib/util/format-badge';
	import FormatPicker from './FormatPicker.svelte';
	import type { SyncHealth } from '$lib/state/reconciler.svelte';
	import { getEngineUrls } from '$lib/api/base-url';
	import { fetchConfidenceThumbnail } from '$lib/api/switch-api';

	type ConnectionIndicatorState = 'webtransport' | 'polling' | 'disconnected';

	interface Props {
		state: ControlRoomState;
		connectionState?: ConnectionIndicatorState;
		switchLayout?: () => void;
		onToggleIOPanel?: () => void;
		ioPanelVisible?: boolean;
		onToggleComms?: () => void;
		commsActive?: boolean;
		syncHealth?: SyncHealth | null;
		onToggleRedundancyPanel?: () => void;
		isDualEngine?: boolean;
		engineBState?: ControlRoomState | null;
		activeEngineId?: 'A' | 'B';
	}
	let { state: crState, connectionState = 'disconnected', switchLayout, onToggleIOPanel, ioPanelVisible = false, onToggleComms, commsActive = false, syncHealth = null, onToggleRedundancyPanel, isDualEngine = false, engineBState = null, activeEngineId }: Props = $props();

	// Object URL of the latest program thumbnail; fetched, because an
	// <img src> request cannot carry the bearer token the API requires.
	let thumbUrl = $state<string | null>(null);
	const THUMB_POLL_MS = 1000;

	const srtActive = $derived(crState.srtOutput?.active ?? false);
	const recActive = $derived(crState.recording?.active ?? false);
	const showConfidence = $derived(recActive || srtActive);

	// I/O button is active when panel is open OR any SRT output/destination is active
	const hasActiveDestination = $derived(
		(crState.destinations ?? []).some((d) => d.state === 'active' || d.state === 'starting')
	);
	const ioActive = $derived(ioPanelVisible || srtActive || hasActiveDestination);

	// I/O button shows warning when any SRT input is unhealthy or any output has errors
	const hasUnhealthySRTInput = $derived(
		Object.values(crState.sources).some(
			(s) => s.type === 'srt' && s.status !== 'healthy'
		)
	);
	const hasOutputError = $derived(
		(crState.destinations ?? []).some((d) => d.state === 'error')
	);
	const ioWarning = $derived(hasUnhealthySRTInput || hasOutputError);

	const outputHealth = $derived(computeOutputHealth(crState.destinations));
	const badgeText = $derived(formatBadgeText(crState.pipelineFormat));

	// Per-engine output status — show dual dots when SRT output states differ
	const engineASrtActive = $derived(crState.srtOutput?.active ?? false);
	const engineBSrtActive = $derived(engineBState?.srtOutput?.active ?? false);
	const srtStatesDiffer = $derived(isDualEngine && engineASrtActive !== engineBSrtActive);

	// Derive base URL for confidence monitor so it routes to the authoritative engine
	const confidenceBaseUrl = $derived((() => {
		const engines = getEngineUrls();
		if (engines && engines.length > 0) {
			return engines[0].url; // Primary/authoritative engine
		}
		return ''; // Same-origin for single-engine mode
	})());

	$effect(() => {
		if (!showConfidence) return;
		const baseUrl = confidenceBaseUrl;
		const abort = new AbortController();
		let inFlight = false;
		let current: string | null = null;

		async function poll() {
			// One request at a time, and none while the tab is hidden.
			if (inFlight || document.hidden) return;
			inFlight = true;
			try {
				const blob = await fetchConfidenceThumbnail(baseUrl, abort.signal);
				if (blob && !abort.signal.aborted) {
					const next = URL.createObjectURL(blob);
					if (current) URL.revokeObjectURL(current);
					current = next;
					thumbUrl = next;
				}
			} catch {
				// Keep the last thumbnail; the next poll retries.
			} finally {
				inFlight = false;
			}
		}

		poll();
		const id = setInterval(poll, THUMB_POLL_MS);
		return () => {
			clearInterval(id);
			abort.abort();
			if (current) URL.revokeObjectURL(current);
			thumbUrl = null;
		};
	});
</script>

<div class="output-controls">
	<Clock />
	<ConnectionStatus state={connectionState} />
	{#if syncHealth}
		<SyncIndicator health={syncHealth} onclick={() => onToggleRedundancyPanel?.()} />
	{/if}
	{#if syncHealth?.warnings?.includes('clock_diverged')}
		<span class="clock-warn" title="Engine clocks diverged beyond safety margin — command timing may be inaccurate">
			CLOCK
		</span>
	{/if}
	{#if isDualEngine}
		<span class="dual-badge"
			class:synced={syncHealth?.status === 'synced'}
			class:warning={syncHealth?.status === 'lagging' || syncHealth?.status === 'diverged' || syncHealth?.status === 'recovering'}
			class:error={syncHealth?.status === 'mismatch' || syncHealth?.status === 'disconnected'}>
			DUAL
		</span>
		<span class="auth-badge" title="Commands routing to Engine {syncHealth?.authoritative ?? 'A'}">
			{syncHealth?.authoritative ?? 'A'}
		</span>
	{/if}
	<EngineRoleBadge engineLabel={crState.engineLabel} role={crState.role} />
	{#if isDualEngine && engineBState}
		<EngineRoleBadge engineLabel={engineBState.engineLabel} role={engineBState.role} />
	{/if}
	{#if badgeText}
		<FormatPicker {badgeText} {isDualEngine} />
	{/if}
	{#if showConfidence && thumbUrl}
		<img
			class="confidence-thumb"
			src={thumbUrl}
			alt="Program output"
			width="80"
			height="45"
		/>
	{/if}
	<RecordingControl
		state={crState}
		isDualEngine={isDualEngine}
		engineBRecording={engineBState?.recording}
		activeEngineId={activeEngineId}
	/>
	<button
		class="header-btn io-btn"
		class:io-active={ioActive}
		class:io-warning={ioWarning && !ioActive}
		onclick={() => onToggleIOPanel?.()}
	>I/O{#if outputHealth}<span class="io-dot-wrapper"><SRTHealthDot level={outputHealth} /></span>{/if}</button>
	{#if srtStatesDiffer}
		<span class="engine-output-status" data-testid="engine-output-status">
			<span class="engine-out-tag">A{#if engineASrtActive}<span class="out-dot active"></span>{:else}<span class="out-dot"></span>{/if}</span>
			<span class="engine-out-tag">B{#if engineBSrtActive}<span class="out-dot active"></span>{:else}<span class="out-dot"></span>{/if}</span>
		</span>
	{/if}
	<button
		class="header-btn comms-btn-header"
		class:comms-active={commsActive}
		onclick={() => onToggleComms?.()}
		title="Operator voice comms"
	>COMMS</button>
	<button
		class="header-btn confirm-btn"
		class:confirm-active={getConfirmMode()}
		onclick={() => setConfirmMode(!getConfirmMode())}
		title="Require double-press of Space or Shift+number for hot punches"
	>CONFIRM</button>
	{#if switchLayout}
		<button class="header-btn mode-btn" onclick={switchLayout} title="Switch layout mode">MODE</button>
	{/if}
</div>

<style>
	.output-controls {
		display: flex;
		align-items: center;
		gap: 5px;
		padding: 4px 8px;
		font-family: var(--font-ui);
	}

	.header-btn {
		padding: 4px 10px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: var(--text-sm);
		letter-spacing: 0.04em;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.header-btn:hover {
		border-color: var(--border-strong);
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	.io-active {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
		color: var(--accent-blue);
	}

	.io-warning {
		border-color: var(--color-warning);
		background: var(--color-warning-dim);
		color: var(--color-warning);
	}

	.comms-active {
		border-color: var(--color-green, #4ade80);
		background: rgba(74, 222, 128, 0.15);
		color: #fff;
	}

	.confirm-btn {
		font-size: var(--text-xs);
	}

	.confirm-active {
		border-color: var(--accent-orange);
		background: var(--accent-orange-dim);
		color: var(--accent-orange);
	}

	.io-dot-wrapper {
		display: inline-flex;
		margin-left: 4px;
		vertical-align: middle;
	}

	.io-btn :global(.srt-dot) {
		position: static;
		width: 6px;
		height: 6px;
	}

	.confidence-thumb {
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		object-fit: cover;
		opacity: 0.85;
	}

	.confidence-thumb:hover {
		opacity: 1;
	}

	.engine-output-status {
		display: inline-flex;
		gap: 4px;
		align-items: center;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-dim);
	}

	.engine-out-tag {
		display: inline-flex;
		align-items: center;
		gap: 2px;
	}

	.out-dot {
		display: inline-block;
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--text-dim);
	}

	.out-dot.active {
		background: var(--accent-blue, #3b82f6);
	}

	.dual-badge {
		font-size: 0.6rem;
		font-weight: 700;
		padding: 2px 5px;
		border-radius: 3px;
		background: var(--tally-preview, #22c55e);
		color: #fff;
		letter-spacing: 0.05em;
	}

	.dual-badge.warning {
		background: var(--accent-yellow, #eab308);
		color: #000;
	}

	.dual-badge.error {
		background: #ef4444;
	}

	.clock-warn {
		background: var(--accent-yellow, #eab308);
		color: #000;
		font-size: 0.65rem;
		font-weight: 700;
		padding: 2px 6px;
		border-radius: 3px;
		animation: pulse-warn 2s ease-in-out infinite;
	}

	@keyframes pulse-warn {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.6; }
	}

	.auth-badge {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.2em;
		height: 1.2em;
		border-radius: 3px;
		font-size: 0.7em;
		font-weight: 700;
		background: var(--surface-2, #333);
		color: var(--text-primary, #fff);
		margin-left: -1px;
	}
</style>
