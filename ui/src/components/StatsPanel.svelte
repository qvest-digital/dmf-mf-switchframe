<script lang="ts">
	import { resolveApiUrl } from '$lib/api/base-url';

	// --- Types ---
	interface PipelineNodeSnapshot {
		name: string;
		last_ns: number;
		max_ns: number;
		latency_us: number;
		last_error?: string;
	}

	interface PipelineSnapshot {
		epoch: number;
		run_count: number;
		last_run_ns: number;
		max_run_ns: number;
		total_latency_us: number;
		lip_sync_hint_us: number;
		active_nodes: PipelineNodeSnapshot[];
	}

	interface DebugSnapshot {
		uptime_ms?: number;
		switcher?: {
			pipeline?: PipelineSnapshot;
			video_pipeline?: {
				output_fps: number;
				frames_processed: number;
				frames_dropped: number;
				queue_len: number;
			};
			frame_budget_ms?: number;
			frame_pool?: { hits: number; misses: number; capacity: number };
			source_decoders?: { active_count: number; estimated_yuv_mb: number };
			transition_engine?: {
				ingest_last_ms: number; ingest_max_ms: number;
				blend_last_ms: number; blend_max_ms: number;
				frames_ingested: number; frames_blended: number;
			};
			cuts_total?: number;
			transitions_completed?: number;
			deadline_violations?: number;
			frame_rate_converter?: { quality: string };
		};
		mixer?: {
			mode: string;
			frames_mixed: number;
			frames_passthrough: number;
			max_inter_frame_gap_ms?: number;
		};
	}

	// --- Constants ---
	const HISTORY_SIZE = 60;
	const DEFAULT_FRAME_BUDGET_NS = 33_333_333; // 30fps
	const POLL_INTERVAL_MS = 2000;
	const MIN_SEGMENT_PCT = 0.5;
	const MIN_LABEL_PCT = 8;
	const MIN_HEADROOM_LABEL_PCT = 15;
	const SYNC_RANGE_MS = 30;
	const SYNC_OK_THRESHOLD_MS = 5;
	const SYNC_WARN_THRESHOLD_MS = 15;

	const NODE_META: Record<string, { display: string; short: string; color: string }> = {
		'upstream-key':    { display: 'Upstream Key',  short: 'KEY', color: 'rgba(167, 139, 250, 0.7)' },
		'compositor':      { display: 'Compositor',    short: 'DSK', color: 'rgba(59, 130, 246, 0.7)' },
		'raw-sink-mxl':    { display: 'Raw Sink MXL',  short: 'MXL', color: 'rgba(234, 179, 8, 0.7)' },
		'raw-sink-monitor':{ display: 'Raw Monitor',   short: 'MON', color: 'rgba(245, 158, 11, 0.7)' },
		'h264-encode':     { display: 'H.264 Encode',  short: 'ENC', color: 'rgba(52, 211, 153, 0.7)' },
	};

	const ALL_NODES = Object.keys(NODE_META);

	// --- Props ---
	interface Props {
		visible: boolean;
		onclose: () => void;
	}

	let { visible, onclose }: Props = $props();

	// --- State ---
	let snapshot = $state<DebugSnapshot | null>(null);
	let intervalId: ReturnType<typeof setInterval> | undefined;
	let abortController: AbortController | undefined;
	let nodeHistory = $state<Map<string, number[]>>(new Map());
	let lastUpdateTime = $state(0);

	// --- Polling ---
	async function poll() {
		try {
			abortController?.abort();
			abortController = new AbortController();
			const resp = await fetch(resolveApiUrl('/api/debug/snapshot'), {
				signal: abortController.signal,
			});
			if (resp.ok) {
				snapshot = await resp.json();
				lastUpdateTime = Date.now();
				recordHistory();
			}
		} catch { /* ignore network + abort errors */ }
	}

	function recordHistory() {
		if (!snapshot?.switcher?.pipeline?.active_nodes) return;
		const nodes = snapshot.switcher.pipeline.active_nodes;
		const updated = new Map(nodeHistory);
		for (const node of nodes) {
			const arr = updated.get(node.name) ?? [];
			arr.push(node.last_ns);
			if (arr.length > HISTORY_SIZE) arr.shift();
			updated.set(node.name, arr);
		}
		nodeHistory = updated;
	}

	$effect(() => {
		if (visible) {
			nodeHistory = new Map(); // reset stale history on open
			poll();
			intervalId = setInterval(poll, POLL_INTERVAL_MS);
		} else {
			if (intervalId) { clearInterval(intervalId); intervalId = undefined; }
		}
		return () => {
			abortController?.abort();
			if (intervalId) { clearInterval(intervalId); intervalId = undefined; }
		};
	});

	// --- Helpers ---
	function fmtMs(ns: number | undefined | null): string {
		if (ns === undefined || ns === null) return '-';
		return (ns / 1e6).toFixed(2);
	}

	function fmtCount(n: number | undefined | null): string {
		if (n === undefined || n === null) return '0';
		return n.toLocaleString();
	}

	function nodeDisplayName(name: string): string {
		return NODE_META[name]?.display ?? name;
	}

	function nodeShortName(name: string): string {
		return NODE_META[name]?.short ?? name;
	}

	function nodeColor(name: string): string {
		return NODE_META[name]?.color ?? 'rgba(59, 130, 246, 0.7)';
	}

	function nodeStatus(lastNs: number, frameBudgetNs: number): 'ok' | 'warn' | 'crit' {
		const ratio = lastNs / frameBudgetNs;
		if (ratio > 0.8) return 'crit';
		if (ratio > 0.5) return 'warn';
		return 'ok';
	}

	// --- Pipeline graph data ---
	const pipelineData = $derived.by(() => {
		const pipe = snapshot?.switcher?.pipeline;
		if (!pipe) return null;
		const activeNodes = pipe.active_nodes ?? [];
		const activeMap = new Map(activeNodes.map(n => [n.name, n]));
		const frameBudgetNs = snapshot?.switcher?.frame_budget_ms
			? snapshot.switcher.frame_budget_ms * 1e6
			: DEFAULT_FRAME_BUDGET_NS;

		return {
			epoch: pipe.epoch,
			runCount: pipe.run_count,
			lastRunNs: pipe.last_run_ns,
			maxRunNs: pipe.max_run_ns,
			totalLatencyUs: pipe.total_latency_us,
			lipSyncHintUs: pipe.lip_sync_hint_us,
			frameBudgetNs,
			activeNodes,
			activeMap,
			inactiveNodes: ALL_NODES.filter(n => !activeMap.has(n)),
		};
	});

	// --- Alarm state ---
	const hasAlarm = $derived.by(() => {
		if (!pipelineData) return false;
		const hasCritNode = pipelineData.activeNodes.some(
			n => nodeStatus(n.last_ns, pipelineData.frameBudgetNs) === 'crit'
		);
		const hasViolations = (snapshot?.switcher?.deadline_violations ?? 0) > 0;
		return hasCritNode || hasViolations;
	});

	// --- Sparkline path ---
	function sparklinePath(values: number[], width: number, height: number): string {
		if (values.length < 2) return '';
		const max = Math.max(...values, 1);
		const step = width / (HISTORY_SIZE - 1);
		const offset = (HISTORY_SIZE - values.length) * step;
		return values
			.map((v, i) => {
				const x = offset + i * step;
				const y = height - (v / max) * (height - 2) - 1;
				return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`;
			})
			.join(' ');
	}

	function sparklineFillPath(values: number[], width: number, height: number): string {
		const line = sparklinePath(values, width, height);
		if (!line) return '';
		const step = width / (HISTORY_SIZE - 1);
		const startX = (HISTORY_SIZE - values.length) * step;
		return `${line} L${width.toFixed(1)},${height} L${startX.toFixed(1)},${height} Z`;
	}
</script>

<div class="stats-panel" class:visible role="complementary" aria-label="Pipeline stats">
	<div class="stats-header" class:alarm={hasAlarm}>
		<div class="title-group">
			<span class="panel-title" class:alarm={hasAlarm}>PIPELINE MONITOR</span>
			{#if lastUpdateTime > 0}
				<span class="update-dot" class:stale={Date.now() - lastUpdateTime > 4000} aria-hidden="true"></span>
			{/if}
			<span class="shortcut-hint">Shift+P</span>
		</div>
		<button class="close-btn" onclick={onclose} aria-label="Close stats panel">&times;</button>
	</div>

	{#if snapshot}
		<div class="panel-body">
			<!-- Pipeline Graph -->
			<div class="section">
				<div class="section-label">
					PIPELINE
					{#if pipelineData}
						<span class="epoch-badge">epoch {pipelineData.epoch}</span>
					{/if}
				</div>
				{#if pipelineData}
					{@const mainChain = ['upstream-key', 'compositor', 'raw-sink-mxl', 'h264-encode']}
					<div class="node-graph">
						{#each mainChain as name, i}
							{@const node = pipelineData.activeMap.get(name)}
							{#if i > 0}
								<div class="node-arrow" aria-hidden="true">&rarr;</div>
							{/if}
							<div
								class="node-box"
								class:inactive={!node}
								class:has-error={node?.last_error}
								title={node ? `${nodeDisplayName(name)}: ${fmtMs(node.last_ns)}ms (max ${fmtMs(node.max_ns)}ms)` : `${nodeDisplayName(name)}: inactive`}
							>
								<div class="node-name">{nodeShortName(name)}</div>
								{#if node}
									<div class="node-time">{fmtMs(node.last_ns)}ms</div>
									<div class="node-dot {nodeStatus(node.last_ns, pipelineData.frameBudgetNs)}" aria-label="{nodeStatus(node.last_ns, pipelineData.frameBudgetNs)} status"></div>
								{:else}
									<div class="node-time inactive-label">off</div>
								{/if}
							</div>
							{#if name === 'raw-sink-mxl'}
								{@const monNode = pipelineData.activeMap.get('raw-sink-monitor')}
								<div class="branch-container">
									<div class="branch-line" aria-hidden="true"></div>
									<div class="node-box branch-node" class:inactive={!monNode}>
										<div class="node-name">{nodeShortName('raw-sink-monitor')}</div>
										{#if monNode}
											<div class="node-time">{fmtMs(monNode.last_ns)}ms</div>
											<div class="node-dot {nodeStatus(monNode.last_ns, pipelineData.frameBudgetNs)}"></div>
										{:else}
											<div class="node-time inactive-label">off</div>
										{/if}
									</div>
								</div>
							{/if}
						{/each}
					</div>
				{/if}
			</div>

			<!-- Frame Budget Bar -->
			<div class="section">
				<div class="section-label">FRAME BUDGET</div>
				{#if pipelineData}
					{@const budgetMs = pipelineData.frameBudgetNs / 1e6}
					{@const totalUsedNs = pipelineData.lastRunNs}
					{@const totalUsedMs = totalUsedNs / 1e6}
					{@const usedPct = Math.min((totalUsedNs / pipelineData.frameBudgetNs) * 100, 100)}
					{@const headroomPct = Math.max(100 - usedPct, 0)}
					{@const headroomClass = headroomPct < 10 ? 'crit' : headroomPct < 30 ? 'warn' : 'ok'}
					<div class="budget-bar">
						{#each pipelineData.activeNodes as node}
							{@const pct = (node.last_ns / pipelineData.frameBudgetNs) * 100}
							{#if pct > MIN_SEGMENT_PCT}
								<div
									class="budget-segment"
									style="width: {pct}%; background: {nodeColor(node.name).replace('0.7', '0.25')}"
									title="{nodeDisplayName(node.name)}: {fmtMs(node.last_ns)}ms"
								>
									{#if pct > MIN_LABEL_PCT}
										<span class="seg-label">{nodeShortName(node.name)}</span>
									{/if}
								</div>
							{/if}
						{/each}
						<div class="budget-headroom {headroomClass}" title="Headroom: {(budgetMs - totalUsedMs).toFixed(1)}ms">
							{#if headroomPct > MIN_HEADROOM_LABEL_PCT}
								<span class="seg-label">{(budgetMs - totalUsedMs).toFixed(1)}ms</span>
							{/if}
						</div>
					</div>
					<div class="budget-summary">
						{totalUsedMs.toFixed(1)}ms / {budgetMs.toFixed(1)}ms ({usedPct.toFixed(0)}%)
					</div>
				{/if}

				{#if snapshot?.switcher?.video_pipeline}
					{@const pipe = snapshot.switcher.video_pipeline}
					<div class="stat-chips">
						<span class="chip" class:crit-chip={(pipe.output_fps ?? 0) < 25 && (pipe.output_fps ?? 0) > 0}>
							{pipe.output_fps ?? '-'} fps
						</span>
						<span class="chip">
							{fmtCount(pipe.frames_processed)} frames
						</span>
						<span class="chip" class:crit-chip={(pipe.frames_dropped ?? 0) > 0}>
							{pipe.frames_dropped ?? 0} dropped
						</span>
						<span class="chip" class:warn-chip={(pipe.queue_len ?? 0) >= 4} class:crit-chip={(pipe.queue_len ?? 0) >= 6}>
							{pipe.queue_len ?? 0}/8 queue
						</span>
						{#if (snapshot?.switcher?.deadline_violations ?? 0) > 0}
							<span class="chip crit-chip">
								{snapshot.switcher.deadline_violations} violations
							</span>
						{/if}
					</div>
				{/if}
			</div>

			<!-- Node Timing Sparklines -->
			<div class="section">
				<div class="section-label">NODE TIMING</div>
				{#if pipelineData}
					<div class="sparklines">
						{#each pipelineData.activeNodes as node}
							{@const history = nodeHistory.get(node.name) ?? []}
							{@const color = nodeColor(node.name)}
							<div class="sparkline-row">
								<span class="spark-name">{nodeShortName(node.name)}</span>
								<svg class="sparkline-svg" viewBox="0 0 200 28" preserveAspectRatio="none" aria-hidden="true">
									<!-- Threshold line at 50% budget -->
									<line x1="0" y1="14" x2="200" y2="14"
										stroke="var(--border-subtle)" stroke-width="0.5" stroke-dasharray="2,2" />
									<!-- Area fill -->
									{#if history.length >= 2}
										<path d={sparklineFillPath(history, 200, 28)} fill={color.replace('0.7', '0.08')} stroke="none" />
									{/if}
									<!-- Line -->
									<path d={sparklinePath(history, 200, 28)} fill="none"
										stroke={color} stroke-width="1.5" />
								</svg>
								<span class="spark-value">{fmtMs(node.last_ns)}ms</span>
								<span class="spark-max">pk {fmtMs(node.max_ns)}</span>
							</div>
						{/each}
					</div>
				{/if}
			</div>

			<!-- Lip-Sync Gauge -->
			{#if pipelineData}
				{@const hintMs = pipelineData.lipSyncHintUs / 1000}
				{@const absHint = Math.abs(hintMs)}
				{@const syncClass = absHint < SYNC_OK_THRESHOLD_MS ? 'sync-ok' : absHint < SYNC_WARN_THRESHOLD_MS ? 'sync-warn' : 'sync-crit'}
				{@const markerPct = Math.max(0, Math.min(100, ((hintMs + SYNC_RANGE_MS) / (SYNC_RANGE_MS * 2)) * 100))}
				<div class="section">
					<div class="section-label">LIP SYNC</div>
					<div class="sync-gauge">
						<div class="sync-labels" aria-hidden="true">
							<span>audio leads</span>
							<span>video leads</span>
						</div>
						<div class="sync-track" role="meter" aria-label="Lip sync offset" aria-valuenow={hintMs} aria-valuemin={-SYNC_RANGE_MS} aria-valuemax={SYNC_RANGE_MS}>
							<div class="sync-safe-zone" aria-hidden="true"></div>
							<div class="sync-center" aria-hidden="true"></div>
							<div class="sync-marker {syncClass}" style="left: {markerPct}%">
								<span class="sync-value">{hintMs.toFixed(1)}ms</span>
							</div>
						</div>
						<div class="sync-scale" aria-hidden="true">
							<span>-{SYNC_RANGE_MS}</span>
							<span>0</span>
							<span>+{SYNC_RANGE_MS}</span>
						</div>
					</div>
				</div>
			{/if}

			<!-- System Stats -->
			<div class="section">
				<div class="section-label">SYSTEM</div>
				<div class="stats-grid">
					{#if snapshot?.switcher?.frame_pool}
						{@const pool = snapshot.switcher.frame_pool}
						{@const total = (pool.hits ?? 0) + (pool.misses ?? 0)}
						{@const hitRate = total > 0 ? ((pool.hits / total) * 100).toFixed(1) : '-'}
						<div class="stat-row">
							<span class="stat-key">Frame Pool</span>
							<span class="stat-val">{hitRate}% hit</span>
							<span class="stat-val">{pool.capacity ?? '-'} cap</span>
						</div>
					{/if}
					{#if snapshot?.switcher?.source_decoders}
						{@const dec = snapshot.switcher.source_decoders}
						<div class="stat-row">
							<span class="stat-key">Decoders</span>
							<span class="stat-val">{dec.active_count} active</span>
							<span class="stat-val">~{dec.estimated_yuv_mb}MB YUV</span>
						</div>
					{/if}
					{#if snapshot?.uptime_ms !== undefined}
						{@const uptimeSec = Math.floor(snapshot.uptime_ms / 1000)}
						{@const hours = Math.floor(uptimeSec / 3600)}
						{@const mins = Math.floor((uptimeSec % 3600) / 60)}
						<div class="stat-row">
							<span class="stat-key">Uptime</span>
							<span class="stat-val" style="flex: 2">{hours}h {mins}m</span>
						</div>
					{/if}
					<div class="stat-row">
						<span class="stat-key">Cuts</span>
						<span class="stat-val" style="flex: 2">{snapshot?.switcher?.cuts_total ?? 0}</span>
					</div>
					<div class="stat-row">
						<span class="stat-key">Transitions</span>
						<span class="stat-val" style="flex: 2">{snapshot?.switcher?.transitions_completed ?? 0} completed</span>
					</div>
					{#if snapshot?.switcher?.frame_rate_converter}
						<div class="stat-row">
							<span class="stat-key">FRC</span>
							<span class="stat-val" style="flex: 2">{snapshot.switcher.frame_rate_converter.quality}</span>
						</div>
					{/if}
				</div>
			</div>

			<!-- Transition Engine (conditional) -->
			{#if snapshot?.switcher?.transition_engine}
				{@const trans = snapshot.switcher.transition_engine}
				<div class="section">
					<div class="section-label">TRANSITION ENGINE</div>
					<div class="stats-grid">
						<div class="stat-row">
							<span class="stat-key">Ingest</span>
							<span class="stat-val">{(trans.ingest_last_ms ?? 0).toFixed(1)}ms</span>
							<span class="stat-val">max {(trans.ingest_max_ms ?? 0).toFixed(1)}ms</span>
						</div>
						<div class="stat-row">
							<span class="stat-key">Blend</span>
							<span class="stat-val">{(trans.blend_last_ms ?? 0).toFixed(1)}ms</span>
							<span class="stat-val">max {(trans.blend_max_ms ?? 0).toFixed(1)}ms</span>
						</div>
						<div class="stat-row">
							<span class="stat-key">Frames</span>
							<span class="stat-val">{trans.frames_ingested ?? 0} in</span>
							<span class="stat-val">{trans.frames_blended ?? 0} blend</span>
						</div>
					</div>
				</div>
			{/if}

			<!-- Audio Mixer -->
			{#if snapshot?.mixer}
				{@const mixer = snapshot.mixer}
				<div class="section">
					<div class="section-label">AUDIO MIXER</div>
					<div class="stats-grid">
						<div class="stat-row">
							<span class="stat-key">Mode</span>
							<span class="stat-val" style="flex: 2">{mixer.mode ?? '-'}</span>
						</div>
						<div class="stat-row">
							<span class="stat-key">Frames</span>
							<span class="stat-val">{fmtCount(mixer.frames_mixed)} mixed</span>
							<span class="stat-val">{fmtCount(mixer.frames_passthrough)} pass</span>
						</div>
						{#if (mixer.max_inter_frame_gap_ms ?? 0) > 0}
							<div class="stat-row" class:warn-row={(mixer.max_inter_frame_gap_ms ?? 0) > 50}>
								<span class="stat-key">Max gap</span>
								<span class="stat-val" style="flex: 2">{(mixer.max_inter_frame_gap_ms ?? 0).toFixed(1)}ms</span>
							</div>
						{/if}
					</div>
				</div>
			{/if}
		</div>
	{:else}
		<div class="loading">Loading...</div>
	{/if}
</div>

<style>
	/* --- Status color variables --- */
	.stats-panel {
		--status-ok: #16a34a;
		--status-warn: #eab308;
		--status-crit: #dc2626;
		--status-ok-dim: rgba(22, 163, 74, 0.15);
		--status-warn-dim: rgba(234, 179, 8, 0.15);
		--status-crit-dim: rgba(220, 38, 38, 0.15);

		position: fixed;
		top: 0;
		right: 0;
		bottom: 0;
		width: 370px;
		background: rgba(9, 9, 11, 0.96);
		border-left: 1px solid var(--border-subtle);
		z-index: 9998;
		transform: translateX(100%);
		transition: transform 200ms ease;
		display: flex;
		flex-direction: column;
		font-family: var(--font-mono);
		font-size: 11px;
		color: var(--text-secondary);
		overflow: hidden;
	}

	.stats-panel.visible {
		transform: translateX(0);
	}

	/* --- Header --- */
	.stats-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 10px 16px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
		transition: background 0.3s ease, border-color 0.3s ease;
	}

	.stats-header.alarm {
		background: var(--status-crit-dim);
		border-bottom-color: rgba(220, 38, 38, 0.4);
	}

	.title-group {
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.panel-title {
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: 11px;
		color: var(--text-primary);
		letter-spacing: 0.5px;
		transition: color 0.3s ease;
	}

	.panel-title.alarm {
		color: var(--status-crit);
	}

	.update-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--status-ok);
		animation: update-blink 2s ease-in-out infinite;
		flex-shrink: 0;
	}

	.update-dot.stale {
		background: var(--status-crit);
		animation: none;
	}

	@keyframes update-blink {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.3; }
	}

	.shortcut-hint {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		opacity: 0.5;
	}

	.close-btn {
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: 16px;
		font-family: var(--font-ui);
		font-weight: 300;
		padding: 2px 6px;
		line-height: 1;
		border-radius: 3px;
		transition: color 0.15s, background 0.15s;
	}

	.close-btn:hover {
		color: var(--text-primary);
		background: rgba(255, 255, 255, 0.06);
	}

	.close-btn:focus-visible {
		outline: 1.5px solid var(--accent-blue);
		outline-offset: 2px;
	}

	/* --- Body --- */
	.panel-body {
		flex: 1;
		overflow-y: auto;
		padding: 12px 16px;
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.section {
		padding: 10px 0;
		border-bottom: 1px solid var(--border-subtle);
		opacity: 0;
		transform: translateY(6px);
		transition: opacity 200ms ease, transform 200ms ease;
	}

	.section:last-child {
		border-bottom: none;
	}

	.stats-panel.visible .section {
		opacity: 1;
		transform: translateY(0);
	}

	.stats-panel.visible .section:nth-child(1) { transition-delay: 30ms; }
	.stats-panel.visible .section:nth-child(2) { transition-delay: 60ms; }
	.stats-panel.visible .section:nth-child(3) { transition-delay: 90ms; }
	.stats-panel.visible .section:nth-child(4) { transition-delay: 120ms; }
	.stats-panel.visible .section:nth-child(5) { transition-delay: 150ms; }
	.stats-panel.visible .section:nth-child(6) { transition-delay: 180ms; }
	.stats-panel.visible .section:nth-child(7) { transition-delay: 210ms; }
	.stats-panel.visible .section:nth-child(8) { transition-delay: 240ms; }

	.section-label {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.5px;
		margin-bottom: 8px;
	}

	.loading {
		padding: 20px 16px;
		color: var(--text-tertiary);
		animation: loading-pulse 1.5s ease-in-out infinite;
	}

	@keyframes loading-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}

	/* --- Pipeline Graph --- */
	.epoch-badge {
		font-family: var(--font-mono);
		font-size: 9px;
		font-weight: 400;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		padding: 1px 6px;
		border-radius: 3px;
		margin-left: 8px;
	}

	.node-graph {
		display: flex;
		align-items: flex-start;
		gap: 0;
		flex-wrap: nowrap;
		overflow-x: auto;
		scrollbar-width: none;
		-ms-overflow-style: none;
		padding-bottom: 4px;
	}

	.node-graph::-webkit-scrollbar {
		display: none;
	}

	.node-box {
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 6px 8px;
		min-width: 52px;
		max-width: 64px;
		text-align: center;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 2px;
		flex-shrink: 0;
		transition: border-color 0.15s, background 0.15s;
	}

	.node-box:not(.inactive):hover {
		border-color: var(--border-strong, var(--border-default));
		background: rgba(255, 255, 255, 0.03);
	}

	.node-box.inactive {
		border-style: dashed;
		border-color: var(--border-subtle);
		opacity: 0.4;
	}

	.node-box.has-error {
		border-color: var(--status-crit);
	}

	.node-name {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 600;
		color: var(--text-primary);
		letter-spacing: 0.3px;
	}

	.node-time {
		font-size: 10px;
		color: var(--text-secondary);
		font-variant-numeric: tabular-nums;
	}

	.inactive-label {
		color: var(--text-tertiary);
		font-style: italic;
	}

	.node-dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		margin-top: 2px;
	}

	.node-dot.ok { background: var(--status-ok); }
	.node-dot.warn {
		background: var(--status-warn);
		box-shadow: 0 0 4px rgba(234, 179, 8, 0.5);
	}
	.node-dot.crit {
		background: var(--status-crit);
		box-shadow: 0 0 6px rgba(220, 38, 38, 0.6);
		animation: pulse-crit 1s ease-in-out infinite;
	}

	@keyframes pulse-crit {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.6; }
	}

	.node-arrow {
		display: flex;
		align-items: center;
		padding: 0 3px;
		color: var(--text-tertiary);
		font-size: 12px;
		margin-top: 12px;
	}

	.branch-container {
		display: flex;
		flex-direction: column;
		align-items: center;
		margin-left: -3px;
		margin-right: -3px;
	}

	.branch-line {
		width: 1px;
		height: 6px;
		background: var(--text-tertiary);
	}

	.branch-node {
		font-size: 9px;
	}

	/* --- Frame Budget Bar --- */
	.budget-bar {
		display: flex;
		height: 22px;
		border-radius: var(--radius-sm);
		overflow: hidden;
		border: 1px solid var(--border-subtle);
		background: var(--bg-base);
	}

	.budget-segment {
		display: flex;
		align-items: center;
		justify-content: center;
		border-right: 1px solid var(--bg-base);
		min-width: 0;
		overflow: hidden;
	}

	.budget-headroom {
		display: flex;
		align-items: center;
		justify-content: center;
		flex: 1;
	}

	.budget-headroom.ok { background: var(--status-ok-dim); }
	.budget-headroom.warn { background: var(--status-warn-dim); }
	.budget-headroom.crit { background: var(--status-crit-dim); }

	.seg-label {
		font-size: 9px;
		font-family: var(--font-ui);
		font-weight: 500;
		color: var(--text-secondary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		padding: 0 2px;
	}

	.budget-summary {
		text-align: right;
		font-size: 10px;
		color: var(--text-tertiary);
		margin-top: 3px;
		font-variant-numeric: tabular-nums;
	}

	.stat-chips {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
		margin-top: 8px;
	}

	.chip {
		font-size: 10px;
		font-variant-numeric: tabular-nums;
		padding: 2px 8px;
		border-radius: 3px;
		background: var(--bg-panel);
		border: 1px solid var(--border-subtle);
		color: var(--text-secondary);
	}

	.warn-chip {
		color: var(--status-warn);
		border-color: rgba(234, 179, 8, 0.3);
	}

	.crit-chip {
		color: var(--status-crit);
		border-color: rgba(220, 38, 38, 0.3);
	}

	/* --- Sparklines --- */
	.sparklines {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.sparkline-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.spark-name {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 600;
		color: var(--text-tertiary);
		width: 32px;
		flex-shrink: 0;
		text-align: right;
	}

	.sparkline-svg {
		flex: 1;
		height: 28px;
		min-width: 0;
	}

	.spark-value {
		font-size: 10px;
		font-variant-numeric: tabular-nums;
		color: var(--text-secondary);
		width: 52px;
		flex-shrink: 0;
		text-align: right;
	}

	.spark-max {
		font-size: 9px;
		font-variant-numeric: tabular-nums;
		color: var(--text-tertiary);
		width: 54px;
		flex-shrink: 0;
		text-align: right;
	}

	/* --- Lip-Sync Gauge --- */
	.sync-gauge {
		padding: 4px 0;
	}

	.sync-labels {
		display: flex;
		justify-content: space-between;
		font-size: 9px;
		color: var(--text-tertiary);
		margin-bottom: 4px;
	}

	.sync-track {
		position: relative;
		height: 14px;
		background: var(--bg-panel);
		border-radius: 7px;
		border: 1px solid var(--border-subtle);
	}

	.sync-safe-zone {
		position: absolute;
		/* +/-5ms safe zone in a +/-30ms range = 10/60 = 16.67% centered */
		left: calc(50% - 8.33%);
		width: 16.67%;
		top: 2px;
		bottom: 2px;
		background: rgba(22, 163, 74, 0.08);
		border-radius: 5px;
	}

	.sync-center {
		position: absolute;
		left: 50%;
		top: 0;
		bottom: 0;
		width: 1px;
		background: var(--text-tertiary);
		opacity: 0.5;
	}

	.sync-marker {
		position: absolute;
		top: -3px;
		width: 8px;
		height: 20px;
		border-radius: 3px;
		transform: translateX(-50%);
		transition: left 0.3s ease, background 0.3s ease;
	}

	.sync-marker.sync-ok { background: var(--status-ok); }
	.sync-marker.sync-warn { background: var(--status-warn); }
	.sync-marker.sync-crit { background: var(--status-crit); }

	.sync-value {
		position: absolute;
		top: -16px;
		left: 50%;
		transform: translateX(-50%);
		font-size: 9px;
		font-variant-numeric: tabular-nums;
		color: var(--text-secondary);
		white-space: nowrap;
	}

	.sync-scale {
		display: flex;
		justify-content: space-between;
		font-size: 9px;
		color: var(--text-tertiary);
		margin-top: 2px;
	}

	/* --- System Stats Grid --- */
	.stats-grid {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.stat-row {
		display: flex;
		align-items: baseline;
		gap: 8px;
	}

	.stat-key {
		font-family: var(--font-ui);
		font-size: 10px;
		color: var(--text-tertiary);
		width: 70px;
		flex-shrink: 0;
	}

	.stat-val {
		font-size: 10px;
		font-variant-numeric: tabular-nums;
		color: var(--text-secondary);
		flex: 1;
	}

	.warn-row .stat-val { color: var(--status-warn); }
</style>
