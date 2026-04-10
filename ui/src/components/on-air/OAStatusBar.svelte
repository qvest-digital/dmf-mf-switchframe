<script lang="ts">
	interface Props {
		programSource: string;
		bugActive: boolean;
		scteState: 'content' | 'in_break' | 'idle';
		scteCountdown?: number;
		segmentElapsed?: number;
		segmentDuration?: number;
		showUptime?: boolean;
		uptimeSeconds?: number;
		isDualEngine: boolean;
		engineAHealthy?: boolean;
		engineBHealthy?: boolean;
		syncStatus?: string;
		isRecording?: boolean;
		recordingDuration?: number;
		programPeakL?: number;
		programPeakR?: number;
		onSyncClick?: () => void;
		onBugToggle?: () => void;
		onModeMenu?: () => void;
		onRecordToggle?: () => void;
	}

	let {
		programSource,
		bugActive,
		scteState,
		scteCountdown,
		segmentElapsed,
		segmentDuration,
		showUptime = false,
		uptimeSeconds,
		isDualEngine,
		engineAHealthy = true,
		engineBHealthy = true,
		syncStatus = 'disconnected',
		isRecording = false,
		recordingDuration = 0,
		programPeakL = -60,
		programPeakR = -60,
		onSyncClick,
		onBugToggle,
		onModeMenu,
		onRecordToggle,
	}: Props = $props();

	// ── Audio level helpers ──

	function peakToPercent(dbfs: number): number {
		// Map -60..0 dBFS to 0..100%
		return Math.max(0, Math.min(100, ((dbfs + 60) / 60) * 100));
	}

	function peakColor(dbfs: number): string {
		if (dbfs > -3) return 'var(--tally-program)';
		if (dbfs > -12) return 'var(--accent-yellow)';
		return 'var(--accent-green)';
	}

	// ── Wall clock ──
	let clockText = $state('');

	function formatClock(): string {
		const now = new Date();
		const h = String(now.getHours()).padStart(2, '0');
		const m = String(now.getMinutes()).padStart(2, '0');
		const s = String(now.getSeconds()).padStart(2, '0');
		const tz = Intl.DateTimeFormat(undefined, { timeZoneName: 'short' })
			.formatToParts(now)
			.find((p) => p.type === 'timeZoneName')?.value ?? '';
		return `${h}:${m}:${s} ${tz}`;
	}

	$effect(() => {
		clockText = formatClock();
		const id = setInterval(() => { clockText = formatClock(); }, 1000);
		return () => clearInterval(id);
	});

	// ── Formatting helpers ──
	function formatMMSS(totalSeconds: number): string {
		const mins = Math.floor(totalSeconds / 60);
		const secs = Math.floor(totalSeconds % 60);
		return `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
	}

	function formatHHMMSS(totalSeconds: number): string {
		const hrs = Math.floor(totalSeconds / 3600);
		const mins = Math.floor((totalSeconds % 3600) / 60);
		const secs = Math.floor(totalSeconds % 60);
		return `${String(hrs).padStart(2, '0')}:${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
	}

	// ── Derived state ──
	const scteLabel = $derived.by((): string => {
		if (scteState === 'content') return 'CONTENT';
		if (scteState === 'idle') return 'IDLE';
		if (scteState === 'in_break') {
			const countdown = scteCountdown != null ? ` ${formatMMSS(scteCountdown)}` : '';
			return `IN BREAK${countdown}`;
		}
		return '';
	});

	const syncLabel = $derived.by((): string => {
		const map: Record<string, string> = {
			synced: 'SYNC',
			lagging: 'LAG',
			diverged: 'DIVERGED',
			mismatch: 'MISMATCH',
			recovering: 'RECOVERING',
			disconnected: 'OFFLINE',
		};
		return map[syncStatus] ?? syncStatus.toUpperCase();
	});

	const syncClass = $derived.by((): string => {
		const map: Record<string, string> = {
			synced: 'sync-green',
			lagging: 'sync-amber',
			diverged: 'sync-red',
			mismatch: 'sync-red',
			recovering: 'sync-amber',
			disconnected: 'sync-red',
		};
		return map[syncStatus] ?? 'sync-red';
	});
</script>

<header class="oa-status-bar" role="banner">
	<!-- Row 1 -->
	<div class="row row-1">
		{#if isDualEngine}
			<div class="engine-group">
				<span class="engine-dot" class:dot-healthy={engineAHealthy} class:dot-offline={!engineAHealthy} title="Engine A"></span>
				<span class="label">A</span>
				<span class="engine-dot" class:dot-healthy={engineBHealthy} class:dot-offline={!engineBHealthy} title="Engine B"></span>
				<span class="label">B</span>
				<button
					class="sync-badge {syncClass}"
					onclick={onSyncClick}
					title="Engine sync status"
				>
					<span class="sync-dot"></span>
					{syncLabel}
				</button>
			</div>
		{:else}
			<div class="engine-single">
				<span class="engine-dot dot-healthy" title="Engine A"></span>
				<span class="label">ENGINE A</span>
			</div>
		{/if}

		<span class="clock">{clockText}</span>

		<span class="on-air-label">
			<span class="label">ON AIR:</span>
			<span class="program-name">{programSource}</span>
		</span>

		<button
			class="bug-toggle"
			onclick={onBugToggle}
			title="Toggle bug overlay"
		>
			<span class="label">BUG:</span>
			{#if bugActive}
				<span class="bug-on">ON</span>
			{:else}
				<span class="bug-off">OFF</span>
			{/if}
		</button>

		<button
			class="rec-indicator"
			class:rec-active={isRecording}
			onclick={onRecordToggle}
			title={isRecording ? 'Stop recording' : 'Start recording'}
		>
			<span class="rec-dot"></span>
			<span class="rec-label">REC</span>
			{#if isRecording}
				<span class="rec-duration">{formatMMSS(recordingDuration)}</span>
			{/if}
		</button>

		<span class="audio-meters" title="Program audio L/R">
			<span class="meter-bar" style:height="{peakToPercent(programPeakL)}%" style:background={peakColor(programPeakL)}></span>
			<span class="meter-bar" style:height="{peakToPercent(programPeakR)}%" style:background={peakColor(programPeakR)}></span>
		</span>

		<button class="mode-btn" onclick={onModeMenu} title="Switch layout mode">MODE</button>
	</div>

	<!-- Row 2 -->
	<div class="row row-2">
		<span class="spacer"></span>

		{#if showUptime && uptimeSeconds != null}
			<span class="timing">
				<span class="label">UPTIME:</span>
				<span class="timing-value">{formatHHMMSS(uptimeSeconds)}</span>
			</span>
		{:else if segmentElapsed != null && segmentDuration != null}
			<span class="timing">
				<span class="label">SEGMENT:</span>
				<span class="timing-value">{formatMMSS(segmentElapsed)} / {formatMMSS(segmentDuration)}</span>
			</span>
		{/if}

		<span class="scte-state scte-{scteState}">
			<span class="label">SCTE:</span>
			<span class="scte-value">{scteLabel}</span>
		</span>
	</div>
</header>

<style>
	.oa-status-bar {
		display: flex;
		flex-direction: column;
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-default);
		user-select: none;
	}

	.row {
		display: flex;
		align-items: center;
		gap: 16px;
		padding: 4px 16px;
		min-height: 28px;
	}

	.row-2 {
		padding-top: 0;
		padding-bottom: 6px;
	}

	/* ── Labels ── */
	.label {
		font-family: var(--font-ui);
		font-size: 13px;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--text-secondary);
	}

	/* ── Engine indicators ── */
	.engine-group,
	.engine-single {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.engine-dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.dot-healthy {
		background: var(--accent-green);
	}

	.dot-offline {
		background: var(--color-error);
	}

	/* ── Sync badge ── */
	.sync-badge {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 1px 6px;
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: 11px;
		font-weight: 600;
		letter-spacing: 0.06em;
		cursor: pointer;
		border: 1px solid transparent;
		background: none;
		line-height: 1;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast),
			color var(--transition-fast);
	}

	.sync-badge:hover {
		filter: brightness(1.2);
	}

	.sync-dot {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.sync-green {
		background: rgba(22, 163, 74, 0.15);
		border-color: var(--tally-preview-medium);
		color: var(--tally-preview);
	}

	.sync-green .sync-dot {
		background: var(--tally-preview);
	}

	.sync-amber {
		background: var(--accent-yellow-dim);
		border-color: rgba(234, 179, 8, 0.3);
		color: var(--accent-yellow);
	}

	.sync-amber .sync-dot {
		background: var(--accent-yellow);
	}

	.sync-red {
		background: var(--tally-program-dim);
		border-color: var(--tally-program-medium);
		color: var(--tally-program);
	}

	.sync-red .sync-dot {
		background: var(--tally-program);
	}

	/* ── Clock ── */
	.clock {
		font-family: var(--font-mono);
		font-size: 18px;
		font-weight: 600;
		color: var(--text-primary);
		letter-spacing: 0.02em;
	}

	/* ── ON AIR ── */
	.on-air-label {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.program-name {
		font-family: var(--font-mono);
		font-size: 14px;
		font-weight: 600;
		color: var(--text-primary);
	}

	/* ── Bug toggle ── */
	.bug-toggle {
		display: flex;
		align-items: center;
		gap: 4px;
		background: none;
		border: none;
		cursor: pointer;
		padding: 2px 4px;
		border-radius: var(--radius-sm);
		transition: background var(--transition-fast);
	}

	.bug-toggle:hover {
		background: var(--bg-hover);
	}

	.bug-on {
		font-family: var(--font-mono);
		font-size: 13px;
		font-weight: 700;
		color: var(--accent-green);
	}

	.bug-off {
		font-family: var(--font-mono);
		font-size: 13px;
		font-weight: 700;
		color: var(--text-secondary);
	}

	/* ── Timing ── */
	.timing {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.timing-value {
		font-family: var(--font-mono);
		font-size: 16px;
		font-weight: 700;
		color: var(--text-primary);
	}

	/* ── SCTE state ── */
	.scte-state {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.scte-value {
		font-family: var(--font-mono);
		font-size: 13px;
		font-weight: 700;
	}

	.scte-content .scte-value {
		color: var(--accent-green);
	}

	.scte-in_break .scte-value {
		color: var(--tally-program);
		background: var(--tally-program-dim);
		padding: 1px 6px;
		border-radius: var(--radius-sm);
		border: 1px solid var(--tally-program-medium);
	}

	.scte-idle .scte-value {
		color: var(--text-secondary);
	}

	/* ── Mode button ── */
	.mode-btn {
		margin-left: auto;
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
		text-transform: uppercase;
		transition: border-color var(--transition-fast), background var(--transition-fast), color var(--transition-fast);
	}

	.mode-btn:hover {
		border-color: var(--border-strong);
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	/* ── Recording indicator ── */
	.rec-indicator {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		background: var(--bg-elevated, #1c1c21);
		border: 1px solid var(--border-default);
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.rec-indicator:hover {
		background: var(--bg-hover);
		border-color: var(--border-strong);
	}

	.rec-indicator.rec-active {
		background: var(--tally-program-dim);
		border-color: var(--tally-program-medium);
	}

	.rec-indicator.rec-active:hover {
		background: rgba(220, 38, 38, 0.15);
	}

	.rec-dot {
		display: inline-block;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--text-secondary);
		transition: background var(--transition-fast);
	}

	.rec-active .rec-dot {
		background: var(--tally-program);
		box-shadow: 0 0 6px rgba(220, 38, 38, 0.5);
		animation: rec-pulse 1.2s ease-in-out infinite;
	}

	@keyframes rec-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.25; }
	}

	.rec-label {
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
	}

	.rec-active .rec-label {
		color: var(--tally-program);
	}

	.rec-duration {
		font-family: var(--font-mono);
		font-size: 11px;
		font-weight: 500;
		color: var(--text-primary);
	}

	/* ── Audio peak meters ── */
	.audio-meters {
		display: inline-flex;
		align-items: flex-end;
		gap: 2px;
		height: 20px;
		padding: 0 4px;
	}

	.meter-bar {
		width: 4px;
		min-height: 1px;
		border-radius: 1px;
		transition: height 80ms linear, background 80ms linear;
	}

	/* ── Spacer ── */
	.spacer {
		flex: 0 0 auto;
	}
</style>
