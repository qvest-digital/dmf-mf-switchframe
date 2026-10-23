<script lang="ts">
	import type { SyncHealth, SyncStatus, EngineHealthDetail } from '$lib/state/reconciler.svelte';
	import type { DivergenceLogEntry } from '$lib/state/divergence-log.svelte';
	import { authHeaders } from '$lib/api/switch-api';
	import type { AudioChannelComparison } from '$lib/util/audio-drift';
	import EngineDot from './EngineDot.svelte';

	interface OutputStatus {
		recording: { A: boolean; B: boolean };
		destinations: {
			name: string;
			A: { state: string; bitrate?: number };
			B: { state: string; bitrate?: number };
		}[];
	}

	interface PlayoutComparison {
		itemA: string;
		itemB: string;
		inSync: boolean;
		ptsFrameDelta?: number;
		cacheA?: string;
		cacheB?: string;
	}

	interface Props {
		health: SyncHealth;
		engineHealth: { A: EngineHealthDetail; B: EngineHealthDetail };
		divergenceLog: DivergenceLogEntry[];
		visible: boolean;
		onclose: () => void;
		onforcereconcile?: (direction: 'AtoB' | 'BtoA') => void;
		oncompare?: () => void;
		audioComparison?: AudioChannelComparison[] | null;
		outputStatus?: OutputStatus | null;
		playoutComparison?: PlayoutComparison | null;
		engineBUrl?: string | null;
		commandLatency?: { A: number | null; B: number | null } | null;
	}

	let { health, engineHealth, divergenceLog, visible, onclose, onforcereconcile, oncompare, audioComparison = null, outputStatus = null, playoutComparison = null, engineBUrl = null, commandLatency = null }: Props = $props();

	// --- Collapsible section state ---
	let sourcesExpanded = $state(false);
	let audioExpanded = $state(false);
	let playoutExpanded = $state(false);
	let commandLatencyExpanded = $state(false);
	let showAllLog = $state(false);

	// --- Confidence thumbnails ---
	let thumbUrlA = $state<string>('');
	let thumbUrlB = $state<string>('');

	// engineHealth is a new object on every reconciler tick. The poll below
	// reads only these values, so it restarts, and blanks both thumbnails,
	// only when one of them changes rather than several times a second.
	const pollVisible = $derived(visible);
	const pollEngineBUrl = $derived(engineBUrl);
	const connectedA = $derived(engineHealth.A.connected);
	const connectedB = $derived(engineHealth.B.connected);

	$effect(() => {
		if (!pollVisible) return;
		if (!connectedA) return;

		const abort = new AbortController();
		let cancelled = false;

		async function fetchThumbnails() {
			if (cancelled) return;

			// Engine A: same origin
			try {
				const res = await fetch('/api/output/confidence', {
					headers: authHeaders(),
					signal: abort.signal,
				});
				if (res.ok && !cancelled) {
					const blob = await res.blob();
					const oldUrl = thumbUrlA;
					thumbUrlA = URL.createObjectURL(blob);
					if (oldUrl) URL.revokeObjectURL(oldUrl);
				}
			} catch { /* aborted or network error */ }

			// Engine B: different origin
			if (pollEngineBUrl && connectedB) {
				try {
					const res = await fetch(`${pollEngineBUrl}/api/output/confidence`, {
						headers: authHeaders(),
						signal: abort.signal,
					});
					if (res.ok && !cancelled) {
						const blob = await res.blob();
						const oldUrl = thumbUrlB;
						thumbUrlB = URL.createObjectURL(blob);
						if (oldUrl) URL.revokeObjectURL(oldUrl);
					}
				} catch { /* aborted or network error */ }
			}
		}

		// Fetch immediately, then every 1s
		fetchThumbnails();
		const interval = setInterval(fetchThumbnails, 1000);

		return () => {
			cancelled = true;
			abort.abort();
			clearInterval(interval);
			if (thumbUrlA) { URL.revokeObjectURL(thumbUrlA); thumbUrlA = ''; }
			if (thumbUrlB) { URL.revokeObjectURL(thumbUrlB); thumbUrlB = ''; }
		};
	});

	// --- Force reconcile confirmation ---
	let pendingReconcile = $state<'AtoB' | 'BtoA' | null>(null);

	function confirmReconcile() {
		if (pendingReconcile) {
			onforcereconcile?.(pendingReconcile);
			pendingReconcile = null;
		}
	}

	function cancelReconcile() {
		pendingReconcile = null;
	}

	const pendingReconcileMessage = $derived.by((): string => {
		if (pendingReconcile === 'AtoB') {
			return "Push Engine A's complete state to Engine B? Engine B's current state will be overwritten.";
		}
		if (pendingReconcile === 'BtoA') {
			return "Push Engine B's complete state to Engine A? Engine A's current state will be overwritten.";
		}
		return '';
	});

	const LOG_DISPLAY_LIMIT = 20;
	const OLD_ENTRY_THRESHOLD_MS = 30 * 60 * 1000; // 30 minutes

	// --- Status helpers ---
	const statusLabel = $derived.by((): string => {
		const map: Record<SyncStatus, string> = {
			synced: 'NOMINAL',
			lagging: 'LAGGING',
			diverged: 'DIVERGED',
			mismatch: 'MISMATCH',
			recovering: 'RECOVERING',
			'single-engine': 'SINGLE ENGINE',
			disconnected: 'NO ENGINES',
		};
		return map[health.status];
	});

	const statusClass = $derived.by((): string => {
		const map: Record<SyncStatus, string> = {
			synced: 'badge-nominal',
			lagging: 'badge-lagging',
			diverged: 'badge-error',
			mismatch: 'badge-error',
			recovering: 'badge-lagging',
			'single-engine': 'badge-muted',
			disconnected: 'badge-error',
		};
		return map[health.status];
	});

	const canReconcile = $derived(
		health.status === 'diverged' || health.status === 'mismatch' || health.status === 'lagging'
	);

	// --- Formatting helpers ---
	function fmtUptime(ms: number): string {
		if (ms <= 0) return '0h 0m';
		const totalMinutes = Math.floor(ms / 60_000);
		const hours = Math.floor(totalMinutes / 60);
		const minutes = totalMinutes % 60;
		return `${hours}h ${minutes}m`;
	}

	function fmtLatency(ms: number | null | undefined): string {
		return ms == null ? '--' : Math.round(ms).toString();
	}

	function fmtTimestamp(ts: number): string {
		const d = new Date(ts);
		return d.toTimeString().slice(0, 8); // HH:MM:SS
	}

	function isOldEntry(ts: number): boolean {
		return Date.now() - ts > OLD_ENTRY_THRESHOLD_MS;
	}

	// --- Source comparison ---
	interface SourceDiff {
		key: string;
		healthA: string;
		healthB: string;
	}

	const sourceDiffs = $derived.by((): SourceDiff[] => {
		const allKeys = new Set([
			...Object.keys(engineHealth.A.sources),
			...Object.keys(engineHealth.B.sources),
		]);
		const diffs: SourceDiff[] = [];
		for (const key of allKeys) {
			const a = engineHealth.A.sources[key] ?? 'missing';
			const b = engineHealth.B.sources[key] ?? 'missing';
			if (a !== b) {
				diffs.push({ key, healthA: a, healthB: b });
			}
		}
		return diffs;
	});

	const allSourcesHealthy = $derived(sourceDiffs.length === 0);

	// Auto-expand sources section when diverged
	$effect(() => {
		if (sourceDiffs.length > 0) {
			sourcesExpanded = true;
		}
	});

	// Auto-expand playout section when out of sync
	$effect(() => {
		if (playoutComparison && !playoutComparison.inSync) {
			playoutExpanded = true;
		}
	});

	// --- Log type styling ---
	function logTypeColor(type: DivergenceLogEntry['type']): string {
		switch (type) {
			case 'diverged':
			case 'mismatch':
				return 'var(--tally-program, #dc2626)';
			case 'lagging':
			case 'transient':
				return 'var(--accent-yellow, #eab308)';
			case 'resolved':
				return 'var(--tally-preview, #16a34a)';
			case 'snapshot':
				return 'var(--accent-blue, #3b82f6)';
			case 'info':
			default:
				return 'var(--text-muted, #71717a)';
		}
	}

	function logTypeIcon(type: DivergenceLogEntry['type']): string {
		switch (type) {
			case 'diverged':
			case 'mismatch':
				return '\u26A0'; // warning
			case 'lagging':
			case 'transient':
				return '\u23F1'; // stopwatch
			case 'resolved':
				return '\u2713'; // check
			case 'snapshot':
				return '\u25CB'; // circle
			case 'info':
			default:
				return '\u2022'; // bullet
		}
	}

	const displayedLog = $derived.by(() => {
		if (showAllLog) return divergenceLog;
		return divergenceLog.slice(0, LOG_DISPLAY_LIMIT);
	});

	const hasMoreLog = $derived(divergenceLog.length > LOG_DISPLAY_LIMIT);
</script>

<div class="redundancy-panel" class:visible role="complementary" aria-label="Redundancy status">
	<div class="panel-header">
		<div class="title-group">
			<span class="panel-title">REDUNDANCY</span>
			<div class="status-badge {statusClass}">{statusLabel}</div>
		</div>
		<button class="close-btn" onclick={onclose} aria-label="Close redundancy panel">&times;</button>
	</div>

	<div class="panel-body">
		<!-- 1. Engines -->
		<section class="section">
			<div class="section-label">ENGINES</div>
			<div class="engine-cards">
				{#each (['A', 'B'] as const) as id}
					{@const eh = id === 'A' ? engineHealth.A : engineHealth.B}
					{@const isAuth = health.authoritative === id}
					<div class="engine-card" class:authoritative={isAuth}>
						<div class="engine-card-header">
							{#if isAuth}
								<span class="auth-star" title="Authoritative">{'\u2605'}</span>
							{/if}
							<span class="engine-name">Engine {id}</span>
							<EngineDot
								status={eh.connected ? 'healthy' : 'offline'}
								size="md"
							/>
						</div>
						<div class="engine-stats">
							<div class="stat-row">
								<span class="stat-label">RTT</span>
								<span class="stat-value mono">{eh.rtt}ms</span>
							</div>
							<div class="stat-row">
								<span class="stat-label">Clock offset</span>
								<span class="stat-value mono">{eh.clockOffset}us</span>
							</div>
							<div class="stat-row">
								<span class="stat-label">Last cmd seq</span>
								<span class="stat-value mono">{eh.lastSeq}</span>
							</div>
							<div class="stat-row">
								<span class="stat-label">Uptime</span>
								<span class="stat-value mono">{fmtUptime(eh.uptime)}</span>
							</div>
						</div>
					</div>
				{/each}
			</div>
		</section>

		<!-- Clock Divergence Warning -->
		{#if health?.warnings?.includes('clock_diverged')}
			<div class="clock-divergence-warning">
				<strong>Clock Divergence</strong>
				<p>Engine clocks are diverged beyond the safety margin. Commands may execute at slightly different times on each engine.</p>
			</div>
		{/if}

		<!-- 2. Program Compare -->
		<section class="section">
			<div class="section-label">PROGRAM COMPARE</div>
			<div class="compare-thumbnails">
				<div class="thumb-container">
					<span class="thumb-label">Engine A</span>
					{#if thumbUrlA}
						<img src={thumbUrlA} alt="Engine A program" class="confidence-thumb" />
					{:else}
						<div class="thumb-placeholder">No signal</div>
					{/if}
				</div>
				<div class="thumb-container">
					<span class="thumb-label">Engine B</span>
					{#if thumbUrlB}
						<img src={thumbUrlB} alt="Engine B program" class="confidence-thumb" />
					{:else}
						<div class="thumb-placeholder">No signal</div>
					{/if}
				</div>
			</div>
			<button class="compare-btn" onclick={() => oncompare?.()}>Full Compare</button>
		</section>

		<!-- 3. Sources (collapsible) -->
		<section class="section">
			<button class="section-toggle" onclick={() => sourcesExpanded = !sourcesExpanded}>
				<span class="toggle-arrow">{sourcesExpanded ? '\u25BE' : '\u25B8'}</span>
				<span class="section-label inline">SOURCES</span>
				{#if allSourcesHealthy}
					<span class="section-ok">All sources healthy on both engines {'\u2713'}</span>
				{:else}
					<span class="section-warn">{sourceDiffs.length} diverged</span>
				{/if}
			</button>
			{#if sourcesExpanded}
				<div class="collapsible-body">
					{#if allSourcesHealthy}
						<div class="ok-msg">All sources reporting identical health across engines.</div>
					{:else}
						<div class="source-diff-list">
							{#each sourceDiffs as diff}
								<div class="source-diff-row">
									<span class="source-key mono">{diff.key}</span>
									<span class="source-health-a" class:unhealthy={diff.healthA !== 'healthy'}>A: {diff.healthA}</span>
									<span class="source-health-b" class:unhealthy={diff.healthB !== 'healthy'}>B: {diff.healthB}</span>
								</div>
							{/each}
						</div>
					{/if}
				</div>
			{/if}
		</section>

		<!-- 4. Audio Sync (collapsible) -->
		<section class="section">
			<button class="section-toggle" onclick={() => audioExpanded = !audioExpanded}>
				<span class="toggle-arrow">{audioExpanded ? '\u25BE' : '\u25B8'}</span>
				<span class="section-label inline">AUDIO SYNC</span>
				{#if audioComparison && audioComparison.some(ch => ch.diff.length > 0)}
					<span class="section-warn">{audioComparison.filter(ch => ch.diff.length > 0).length} out of sync</span>
				{:else}
					<span class="section-ok">Audio in sync {'\u2713'}</span>
				{/if}
			</button>
			{#if audioExpanded}
				<div class="collapsible-body">
					{#if audioComparison}
						<div class="audio-comparison">
							{#each audioComparison as ch}
								<div class="audio-ch-row" class:out-of-tolerance={ch.diff.length > 0}>
									<span class="audio-ch-name">{ch.channel}</span>
									<span class="audio-ch-level mono">A: {ch.levelA.toFixed(1)}dB</span>
									<span class="audio-ch-level mono">B: {ch.levelB.toFixed(1)}dB</span>
									{#if ch.diff.length > 0}
										<span class="audio-ch-diff">{ch.diff.join(', ')} differ</span>
									{/if}
								</div>
							{/each}
						</div>
					{:else}
						<div class="ok-msg">Audio pipeline synchronized across both engines.</div>
					{/if}
				</div>
			{/if}
		</section>

		<!-- 5. Output (always expanded) -->
		<section class="section">
			<div class="section-label">OUTPUT</div>
			{#if outputStatus}
				<div class="output-grid">
					<div class="output-row">
						<span class="output-label">Recording</span>
						<span class="output-status">
							<span class="output-dot a" class:active={outputStatus.recording.A}>A</span>
							<span class="output-dot b" class:active={outputStatus.recording.B}>B</span>
							{#if outputStatus.recording.A === outputStatus.recording.B}
								<span class="mono output-state-ok">{outputStatus.recording.A ? 'REC' : 'IDLE'}</span>
							{:else}
								<span class="mono output-state-warn">MISMATCH</span>
							{/if}
						</span>
					</div>
					{#each outputStatus.destinations as dest}
						<div class="output-row">
							<span class="output-label">{dest.name}</span>
							<span class="output-status">
								<span class="output-dot a" class:active={dest.A.state === 'connected'}>{dest.A.state === 'connected' ? '\u2713' : '\u2717'}</span>
								<span class="output-dot b" class:active={dest.B.state === 'connected'}>{dest.B.state === 'connected' ? '\u2713' : '\u2717'}</span>
								{#if dest.A.bitrate && dest.B.bitrate}
									<span class="mono muted">{(dest.A.bitrate / 1000).toFixed(0)}k / {(dest.B.bitrate / 1000).toFixed(0)}k</span>
								{/if}
							</span>
						</div>
					{/each}
				</div>
			{:else}
				<div class="output-grid">
					<div class="output-row">
						<span class="output-label">Recording</span>
						<span class="output-status">
							<span class="output-dot a">A</span>
							<span class="output-dot b">B</span>
							<span class="mono muted">--</span>
						</span>
					</div>
					<div class="output-row">
						<span class="output-label">Destinations</span>
						<span class="output-status">
							<span class="output-dot a">A</span>
							<span class="output-dot b">B</span>
							<span class="mono muted">--</span>
						</span>
					</div>
				</div>
			{/if}
		</section>

		<!-- 6. Playout Sync (collapsible) -->
		<section class="section">
			<button class="section-toggle" onclick={() => playoutExpanded = !playoutExpanded}>
				<span class="toggle-arrow">{playoutExpanded ? '\u25BE' : '\u25B8'}</span>
				<span class="section-label inline">PLAYOUT SYNC</span>
				{#if playoutComparison && !playoutComparison.inSync}
					<span class="section-warn">out of sync</span>
				{:else}
					<span class="section-ok">Playout in sync {'\u2713'}</span>
				{/if}
			</button>
			{#if playoutExpanded}
				<div class="collapsible-body">
					{#if playoutComparison}
						<div class="playout-comparison">
							<div class="playout-row">
								<span class="playout-label">Item A:</span>
								<span class="playout-value mono">{playoutComparison.itemA}</span>
							</div>
							<div class="playout-row">
								<span class="playout-label">Item B:</span>
								<span class="playout-value mono">{playoutComparison.itemB}</span>
							</div>
							{#if playoutComparison.ptsFrameDelta != null}
								<div class="playout-row">
									<span class="playout-label">PTS delta:</span>
									<span class="playout-value mono" class:out-of-tolerance={!playoutComparison.inSync}>
										{playoutComparison.ptsFrameDelta} frames
									</span>
								</div>
							{/if}
							{#if playoutComparison.cacheA || playoutComparison.cacheB}
								<div class="playout-row">
									<span class="playout-label">Cache:</span>
									<span class="playout-value mono">A: {playoutComparison.cacheA ?? '--'} | B: {playoutComparison.cacheB ?? '--'}</span>
								</div>
							{/if}
						</div>
					{:else}
						<div class="ok-msg">Playout engine synchronized across both engines.</div>
					{/if}
				</div>
			{/if}
		</section>

		<!-- 7. Command Latency (collapsed by default) -->
		<section class="section">
			<button class="section-toggle" onclick={() => commandLatencyExpanded = !commandLatencyExpanded}>
				<span class="toggle-arrow">{commandLatencyExpanded ? '\u25BE' : '\u25B8'}</span>
				<span class="section-label inline">COMMAND LATENCY</span>
			</button>
			{#if commandLatencyExpanded}
				<div class="collapsible-body">
					<div class="latency-summary mono">
						Avg Engine A: {fmtLatency(commandLatency?.A)}ms | Engine B: {fmtLatency(commandLatency?.B)}ms
					</div>
				</div>
			{/if}
		</section>

		<!-- 8. Divergence Log -->
		<section class="section">
			<div class="section-label">DIVERGENCE LOG</div>
			{#if divergenceLog.length === 0}
				<div class="empty-msg">No events recorded</div>
			{:else}
				<div class="log-list">
					{#each displayedLog as entry}
						<div
							class="log-entry"
							style:opacity={isOldEntry(entry.timestamp) ? 0.5 : 1}
						>
							<span class="log-time mono">{fmtTimestamp(entry.timestamp)}</span>
							<span class="log-icon" style:color={logTypeColor(entry.type)}>{logTypeIcon(entry.type)}</span>
							<span class="log-msg">{entry.message}</span>
						</div>
					{/each}
				</div>
				{#if hasMoreLog && !showAllLog}
					<button class="show-all-btn" onclick={() => showAllLog = true}>
						Show all ({divergenceLog.length} entries)
					</button>
				{/if}
			{/if}
		</section>

		<!-- 9. Actions -->
		<section class="section section-actions">
			<div class="section-label">ACTIONS</div>
			{#if pendingReconcile}
				<div class="reconcile-confirm">
					<div class="confirm-message">{pendingReconcileMessage}</div>
					<div class="confirm-buttons">
						<button class="confirm-cancel-btn" onclick={cancelReconcile}>Cancel</button>
						<button class="confirm-btn" onclick={confirmReconcile}>Confirm</button>
					</div>
				</div>
			{:else}
				<div class="action-buttons">
					<button
						class="reconcile-btn"
						disabled={!canReconcile}
						onclick={() => pendingReconcile = 'AtoB'}
						title="Force state from Engine A to Engine B"
					>
						Force A {'\u2192'} B
					</button>
					<button
						class="reconcile-btn"
						disabled={!canReconcile}
						onclick={() => pendingReconcile = 'BtoA'}
						title="Force state from Engine B to Engine A"
					>
						Force B {'\u2192'} A
					</button>
				</div>
				<div class="action-hint">
					{canReconcile ? 'Push authoritative state to the other engine' : 'Available while the engines are out of sync'}
				</div>
			{/if}
		</section>
	</div>
</div>

<style>
	.redundancy-panel {
		position: fixed;
		top: 0;
		right: 0;
		bottom: 0;
		width: 380px;
		height: 100vh;
		background: rgba(9, 9, 11, 0.96);
		border-left: 1px solid var(--border-subtle);
		z-index: var(--z-fullscreen);
		transform: translateX(100%);
		transition: transform 200ms ease;
		display: flex;
		flex-direction: column;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--text-secondary);
		overflow: hidden;
	}

	.redundancy-panel.visible {
		transform: translateX(0);
	}

	/* Header */
	.panel-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 10px 16px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}

	.title-group {
		display: flex;
		align-items: center;
		gap: 10px;
	}

	.panel-title {
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: var(--text-sm);
		color: var(--text-primary);
		letter-spacing: 0.5px;
	}

	.close-btn {
		background: none;
		border: none;
		color: var(--text-muted, var(--text-secondary));
		cursor: pointer;
		font-size: 18px;
		line-height: 1;
		padding: 2px 4px;
		border-radius: var(--radius-sm);
		transition: color var(--transition-fast);
	}

	.close-btn:hover {
		color: var(--text-primary);
	}

	/* Body */
	.panel-body {
		flex: 1;
		overflow-y: auto;
		padding: 12px 16px;
		display: flex;
		flex-direction: column;
		gap: 20px;
	}

	/* Section */
	.section {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.section-label {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 700;
		letter-spacing: 0.1em;
		color: var(--text-muted, var(--text-secondary));
		text-transform: uppercase;
	}

	.section-label.inline {
		display: inline;
		margin: 0;
	}

	/* Status badge */
	.status-badge {
		display: inline-flex;
		align-items: center;
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: 700;
		letter-spacing: 0.08em;
		border: 1px solid transparent;
	}

	.badge-nominal {
		background: rgba(22, 163, 74, 0.15);
		border-color: var(--tally-preview-medium);
		color: var(--tally-preview);
	}

	.badge-lagging {
		background: var(--accent-yellow-dim);
		border-color: rgba(234, 179, 8, 0.3);
		color: var(--accent-yellow);
	}

	.badge-error {
		background: var(--tally-program-dim);
		border-color: var(--tally-program-medium);
		color: var(--tally-program);
	}

	.badge-muted {
		background: rgba(113, 113, 122, 0.15);
		border-color: rgba(113, 113, 122, 0.3);
		color: var(--text-muted, #71717a);
	}

	/* Clock divergence warning */
	.clock-divergence-warning {
		background: rgba(234, 179, 8, 0.1);
		border: 1px solid rgba(234, 179, 8, 0.4);
		border-radius: var(--radius-sm);
		padding: 10px 12px;
		color: var(--accent-yellow, #eab308);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		line-height: 1.4;
	}

	.clock-divergence-warning strong {
		display: block;
		font-size: var(--text-sm);
		margin-bottom: 4px;
		letter-spacing: 0.03em;
	}

	.clock-divergence-warning p {
		margin: 0;
		opacity: 0.85;
	}

	/* Engine cards */
	.engine-cards {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
	}

	.engine-card {
		background: rgba(255, 255, 255, 0.03);
		border: 1px solid rgba(255, 255, 255, 0.06);
		border-radius: var(--radius-sm);
		padding: 10px;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.engine-card.authoritative {
		border-color: rgba(234, 179, 8, 0.25);
		background: rgba(234, 179, 8, 0.04);
	}

	.engine-card-header {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.engine-name {
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: var(--text-xs);
		color: var(--text-primary);
	}

	.auth-star {
		color: var(--accent-yellow, #eab308);
		font-size: 11px;
		line-height: 1;
	}

	.engine-stats {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.stat-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		font-size: var(--text-xs);
	}

	.stat-label {
		color: var(--text-muted, var(--text-secondary));
		font-family: var(--font-ui);
		font-size: 10px;
	}

	.stat-value {
		color: var(--text-secondary);
		font-size: 11px;
	}

	.mono {
		font-family: var(--font-mono);
	}

	.muted {
		color: var(--text-muted, var(--text-secondary));
		opacity: 0.5;
	}

	/* Program Compare */
	.compare-thumbnails {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
	}

	.thumb-container {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.thumb-label {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		color: var(--text-muted, var(--text-secondary));
	}

	.confidence-thumb {
		width: 100%;
		aspect-ratio: 16 / 9;
		object-fit: contain;
		border-radius: var(--radius-sm);
		background: #000;
	}

	.thumb-placeholder {
		width: 100%;
		aspect-ratio: 16 / 9;
		display: flex;
		align-items: center;
		justify-content: center;
		background: #1a1a1a;
		border-radius: var(--radius-sm);
		color: var(--text-muted, var(--text-secondary));
		font-size: var(--text-xs);
	}

	.compare-btn {
		align-self: flex-start;
		padding: 5px 12px;
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-radius: var(--radius-sm);
		background: rgba(255, 255, 255, 0.04);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.compare-btn:hover {
		background: rgba(255, 255, 255, 0.08);
		border-color: rgba(255, 255, 255, 0.2);
		color: var(--text-primary);
	}

	/* Collapsible sections */
	.section-toggle {
		display: flex;
		align-items: center;
		gap: 6px;
		background: none;
		border: none;
		padding: 0;
		cursor: pointer;
		color: var(--text-muted, var(--text-secondary));
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 700;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		text-align: left;
		width: 100%;
	}

	.section-toggle:hover {
		color: var(--text-secondary);
	}

	.toggle-arrow {
		font-size: 10px;
		width: 10px;
		text-align: center;
		flex-shrink: 0;
	}

	.section-ok {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 400;
		color: var(--tally-preview, #16a34a);
		letter-spacing: 0;
		text-transform: none;
		margin-left: auto;
	}

	.section-warn {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		color: var(--accent-yellow, #eab308);
		letter-spacing: 0;
		text-transform: none;
		margin-left: auto;
	}

	.collapsible-body {
		padding: 6px 0 0 16px;
	}

	.ok-msg {
		font-size: var(--text-xs);
		color: var(--text-muted, var(--text-secondary));
		opacity: 0.6;
		font-style: italic;
	}

	/* Source diffs */
	.source-diff-list {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.source-diff-row {
		display: flex;
		gap: 8px;
		align-items: center;
		font-size: var(--text-xs);
		padding: 3px 6px;
		background: rgba(220, 38, 38, 0.06);
		border-radius: var(--radius-sm);
	}

	.source-key {
		font-weight: 600;
		color: var(--text-primary);
		min-width: 60px;
	}

	.source-health-a,
	.source-health-b {
		font-size: 10px;
		color: var(--text-secondary);
	}

	.source-health-a.unhealthy,
	.source-health-b.unhealthy {
		color: var(--tally-program, #dc2626);
		font-weight: 600;
	}

	/* Output grid */
	.output-grid {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.output-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		font-size: var(--text-xs);
	}

	.output-label {
		color: var(--text-secondary);
		font-family: var(--font-ui);
	}

	.output-status {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.output-dot {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 16px;
		height: 16px;
		border-radius: var(--radius-sm);
		font-size: 9px;
		font-weight: 700;
		font-family: var(--font-mono);
		background: rgba(255, 255, 255, 0.06);
		color: var(--text-muted, var(--text-secondary));
	}

	/* Latency placeholder */
	.latency-summary {
		font-size: var(--text-xs);
		color: var(--text-muted, var(--text-secondary));
		opacity: 0.6;
	}

	/* Divergence log */
	.log-list {
		display: flex;
		flex-direction: column;
		gap: 3px;
		max-height: 300px;
		overflow-y: auto;
	}

	.log-entry {
		display: flex;
		align-items: flex-start;
		gap: 6px;
		font-size: var(--text-xs);
		padding: 3px 0;
		border-bottom: 1px solid rgba(255, 255, 255, 0.03);
	}

	.log-time {
		color: var(--text-muted, var(--text-secondary));
		font-size: 10px;
		white-space: nowrap;
		flex-shrink: 0;
	}

	.log-icon {
		font-size: 11px;
		flex-shrink: 0;
		width: 14px;
		text-align: center;
	}

	.log-msg {
		color: var(--text-secondary);
		word-break: break-word;
	}

	.empty-msg {
		font-size: var(--text-xs);
		color: var(--text-muted, var(--text-secondary));
		opacity: 0.6;
		font-style: italic;
	}

	.show-all-btn {
		align-self: flex-start;
		background: none;
		border: none;
		padding: 4px 0;
		color: var(--accent-blue, #3b82f6);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		opacity: 0.8;
	}

	.show-all-btn:hover {
		opacity: 1;
		text-decoration: underline;
	}

	/* Actions */
	.section-actions {
		margin-top: auto;
		padding-top: 8px;
		border-top: 1px solid var(--border-subtle);
	}

	.action-buttons {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
	}

	.reconcile-btn {
		padding: 7px 10px;
		border: 1px solid rgba(234, 179, 8, 0.4);
		border-radius: var(--radius-sm);
		background: rgba(234, 179, 8, 0.1);
		color: var(--accent-yellow, #eab308);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 700;
		font-size: var(--text-xs);
		letter-spacing: 0.04em;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast),
			opacity var(--transition-fast);
	}

	.reconcile-btn:hover:not(:disabled) {
		background: rgba(234, 179, 8, 0.2);
		border-color: rgba(234, 179, 8, 0.6);
	}

	.reconcile-btn:disabled {
		opacity: 0.35;
		cursor: not-allowed;
	}

	.action-hint {
		font-size: 10px;
		color: var(--text-muted, var(--text-secondary));
		opacity: 0.55;
		text-align: center;
		font-family: var(--font-ui);
	}

	/* Reconcile confirmation */
	.reconcile-confirm {
		display: flex;
		flex-direction: column;
		gap: 8px;
		padding: 10px;
		background: rgba(234, 179, 8, 0.08);
		border: 1px solid rgba(234, 179, 8, 0.3);
		border-radius: var(--radius-sm);
	}

	.confirm-message {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--accent-yellow, #eab308);
		line-height: 1.4;
	}

	.confirm-buttons {
		display: flex;
		gap: 8px;
		justify-content: flex-end;
	}

	.confirm-cancel-btn {
		padding: 5px 12px;
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-radius: var(--radius-sm);
		background: rgba(255, 255, 255, 0.04);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		transition: background var(--transition-fast);
	}

	.confirm-cancel-btn:hover {
		background: rgba(255, 255, 255, 0.08);
	}

	.confirm-btn {
		padding: 5px 12px;
		border: 1px solid rgba(234, 179, 8, 0.5);
		border-radius: var(--radius-sm);
		background: rgba(234, 179, 8, 0.2);
		color: var(--accent-yellow, #eab308);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		transition: background var(--transition-fast);
	}

	.confirm-btn:hover {
		background: rgba(234, 179, 8, 0.35);
	}

	/* Audio comparison */
	.audio-comparison {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.audio-ch-row {
		display: flex;
		gap: 8px;
		align-items: center;
		font-size: var(--text-xs);
		padding: 3px 6px;
		border-radius: var(--radius-sm);
	}

	.audio-ch-row.out-of-tolerance {
		background: rgba(220, 38, 38, 0.06);
	}

	.audio-ch-name {
		font-family: var(--font-ui);
		font-weight: 600;
		color: var(--text-primary);
		min-width: 60px;
	}

	.audio-ch-level {
		font-size: 10px;
		color: var(--text-secondary);
	}

	.audio-ch-diff {
		font-size: 10px;
		color: var(--tally-program, #dc2626);
	}

	/* Output states */
	.output-dot.active {
		background: var(--tally-preview, #16a34a);
		color: white;
	}

	.output-state-ok {
		color: var(--text-secondary);
		font-size: 10px;
	}

	.output-state-warn {
		color: var(--accent-yellow, #eab308);
		font-size: 10px;
		font-weight: 600;
	}

	/* Playout comparison */
	.playout-comparison {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.playout-row {
		display: flex;
		gap: 8px;
		align-items: center;
		font-size: var(--text-xs);
	}

	.playout-label {
		color: var(--text-muted, var(--text-secondary));
		font-family: var(--font-ui);
		font-size: 10px;
		min-width: 60px;
	}

	.playout-value {
		color: var(--text-secondary);
		font-size: 11px;
	}

	.playout-value.out-of-tolerance {
		color: var(--accent-yellow, #eab308);
		font-weight: 600;
	}
</style>
