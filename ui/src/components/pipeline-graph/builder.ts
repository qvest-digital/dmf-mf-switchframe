import type {
	GraphNode,
	GraphEdge,
	PipelineGraph,
	NodeCategory,
	HealthStatus,
	BufferIndicator
} from './types';
import {
	sourceHealth,
	decodeHealth,
	pipelineNodeHealth,
	audioMixerHealth,
	srtIngestHealth,
	srtOutputHealth,
	previewEncodeHealth,
	browserDecodeHealth,
	bufferHealth,
	nsToMs
} from './health';

/* ---------- helpers ---------- */

/** Determine source type badge from source key prefix.
 *  Demo sources have NO prefix (just 'cam1', 'cam2', etc.). */
export function sourceType(key: string): string {
	if (key.startsWith('srt:')) return 'SRT';
	if (key.startsWith('mxl:')) return 'MXL';
	if (key.startsWith('clip-player-')) return 'Clip';
	if (key.startsWith('demo:')) return 'Demo';
	if (/^cam\d+$/.test(key)) return 'Demo';
	if (key === 'replay') return 'Replay';
	return 'Source';
}

/** Strip prefix from source key for display label. */
export function sourceLabel(key: string): string {
	if (key.startsWith('srt:')) return key.slice(4);
	if (key.startsWith('mxl:')) return key.slice(4);
	if (key.startsWith('clip-player-')) return key.slice(12);
	if (key.startsWith('demo:')) return key.slice(5);
	return key;
}

/** Map internal pipeline node names to display labels. */
const pipelineNodeLabels: Record<string, string> = {
	'upstream-key': 'Upstream Key',
	'layout-compositor': 'Layout',
	'dsk-compositor': 'DSK Compositor',
	'raw-sink': 'Raw Sink',
	'raw-monitor-sink': 'Raw Monitor',
	'h264-encode': 'H.264 Encode'
};

export function pipelineNodeLabel(name: string): string {
	return pipelineNodeLabels[name] ?? name;
}

/** Canonical pipeline node ordering (processing column). */
const pipelineNodeOrder = [
	'upstream-key',
	'layout-compositor',
	'dsk-compositor',
	'raw-sink',
	'raw-monitor-sink',
	'h264-encode'
];

/* ---------- builder ---------- */

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function buildGraph(perf: any, browserDiag: Record<string, any> | null = null): PipelineGraph {
	const nodes: GraphNode[] = [];
	const edges: GraphEdge[] = [];

	if (!perf) {
		return { nodes, edges };
	}

	const sources: Record<string, any> = perf.sources ?? {};
	const sourceKeys = Object.keys(sources).sort();
	const frameBudgetNs: number = perf.frame_budget_ns ?? 0;

	/* ---------- Column 1: Ingest ---------- */

	sourceKeys.forEach((key, row) => {
		const src = sources[key];
		const avgFps = src.decode?.current?.avg_fps ?? 0;
		const kpis: Record<string, string> = {};

		// Only show FPS if the source actually reports it (raw sources report 0)
		if (avgFps > 0) {
			kpis.fps = avgFps.toFixed(1);
		}

		let health: HealthStatus = sourceHealth(src.health ?? 'offline');

		// SRT-specific KPIs
		if (src.srt) {
			kpis.rtt = src.srt.rtt_ms.toFixed(1) + 'ms';
			kpis.loss = src.srt.loss_rate_pct.toFixed(2) + '%';
			// Worst-of source health and SRT health
			const srtH = srtIngestHealth(src.srt.loss_rate_pct);
			if (srtH === 'error' || health === 'error') health = 'error';
			else if (srtH === 'degraded' || health === 'degraded') health = 'degraded';
		}

		// Show health status text for non-healthy sources
		if (health !== 'healthy') {
			kpis.status = src.health ?? 'unknown';
		}

		nodes.push({
			id: `source-${key}`,
			label: sourceLabel(key),
			category: 'source',
			column: 'ingest',
			row,
			badge: sourceType(key),
			kpis,
			health,
			detail: src
		});
	});

	/* ---------- Column 2: Decode ---------- */

	sourceKeys.forEach((key, row) => {
		const src = sources[key];
		const lastNs = src.decode?.current?.last_ns ?? 0;
		const drops = src.decode?.current?.drops ?? 0;
		const avgFps = src.decode?.current?.avg_fps ?? 0;
		const isRaw = avgFps === 0 && lastNs === 0;

		const kpis: Record<string, string> = {};
		if (isRaw) {
			kpis.mode = 'raw YUV';
		} else {
			kpis.latency = nsToMs(lastNs);
			if (drops > 0) kpis.drops = String(drops);
		}

		nodes.push({
			id: `decode-${key}`,
			label: isRaw ? 'Raw Input' : 'Decode',
			category: 'decode',
			column: 'decode',
			row,
			kpis,
			health: isRaw ? 'healthy' : decodeHealth(lastNs, drops),
			detail: src.decode
		});

		// Edge: source → decode
		edges.push({ from: `source-${key}`, to: `decode-${key}` });
	});

	/* ---------- Column 3: Processing ---------- */

	let processingRow = 0;

	// Frame sync node (fan-in point)
	const hasFrameSync = sourceKeys.length > 0;
	if (hasFrameSync) {
		nodes.push({
			id: 'frame-sync',
			label: 'Frame Sync',
			category: 'frame-sync',
			column: 'processing',
			row: processingRow++,
			kpis: {
				sources: String(sourceKeys.length)
			},
			health: 'healthy'
		});

		// Edges: each decode → frame-sync
		sourceKeys.forEach((key) => {
			edges.push({ from: `decode-${key}`, to: 'frame-sync' });
		});
	}

	// Pipeline nodes (only those present in perf.pipeline.nodes)
	const perfNodes: Record<string, any> = perf.pipeline?.nodes ?? {};
	const activePipelineNodes = pipelineNodeOrder.filter((name) => name in perfNodes);
	let prevNodeId = hasFrameSync ? 'frame-sync' : '';

	activePipelineNodes.forEach((name, idx) => {
		const nodeData = perfNodes[name];
		const lastNs = nodeData?.current?.last_ns ?? 0;
		const nodeId = `pipeline-${name}`;

		nodes.push({
			id: nodeId,
			label: pipelineNodeLabel(name),
			category: 'pipeline-node',
			column: 'processing',
			row: processingRow++,
			kpis: {
				latency: nsToMs(lastNs)
			},
			health: pipelineNodeHealth(lastNs, frameBudgetNs),
			detail: nodeData
		});

		// Edge from previous node (or frame-sync)
		if (prevNodeId) {
			const edge: GraphEdge = { from: prevNodeId, to: nodeId };

			// videoProcCh buffer indicator on the edge from frame-sync to first pipeline node
			if (idx === 0 && prevNodeId === 'frame-sync') {
				const queueLen = perf.pipeline?.current?.queue_len ?? 0;
				const capacity = 8;
				const buf: BufferIndicator = {
					name: 'videoProcCh',
					fill: queueLen,
					capacity,
					health: bufferHealth(queueLen, capacity)
				};
				edge.buffer = buf;
			}

			edges.push(edge);
		}

		prevNodeId = nodeId;
	});

	// Audio mixer node (parallel lane)
	const audioRow = processingRow++;
	const audioMode = perf.audio?.mode ?? 'unknown';
	const mixCycleNs = perf.audio?.mix_cycle?.current?.last_ns ?? 0;
	const audioDecodeErrors = perf.audio?.counters?.decode_errors ?? 0;
	const audioEncodeErrors = perf.audio?.counters?.encode_errors ?? 0;
	const momentaryLUFS = perf.audio?.loudness?.momentary_lufs ?? -Infinity;

	const audioKpis: Record<string, string> = {
		mode: audioMode,
		latency: nsToMs(mixCycleNs)
	};
	if (isFinite(momentaryLUFS)) {
		audioKpis.lufs = momentaryLUFS.toFixed(1);
	}

	nodes.push({
		id: 'audio-mixer',
		label: 'Audio Mixer',
		category: 'audio-mixer',
		column: 'processing',
		row: audioRow,
		kpis: audioKpis,
		health: audioMixerHealth(audioMode, mixCycleNs, audioDecodeErrors, audioEncodeErrors),
		detail: perf.audio
	});

	/* ---------- Column 4: Output ---------- */

	let outputRow = 0;

	// Program relay (always present)
	const outputFPS = perf.broadcast?.output_fps ?? 0;
	const broadcastGapNs = perf.broadcast?.gap?.current?.max_ns ?? 0;

	// Relay health: low FPS or high gap is degraded
	let relayHealth: HealthStatus = 'healthy';
	if (outputFPS > 0 && outputFPS < 20) relayHealth = 'degraded';
	const gapMs = broadcastGapNs / 1_000_000;
	if (gapMs > 100) relayHealth = 'degraded';
	if (gapMs > 500) relayHealth = 'error';

	nodes.push({
		id: 'program-relay',
		label: 'Program Relay',
		category: 'program-relay',
		column: 'output',
		row: outputRow++,
		kpis: {
			fps: outputFPS.toFixed(1),
			gap: nsToMs(broadcastGapNs),
			sent: String(perf.output?.viewer?.video_sent ?? 0)
		},
		health: relayHealth,
		detail: { broadcast: perf.broadcast, output: perf.output }
	});

	// Edge: last pipeline node → relay
	if (prevNodeId) {
		edges.push({ from: prevNodeId, to: 'program-relay' });
	}

	// Edge: audio-mixer → relay
	edges.push({ from: 'audio-mixer', to: 'program-relay' });

	// Preview encode nodes (one per entry in perf.preview)
	const preview: Record<string, any> = perf.preview ?? {};
	const previewKeys = Object.keys(preview).sort();
	previewKeys.forEach((key) => {
		const ps = preview[key];
		const lastEncMs = ps.last_encode_ms ?? 0;
		const framesDropped = ps.frames_dropped ?? 0;

		nodes.push({
			id: `preview-${key}`,
			label: `Preview ${sourceLabel(key)}`,
			category: 'preview-encode',
			column: 'output',
			row: outputRow++,
			kpis: {
				encode: lastEncMs.toFixed(1) + 'ms',
				dropped: String(framesDropped)
			},
			health: previewEncodeHealth(lastEncMs, framesDropped)
		});

		// Edge: decode → preview (not relay)
		edges.push({ from: `decode-${key}`, to: `preview-${key}` });
	});

	// Recording node (only if active)
	if (perf.output?.recording?.active) {
		nodes.push({
			id: 'recording',
			label: 'Recording',
			category: 'recording',
			column: 'output',
			row: outputRow++,
			kpis: {},
			health: 'healthy'
		});
		edges.push({ from: 'program-relay', to: 'recording' });
	}

	// SRT output node (only if bytes_written > 0 or overflow > 0)
	const srtBytesWritten = perf.output?.srt?.bytes_written ?? 0;
	const srtOverflow = perf.output?.srt?.overflow_count ?? 0;
	if (srtBytesWritten > 0 || srtOverflow > 0) {
		nodes.push({
			id: 'srt-output',
			label: 'SRT Output',
			category: 'srt-output',
			column: 'output',
			row: outputRow++,
			kpis: {
				overflow: String(srtOverflow)
			},
			health: srtOutputHealth(srtOverflow)
		});
		edges.push({ from: 'program-relay', to: 'srt-output' });
	}

	/* ---------- Column 5: Browser ---------- */

	// browserDiag is Record<string, SourceDiagnostics> directly (NOT {sources: ...})
	// Each SourceDiagnostics has: renderer, videoDecoder, audio, transport
	if (browserDiag && Object.keys(browserDiag).length > 0) {
		let browserRow = 0;
		const browserSourceKeys = Object.keys(browserDiag).sort();

		// Browser transport node
		nodes.push({
			id: 'browser-transport',
			label: 'WebTransport',
			category: 'browser-transport',
			column: 'browser',
			row: browserRow++,
			kpis: {
				sources: String(browserSourceKeys.length)
			},
			health: 'healthy'
		});
		edges.push({ from: 'program-relay', to: 'browser-transport' });

		// Per-source browser decode nodes
		browserSourceKeys.forEach((key) => {
			const diag = browserDiag[key];
			// decode errors are nested inside videoDecoder diagnostics
			const decodeErrors = (diag?.videoDecoder as any)?.decodeErrorCount
				?? (diag?.videoDecoder as any)?.decodeErrors
				?? (diag?.videoDecoder as any)?.errors
				?? 0;
			const framesDecoded = (diag?.videoDecoder as any)?.framesDecoded
				?? (diag?.videoDecoder as any)?.decodedFrames
				?? 0;

			const kpis: Record<string, string> = {};
			if (framesDecoded > 0) kpis.decoded = String(framesDecoded);
			if (decodeErrors > 0) kpis.errors = String(decodeErrors);
			if (Object.keys(kpis).length === 0) kpis.status = 'active';

			nodes.push({
				id: `browser-decode-${key}`,
				label: sourceLabel(key),
				category: 'browser-decode',
				column: 'browser',
				row: browserRow++,
				kpis,
				health: browserDecodeHealth(decodeErrors),
				detail: diag
			});
			edges.push({ from: 'browser-transport', to: `browser-decode-${key}` });
		});

		// Browser render node
		nodes.push({
			id: 'browser-render',
			label: 'Render',
			category: 'browser-render',
			column: 'browser',
			row: browserRow++,
			kpis: {
				sources: String(browserSourceKeys.length)
			},
			health: 'healthy'
		});

		browserSourceKeys.forEach((key) => {
			edges.push({ from: `browser-decode-${key}`, to: 'browser-render' });
		});
	}

	return { nodes, edges };
}
