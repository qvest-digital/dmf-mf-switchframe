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
		onSyncClick?: () => void;
		onBugToggle?: () => void;
		onModeMenu?: () => void;
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
		onSyncClick,
		onBugToggle,
		onModeMenu,
	}: Props = $props();

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

		<button class="mode-btn" onclick={onModeMenu} title="Switch mode">
			&#8942;
		</button>
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
	}

	.scte-idle .scte-value {
		color: var(--text-secondary);
	}

	/* ── Mode button ── */
	.mode-btn {
		margin-left: auto;
		background: none;
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-size: 16px;
		cursor: pointer;
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		line-height: 1;
		transition:
			color var(--transition-fast),
			border-color var(--transition-fast);
	}

	.mode-btn:hover {
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	/* ── Spacer ── */
	.spacer {
		flex: 0 0 auto;
	}
</style>
