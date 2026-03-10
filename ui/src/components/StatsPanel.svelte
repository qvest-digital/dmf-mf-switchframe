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
		total_nodes?: number;
	}

	interface SourceDebug {
		video_frames_in: number;
		audio_frames_in: number;
		health_status: string;
		last_frame_ago_ms: number;
		raw_pipeline: boolean;
	}

	interface RelayViewer {
		id: string;
		video_sent: number;
		video_dropped: number;
		audio_sent: number;
		audio_dropped: number;
		bytes_sent: number;
	}

	interface ReplayBuffer {
		frameCount: number;
		gopCount: number;
		durationSecs: number;
		bytesUsed: number;
	}

	interface DebugSnapshot {
		uptime_ms?: number;
		switcher?: {
			program_source?: string;
			preview_source?: string;
			state?: string;
			in_transition?: boolean;
			ftb_active?: boolean;
			seq?: number;
			sources?: Record<string, SourceDebug>;
			source_decoders?: { active_count: number; estimated_yuv_mb: number };
			pipeline?: PipelineSnapshot;
			video_pipeline?: {
				output_fps: number;
				frames_processed: number;
				frames_broadcast: number;
				frames_dropped: number;
				encode_nil: number;
				queue_len: number;
				last_proc_time_ms: number;
				max_proc_time_ms: number;
				max_broadcast_gap_ms: number;
				route_to_engine: number;
				route_to_idle_engine: number;
				route_to_pipeline: number;
				route_filtered: number;
				trans_output: number;
				trans_seam_last_ms: number;
				trans_seam_max_ms: number;
				trans_seam_count: number;
			};
			frame_budget_ms?: number;
			frame_pool?: { hits: number; misses: number; capacity: number; buf_size: number };
			transition_engine?: {
				ingest_last_ms: number; ingest_max_ms: number;
				blend_last_ms: number; blend_max_ms: number;
				decode_last_ms?: number; decode_max_ms?: number;
				frames_ingested: number; frames_blended: number;
			};
			cuts_total?: number;
			transitions_started?: number;
			transitions_completed?: number;
			deadline_violations?: number;
			frame_rate_converter?: { quality: string };
			program_relay_viewers?: RelayViewer[];
		};
		mixer?: {
			mode: string;
			program_peak_dbfs?: [number, number];
			channels_active?: number;
			channels_muted?: number;
			frames_mixed: number;
			frames_passthrough: number;
			frames_output_total?: number;
			crossfade_count?: number;
			crossfade_timeouts?: number;
			decode_errors?: number;
			encode_errors?: number;
			deadline_flushes?: number;
			max_inter_frame_gap_ms?: number;
			mode_transitions?: number;
			trans_crossfade_active?: boolean;
			trans_crossfade_pos?: number;
			trans_crossfade_from?: string;
			trans_crossfade_to?: string;
			trans_crossfade_count?: number;
		};
		output?: {
			recording?: { active: boolean; fileCount?: number; latestFile?: string };
			srt?: { active: boolean; enabled?: boolean };
			viewer?: { video_sent: number; video_dropped: number; audio_sent: number; audio_dropped: number } | null;
			destinations?: Array<{ name?: string; active?: boolean }>;
		};
		replay?: {
			state?: string;
			source?: string;
			speed?: number;
			loop?: boolean;
			buffers?: Record<string, ReplayBuffer>;
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
		'upstream-key':      { display: 'Upstream Key',       short: 'KEY',  color: 'rgba(167, 139, 250, 0.7)' },
		'compositor':        { display: 'DSK Compositor',     short: 'DSK',  color: 'rgba(59, 130, 246, 0.7)' },
		'layout-compositor': { display: 'Layout Compositor',  short: 'LAY',  color: 'rgba(139, 92, 246, 0.7)' },
		'raw-sink-mxl':      { display: 'Raw Sink MXL',      short: 'MXL',  color: 'rgba(234, 179, 8, 0.7)' },
		'raw-sink-monitor':  { display: 'Raw Monitor',        short: 'MON',  color: 'rgba(245, 158, 11, 0.7)' },
		'h264-encode':       { display: 'H.264 Encode',      short: 'ENC',  color: 'rgba(52, 211, 153, 0.7)' },
	};

	// Canonical pipeline order for display (MON inline after MXL)
	const PIPELINE_ORDER = ['upstream-key', 'layout-compositor', 'compositor', 'raw-sink-mxl', 'raw-sink-monitor', 'h264-encode'];

	// Tap nodes that route to a side output destination
	const SIDE_OUTPUT: Record<string, string> = {
		'raw-sink-mxl': 'MXL Output',
		'raw-sink-monitor': 'Raw Monitor',
	};

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
			nodeHistory = new Map();
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

	function fmtBytes(bytes: number): string {
		if (bytes < 1024) return `${bytes}B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
		return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
	}

	function fmtDuration(secs: number): string {
		if (secs < 60) return `${secs.toFixed(0)}s`;
		const m = Math.floor(secs / 60);
		const s = Math.floor(secs % 60);
		return `${m}m${s}s`;
	}

	function fmtDbfs(db: number): string {
		return db.toFixed(1);
	}

	function nodeDisplayName(name: string): string {
		return NODE_META[name]?.display ?? name;
	}

	function nodeShortName(name: string): string {
		return NODE_META[name]?.short ?? name.replace(/-/g, ' ').substring(0, 5).toUpperCase();
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

	function healthDot(status: string): string {
		switch (status) {
			case 'healthy': return 'ok';
			case 'stale': return 'warn';
			default: return 'crit';
		}
	}

	function sourceFps(framesIn: number, uptimeMs: number | undefined): string {
		if (!uptimeMs || !framesIn) return '0';
		return (framesIn / (uptimeMs / 1000)).toFixed(0);
	}

	// --- Pipeline graph data ---
	const pipelineData = $derived.by(() => {
		const pipe = snapshot?.switcher?.pipeline;
		if (!pipe) return null;
		const activeNodes = pipe.active_nodes ?? [];
		const activeMap = new Map(activeNodes.map(n => [n.name, n]));
		const activeNames = new Set(activeNodes.map(n => n.name));
		const frameBudgetNs = snapshot?.switcher?.frame_budget_ms
			? snapshot.switcher.frame_budget_ms * 1e6
			: DEFAULT_FRAME_BUDGET_NS;

		// Build dynamic display chain (MON inline after MXL)
		const displayChain: string[] = [];
		for (const name of PIPELINE_ORDER) {
			// Skip compositor when layout-compositor is active (mutually exclusive)
			if (name === 'compositor' && activeNames.has('layout-compositor')) continue;
			if (name === 'layout-compositor' && !activeNames.has('layout-compositor')) continue;
			displayChain.push(name);
		}
		// Add any unknown active nodes (future-proof)
		const knownSet = new Set(PIPELINE_ORDER);
		for (const node of activeNodes) {
			if (!knownSet.has(node.name) && !displayChain.includes(node.name)) {
				displayChain.push(node.name);
			}
		}

		return {
			epoch: pipe.epoch,
			runCount: pipe.run_count,
			lastRunNs: pipe.last_run_ns,
			maxRunNs: pipe.max_run_ns,
			totalLatencyUs: pipe.total_latency_us,
			lipSyncHintUs: pipe.lip_sync_hint_us,
			totalNodes: pipe.total_nodes,
			frameBudgetNs,
			activeNodes,
			activeMap,
			displayChain,
		};
	});

	// --- Alarm state ---
	const hasAlarm = $derived.by(() => {
		if (!pipelineData) return false;
		const hasCritNode = pipelineData.activeNodes.some(
			n => nodeStatus(n.last_ns, pipelineData.frameBudgetNs) === 'crit'
		);
		const hasViolations = (snapshot?.switcher?.deadline_violations ?? 0) > 0;
		const overBudget = pipelineData.lastRunNs > pipelineData.frameBudgetNs;
		return hasCritNode || hasViolations || overBudget;
	});

	// --- Derived metrics ---
	const derivedMetrics = $derived.by(() => {
		const vp = snapshot?.switcher?.video_pipeline;
		const pool = snapshot?.switcher?.frame_pool;
		const replay = snapshot?.replay?.buffers;
		const decoders = snapshot?.switcher?.source_decoders;
		const budgetMs = (snapshot?.switcher?.frame_budget_ms ?? 33.3);

		// Drop rate
		const dropRate = vp && vp.frames_processed > 0
			? ((vp.frames_dropped / vp.frames_processed) * 100)
			: 0;

		// Max gap in frame-times
		const maxGapFrames = vp ? (vp.max_broadcast_gap_ms / budgetMs) : 0;

		// Max proc in frame-times
		const maxProcFrames = vp ? (vp.max_proc_time_ms / budgetMs) : 0;

		// Trans seam in frame-times
		const seamMaxFrames = vp ? (vp.trans_seam_max_ms / budgetMs) : 0;

		// Memory estimates
		const poolMemMB = pool ? (pool.capacity * pool.buf_size / (1024 * 1024)) : 0;
		const replayMemBytes = replay
			? Object.values(replay).reduce((sum, b) => sum + b.bytesUsed, 0)
			: 0;
		const replayMemMB = replayMemBytes / (1024 * 1024);
		const decoderMemMB = decoders?.estimated_yuv_mb ?? 0;
		const totalMemMB = poolMemMB + replayMemMB + decoderMemMB;

		// Replay total duration
		const replayTotalSecs = replay
			? Math.max(...Object.values(replay).map(b => b.durationSecs))
			: 0;

		// Viewer stats
		const viewers = snapshot?.switcher?.program_relay_viewers ?? [];
		const totalViewerDrops = viewers.reduce((sum, v) => sum + v.video_dropped, 0);
		const totalViewerBytes = viewers.reduce((sum, v) => sum + v.bytes_sent, 0);

		// Audio error total
		const audioErrors = (snapshot?.mixer?.decode_errors ?? 0) + (snapshot?.mixer?.encode_errors ?? 0);

		return {
			dropRate, maxGapFrames, maxProcFrames, seamMaxFrames,
			poolMemMB, replayMemMB, decoderMemMB, totalMemMB,
			replayTotalSecs, viewers, totalViewerDrops, totalViewerBytes,
			audioErrors,
		};
	});

	// --- Sparkline helpers ---
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

	function budgetThresholdY(values: number[], height: number, budgetNs: number): number | null {
		if (values.length < 2) return null;
		const max = Math.max(...values, 1);
		if (budgetNs > max) return null; // threshold off chart
		return height - (budgetNs / max) * (height - 2) - 1;
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
						{#if pipelineData.totalNodes}
							<span class="node-count">{pipelineData.activeNodes.length}/{pipelineData.totalNodes} nodes</span>
						{/if}
					{/if}
				</div>
				{#if pipelineData}
					<div class="pipeline-flow">
						<!-- Input summary -->
						<div class="flow-input">
							<span class="flow-box-label">INPUTS</span>
							<span class="flow-box-detail">
								{snapshot?.switcher?.source_decoders?.active_count ?? 0} decoders
								{#if snapshot?.switcher?.program_source}
									· PGM: {snapshot.switcher.program_source}
								{/if}
								{#if snapshot?.switcher?.video_pipeline?.output_fps}
									· {snapshot.switcher.video_pipeline.output_fps}fps
								{/if}
							</span>
						</div>

						<!-- Vertical node chain -->
						{#each pipelineData.displayChain as name}
							{@const node = pipelineData.activeMap.get(name)}
							{@const status = node ? nodeStatus(node.last_ns, pipelineData.frameBudgetNs) : 'ok'}
							{@const budgetRatio = node ? Math.min(node.last_ns / pipelineData.frameBudgetNs, 1) : 0}
							{@const budgetPct = node ? ((node.last_ns / pipelineData.frameBudgetNs) * 100).toFixed(1) : '0'}
							<div
								class="flow-node {status}"
								class:inactive={!node}
								class:has-error={node?.last_error}
								title={node ? `${nodeDisplayName(name)}: ${fmtMs(node.last_ns)}ms (max ${fmtMs(node.max_ns)}ms)` : `${nodeDisplayName(name)}: inactive`}
							>
								{#if node}
									<div class="flow-node-top">
										<span class="flow-node-left">
											<span class="node-name">{nodeShortName(name)}</span>
											<span class="flow-node-fullname">{nodeDisplayName(name)}</span>
										</span>
										<span class="flow-node-right">
											<span class="node-time">{fmtMs(node.last_ns)}ms</span>
											{#if SIDE_OUTPUT[name]}
												<span class="flow-side-output">&rarr; {SIDE_OUTPUT[name]}</span>
											{/if}
										</span>
									</div>
									<div class="flow-node-detail">
										<span>max {fmtMs(node.max_ns)}ms · {node.latency_us}µs</span>
										<span class="flow-budget-pct">{budgetPct}%</span>
									</div>
									<div class="node-micro-bar">
										<div class="node-micro-fill {status}" style="width: {budgetRatio * 100}%"></div>
									</div>
								{:else}
									<div class="flow-node-top">
										<span class="flow-node-left">
											<span class="node-name">{nodeShortName(name)}</span>
											<span class="flow-node-fullname">{nodeDisplayName(name)}</span>
										</span>
										<span class="flow-node-right">
											<span class="node-time inactive-label">off</span>
										</span>
									</div>
								{/if}
							</div>
						{/each}

						<!-- Output summary -->
						<div class="flow-output">
							<span class="flow-box-label">OUTPUTS</span>
							<span class="flow-box-detail">
								{derivedMetrics.viewers.length} viewer{derivedMetrics.viewers.length !== 1 ? 's' : ''}
								{#if derivedMetrics.totalViewerBytes > 0}
									· {fmtBytes(derivedMetrics.totalViewerBytes)}
								{/if}
								· Rec: {snapshot?.output?.recording?.active ? 'ON' : 'off'}
								{#if snapshot?.output?.srt?.active}
									· SRT: LIVE
								{/if}
							</span>
						</div>
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
					{@const isOverBudget = totalUsedMs > budgetMs}
					<div class="budget-bar">
						{#each pipelineData.activeNodes as node}
							{@const pct = (node.last_ns / pipelineData.frameBudgetNs) * 100}
							{#if pct > MIN_SEGMENT_PCT}
								<div
									class="budget-segment"
									style="width: {Math.min(pct, 100)}%; background: {nodeColor(node.name).replace('0.7', '0.25')}"
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
					<div class="budget-summary" class:over-budget={isOverBudget}>
						{#if isOverBudget}
							{totalUsedMs.toFixed(1)}ms / {budgetMs.toFixed(1)}ms — <span class="over-label">+{(totalUsedMs - budgetMs).toFixed(1)}ms OVER</span>
						{:else}
							{totalUsedMs.toFixed(1)}ms / {budgetMs.toFixed(1)}ms ({usedPct.toFixed(0)}%)
						{/if}
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
							{#if derivedMetrics.dropRate > 0}
								({derivedMetrics.dropRate.toFixed(2)}%)
							{/if}
						</span>
						<span class="chip" class:warn-chip={(pipe.queue_len ?? 0) >= 4} class:crit-chip={(pipe.queue_len ?? 0) >= 6}>
							{pipe.queue_len ?? 0}/8 queue
						</span>
						{#if (snapshot?.switcher?.deadline_violations ?? 0) > 0}
							<span class="chip crit-chip">
								{fmtCount(snapshot.switcher?.deadline_violations)} violations
							</span>
						{/if}
						{#if (pipe.max_broadcast_gap_ms ?? 0) > 0}
							<span class="chip" class:warn-chip={derivedMetrics.maxGapFrames > 1.5} class:crit-chip={derivedMetrics.maxGapFrames > 3}>
								{pipe.max_broadcast_gap_ms.toFixed(1)}ms max gap
							</span>
						{/if}
						{#if (pipe.max_proc_time_ms ?? 0) > 0}
							<span class="chip" class:warn-chip={derivedMetrics.maxProcFrames > 0.5} class:crit-chip={derivedMetrics.maxProcFrames > 1}>
								{pipe.max_proc_time_ms.toFixed(1)}ms max proc
							</span>
						{/if}
						{#if (pipe.trans_seam_max_ms ?? 0) > 0}
							<span class="chip" class:warn-chip={derivedMetrics.seamMaxFrames > 1} class:crit-chip={derivedMetrics.seamMaxFrames > 3}>
								{pipe.trans_seam_max_ms.toFixed(1)}ms seam
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
							{@const threshY = budgetThresholdY(history, 28, pipelineData.frameBudgetNs)}
							<div class="sparkline-row">
								<span class="spark-name">{nodeShortName(node.name)}</span>
								<svg class="sparkline-svg" viewBox="0 0 200 28" preserveAspectRatio="none" aria-hidden="true">
									<!-- Budget threshold line -->
									{#if threshY !== null}
										<line x1="0" y1={threshY} x2="200" y2={threshY}
											stroke="var(--status-crit)" stroke-width="0.5" stroke-dasharray="3,2" opacity="0.5" />
									{/if}
									<!-- Midpoint reference -->
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
							<span class="sync-value-label">{hintMs.toFixed(1)}ms</span>
							<span>video leads</span>
						</div>
						<div class="sync-track" role="meter" aria-label="Lip sync offset" aria-valuenow={hintMs} aria-valuemin={-SYNC_RANGE_MS} aria-valuemax={SYNC_RANGE_MS}>
							<div class="sync-safe-zone" aria-hidden="true"></div>
							<div class="sync-center" aria-hidden="true"></div>
							<div class="sync-marker {syncClass}" style="left: {markerPct}%"></div>
						</div>
						<div class="sync-scale" aria-hidden="true">
							<span>-{SYNC_RANGE_MS}</span>
							<span>0</span>
							<span>+{SYNC_RANGE_MS}</span>
						</div>
					</div>
				</div>
			{/if}

			<!-- Sources Health Grid -->
			{#if snapshot?.switcher?.sources}
				{@const sources = snapshot.switcher.sources}
				{@const sourceKeys = Object.keys(sources).sort()}
				<div class="section">
					<div class="section-label">
						SOURCES
						<span class="node-count">{sourceKeys.length} sources</span>
					</div>
					<div class="source-grid">
						{#each sourceKeys as key}
							{@const src = sources[key]}
							<div class="source-row">
								<span class="source-dot {healthDot(src.health_status)}"></span>
								<span class="source-name" title={key}>{key}</span>
								<span class="source-fps">{sourceFps(src.video_frames_in, snapshot?.uptime_ms)}fps</span>
								<span class="source-ago" class:warn-text={src.last_frame_ago_ms > 50} class:crit-text={src.last_frame_ago_ms > 100}>{src.last_frame_ago_ms}ms</span>
								{#if src.raw_pipeline}
									<span class="source-badge">DEC</span>
								{/if}
							</div>
						{/each}
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
					<div class="stat-row">
						<span class="stat-key">Memory</span>
						<span class="stat-val" style="flex: 2">
							~{derivedMetrics.totalMemMB.toFixed(0)}MB
							<span class="stat-detail">
								(pool {derivedMetrics.poolMemMB.toFixed(0)} + replay {derivedMetrics.replayMemMB.toFixed(0)} + dec {derivedMetrics.decoderMemMB})
							</span>
						</span>
					</div>
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

			<!-- Output & Viewers -->
			<div class="section">
				<div class="section-label">OUTPUT</div>
				<div class="stats-grid">
					<div class="stat-row">
						<span class="stat-key">Recording</span>
						<span class="stat-val" style="flex: 2">
							{#if snapshot?.output?.recording?.active}
								<span class="rec-active">REC</span>
							{:else}
								inactive
							{/if}
						</span>
					</div>
					<div class="stat-row">
						<span class="stat-key">SRT</span>
						<span class="stat-val" style="flex: 2">
							{#if snapshot?.output?.srt?.active}
								<span class="srt-active">LIVE</span>
							{:else}
								inactive
							{/if}
						</span>
					</div>
					{#if derivedMetrics.viewers.length > 0}
						<div class="stat-row">
							<span class="stat-key">Viewers</span>
							<span class="stat-val">{derivedMetrics.viewers.length} connected</span>
							<span class="stat-val" class:crit-text={derivedMetrics.totalViewerDrops > 0}>
								{derivedMetrics.totalViewerDrops} drops
							</span>
						</div>
						{#if derivedMetrics.totalViewerBytes > 0}
							<div class="stat-row">
								<span class="stat-key">Sent</span>
								<span class="stat-val" style="flex: 2">{fmtBytes(derivedMetrics.totalViewerBytes)}</span>
							</div>
						{/if}
					{/if}
					{#if snapshot?.output?.destinations && snapshot.output.destinations.length > 0}
						<div class="stat-row">
							<span class="stat-key">Destinations</span>
							<span class="stat-val" style="flex: 2">{snapshot.output.destinations.length}</span>
						</div>
					{/if}
				</div>
			</div>

			<!-- Replay Buffers -->
			{#if snapshot?.replay?.buffers && Object.keys(snapshot.replay.buffers).length > 0}
				{@const buffers = snapshot.replay.buffers}
				{@const bufferKeys = Object.keys(buffers).sort()}
				<div class="section">
					<div class="section-label">
						REPLAY
						<span class="node-count">{fmtBytes(bufferKeys.reduce((s, k) => s + buffers[k].bytesUsed, 0))} total</span>
					</div>
					<div class="replay-grid">
						{#each bufferKeys as key}
							{@const buf = buffers[key]}
							<div class="replay-row">
								<span class="replay-name">{key}</span>
								<span class="replay-dur">{fmtDuration(buf.durationSecs)}</span>
								<span class="replay-frames">{fmtCount(buf.frameCount)}f</span>
								<span class="replay-gops">{buf.gopCount} GOPs</span>
								<span class="replay-size">{fmtBytes(buf.bytesUsed)}</span>
							</div>
						{/each}
					</div>
				</div>
			{/if}

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
						{#if mixer.program_peak_dbfs}
							<div class="stat-row">
								<span class="stat-key">Peak</span>
								<span class="stat-val">L {fmtDbfs(mixer.program_peak_dbfs[0])} dBFS</span>
								<span class="stat-val">R {fmtDbfs(mixer.program_peak_dbfs[1])} dBFS</span>
							</div>
						{/if}
						<div class="stat-row">
							<span class="stat-key">Frames</span>
							<span class="stat-val">{fmtCount(mixer.frames_mixed)} mixed</span>
							<span class="stat-val">{fmtCount(mixer.frames_passthrough)} pass</span>
						</div>
						{#if (mixer.channels_active ?? 0) > 0 || (mixer.channels_muted ?? 0) > 0}
							<div class="stat-row">
								<span class="stat-key">Channels</span>
								<span class="stat-val">{mixer.channels_active ?? 0} active</span>
								<span class="stat-val">{mixer.channels_muted ?? 0} muted</span>
							</div>
						{/if}
						{#if (mixer.max_inter_frame_gap_ms ?? 0) > 0}
							<div class="stat-row" class:warn-row={(mixer.max_inter_frame_gap_ms ?? 0) > 50} class:crit-row={(mixer.max_inter_frame_gap_ms ?? 0) > 100}>
								<span class="stat-key">Max gap</span>
								<span class="stat-val" style="flex: 2">{(mixer.max_inter_frame_gap_ms ?? 0).toFixed(1)}ms</span>
							</div>
						{/if}
						{#if (mixer.trans_crossfade_count ?? 0) > 0 || (mixer.crossfade_count ?? 0) > 0}
							<div class="stat-row">
								<span class="stat-key">Crossfades</span>
								<span class="stat-val">{mixer.crossfade_count ?? 0} cut</span>
								<span class="stat-val">{mixer.trans_crossfade_count ?? 0} trans</span>
							</div>
						{/if}
						{#if mixer.trans_crossfade_active}
							<div class="stat-row">
								<span class="stat-key">XFade</span>
								<span class="stat-val" style="flex: 2">
									{mixer.trans_crossfade_from} → {mixer.trans_crossfade_to}
									({((mixer.trans_crossfade_pos ?? 0) * 100).toFixed(0)}%)
								</span>
							</div>
						{/if}
						{#if derivedMetrics.audioErrors > 0}
							<div class="stat-row crit-row">
								<span class="stat-key">Errors</span>
								<span class="stat-val">{mixer.decode_errors ?? 0} dec</span>
								<span class="stat-val">{mixer.encode_errors ?? 0} enc</span>
							</div>
						{/if}
						{#if (mixer.mode_transitions ?? 0) > 0}
							<div class="stat-row">
								<span class="stat-key">Mode Δ</span>
								<span class="stat-val" style="flex: 2">{mixer.mode_transitions} transitions</span>
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
	.stats-panel.visible .section:nth-child(9) { transition-delay: 270ms; }
	.stats-panel.visible .section:nth-child(10) { transition-delay: 300ms; }
	.stats-panel.visible .section:nth-child(11) { transition-delay: 330ms; }

	.section-label {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.5px;
		margin-bottom: 8px;
		display: flex;
		align-items: center;
		gap: 8px;
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
	}

	.node-count {
		font-family: var(--font-mono);
		font-size: 9px;
		font-weight: 400;
		color: var(--text-tertiary);
		margin-left: auto;
	}

	/* --- Vertical Pipeline Flow --- */
	.pipeline-flow {
		display: flex;
		flex-direction: column;
		gap: 0;
		position: relative;
		padding-left: 16px;
	}

	/* Vertical spine line */
	.pipeline-flow::before {
		content: '';
		position: absolute;
		left: 22px;
		top: 0;
		bottom: 0;
		width: 2px;
		background: var(--border-subtle);
		z-index: 0;
	}

	/* Individual flow node card */
	.flow-node {
		position: relative;
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-left: 3px solid var(--status-ok);
		border-radius: var(--radius-sm);
		padding: 6px 10px 8px;
		margin: 3px 0 3px 20px;
		overflow: hidden;
		transition: border-color 0.15s, background 0.15s;
		z-index: 1;
	}

	/* Tick mark connecting node to spine */
	.flow-node::before {
		content: '';
		position: absolute;
		left: -21px;
		top: 50%;
		width: 18px;
		height: 2px;
		background: var(--border-subtle);
	}

	/* Heat-based left border */
	.flow-node.ok {
		border-left-color: var(--status-ok);
		background: rgba(22, 163, 74, 0.04);
	}
	.flow-node.warn {
		border-left-color: var(--status-warn);
		background: rgba(234, 179, 8, 0.06);
	}
	.flow-node.crit {
		border-left-color: var(--status-crit);
		background: rgba(220, 38, 38, 0.06);
		animation: pulse-crit 1s ease-in-out infinite;
	}

	.flow-node:not(.inactive):hover {
		border-color: var(--border-strong, var(--border-default));
	}
	.flow-node.ok:not(.inactive):hover { border-left-color: var(--status-ok); }
	.flow-node.warn:not(.inactive):hover { border-left-color: var(--status-warn); }
	.flow-node.crit:not(.inactive):hover { border-left-color: var(--status-crit); }

	.flow-node.inactive {
		border-style: dashed;
		border-left-style: dashed;
		border-color: var(--border-subtle);
		border-left-color: var(--border-subtle);
		background: transparent;
		opacity: 0.4;
	}

	.flow-node.has-error {
		border-color: var(--status-crit);
		border-left-color: var(--status-crit);
	}

	.flow-node-top {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 8px;
	}

	.flow-node-left {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
	}

	.flow-node-fullname {
		font-size: 10px;
		color: var(--text-tertiary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.flow-node-right {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-shrink: 0;
	}

	.flow-node-detail {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-top: 2px;
		font-size: 9px;
		color: var(--text-tertiary);
		font-variant-numeric: tabular-nums;
	}

	.flow-budget-pct {
		font-weight: 500;
	}

	.flow-side-output {
		font-size: 9px;
		font-weight: 500;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		padding: 1px 5px;
		border-radius: 3px;
		white-space: nowrap;
	}

	/* Input/output summary boxes */
	.flow-input,
	.flow-output {
		position: relative;
		background: var(--bg-panel);
		border: 1px solid var(--border-subtle);
		border-left: 3px solid var(--accent-blue);
		border-radius: var(--radius-sm);
		padding: 6px 10px;
		margin-left: 20px;
		z-index: 1;
	}

	.flow-input {
		margin-bottom: 2px;
	}

	.flow-output {
		margin-top: 2px;
	}

	/* Connector from input/output to spine */
	.flow-input::before,
	.flow-output::before {
		content: '';
		position: absolute;
		left: -21px;
		top: 50%;
		width: 18px;
		height: 2px;
		background: var(--border-subtle);
	}

	.flow-box-label {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.5px;
		margin-right: 8px;
	}

	.flow-box-detail {
		font-size: 10px;
		color: var(--text-secondary);
		overflow: hidden;
		white-space: nowrap;
		text-overflow: ellipsis;
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

	/* Micro progress bar at bottom of node */
	.node-micro-bar {
		position: absolute;
		bottom: 0;
		left: 0;
		right: 0;
		height: 2px;
		background: rgba(255, 255, 255, 0.04);
	}

	.node-micro-fill {
		height: 100%;
		transition: width 0.3s ease;
	}

	.node-micro-fill.ok { background: var(--status-ok); }
	.node-micro-fill.warn { background: var(--status-warn); }
	.node-micro-fill.crit { background: var(--status-crit); }

	@keyframes pulse-crit {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.6; }
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

	.budget-summary.over-budget {
		color: var(--status-crit);
	}

	.over-label {
		font-weight: 600;
		color: var(--status-crit);
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

	.sync-value-label {
		font-variant-numeric: tabular-nums;
		color: var(--text-secondary);
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

	.sync-scale {
		display: flex;
		justify-content: space-between;
		font-size: 9px;
		color: var(--text-tertiary);
		margin-top: 2px;
	}

	/* --- Source Health Grid --- */
	.source-grid {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.source-row {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 10px;
	}

	.source-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.source-dot.ok { background: var(--status-ok); }
	.source-dot.warn { background: var(--status-warn); }
	.source-dot.crit { background: var(--status-crit); }

	.source-name {
		color: var(--text-secondary);
		width: 80px;
		flex-shrink: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.source-fps {
		font-variant-numeric: tabular-nums;
		color: var(--text-tertiary);
		width: 40px;
		text-align: right;
	}

	.source-ago {
		font-variant-numeric: tabular-nums;
		color: var(--text-tertiary);
		width: 36px;
		text-align: right;
	}

	.source-badge {
		font-family: var(--font-ui);
		font-size: 8px;
		font-weight: 600;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		padding: 0px 4px;
		border-radius: 2px;
		letter-spacing: 0.3px;
	}

	.warn-text { color: var(--status-warn); }
	.crit-text { color: var(--status-crit); }

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

	.stat-detail {
		font-size: 9px;
		color: var(--text-tertiary);
	}

	.warn-row .stat-val, .warn-row .stat-key { color: var(--status-warn); }
	.crit-row .stat-val, .crit-row .stat-key { color: var(--status-crit); }

	/* --- Output --- */
	.rec-active {
		color: var(--status-crit);
		font-weight: 600;
		animation: pulse-crit 1.5s ease-in-out infinite;
	}

	.srt-active {
		color: var(--status-ok);
		font-weight: 600;
	}

	/* --- Replay Grid --- */
	.replay-grid {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.replay-row {
		display: flex;
		align-items: baseline;
		gap: 6px;
		font-size: 10px;
		font-variant-numeric: tabular-nums;
	}

	.replay-name {
		color: var(--text-secondary);
		width: 60px;
		flex-shrink: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.replay-dur {
		color: var(--text-secondary);
		width: 36px;
		text-align: right;
	}

	.replay-frames {
		color: var(--text-tertiary);
		width: 44px;
		text-align: right;
	}

	.replay-gops {
		color: var(--text-tertiary);
		width: 50px;
		text-align: right;
	}

	.replay-size {
		color: var(--text-tertiary);
		width: 44px;
		text-align: right;
		margin-left: auto;
	}
</style>
