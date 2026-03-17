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

/** Determine source type badge from source key prefix. */
export function sourceType(key: string): string {
	if (key.startsWith('srt:')) return 'SRT';
	if (key.startsWith('mxl:')) return 'MXL';
	if (key.startsWith('clip-player-')) return 'Clip';
	if (key.startsWith('demo:')) return 'Demo';
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
		const kpis: Record<string, string> = {
			fps: (src.decode?.current?.avg_fps ?? 0).toFixed(1)
		};

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

		nodes.push({
			id: `source-${key}`,
			label: sourceLabel(key),
			category: 'source',
			column: 'ingest',
			row,
			badge: sourceType(key),
			kpis,
			health
		});
	});

	/* ---------- Column 2: Decode ---------- */

	sourceKeys.forEach((key, row) => {
		const src = sources[key];
		const lastNs = src.decode?.current?.last_ns ?? 0;
		const drops = src.decode?.current?.drops ?? 0;

		const kpis: Record<string, string> = {
			latency: nsToMs(lastNs),
			drops: String(drops)
		};

		nodes.push({
			id: `decode-${key}`,
			label: 'Decode',
			category: 'decode',
			column: 'decode',
			row,
			kpis,
			health: decodeHealth(lastNs, drops)
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
			kpis: {},
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
			health: pipelineNodeHealth(lastNs, frameBudgetNs)
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
		health: audioMixerHealth(audioMode, mixCycleNs, audioDecodeErrors, audioEncodeErrors)
	});

	/* ---------- Column 4: Output ---------- */

	let outputRow = 0;

	// Program relay (always present)
	const outputFPS = perf.broadcast?.output_fps ?? 0;
	const broadcastGapNs = perf.broadcast?.gap?.current?.max_ns ?? 0;

	nodes.push({
		id: 'program-relay',
		label: 'Program Relay',
		category: 'program-relay',
		column: 'output',
		row: outputRow++,
		kpis: {
			fps: outputFPS.toFixed(1),
			gap: nsToMs(broadcastGapNs)
		},
		health: 'healthy'
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

	if (browserDiag) {
		let browserRow = 0;

		// Browser transport node
		nodes.push({
			id: 'browser-transport',
			label: 'WebTransport',
			category: 'browser-transport',
			column: 'browser',
			row: browserRow++,
			kpis: {},
			health: 'healthy'
		});
		edges.push({ from: 'program-relay', to: 'browser-transport' });

		// Per-source browser decode nodes
		const browserSources: Record<string, any> = browserDiag.sources ?? {};
		const browserSourceKeys = Object.keys(browserSources).sort();

		browserSourceKeys.forEach((key) => {
			const bsrc = browserSources[key];
			const decodeErrors = bsrc?.decode_errors ?? 0;

			nodes.push({
				id: `browser-decode-${key}`,
				label: `Decode ${sourceLabel(key)}`,
				category: 'browser-decode',
				column: 'browser',
				row: browserRow++,
				kpis: {
					errors: String(decodeErrors)
				},
				health: browserDecodeHealth(decodeErrors)
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
			kpis: {},
			health: 'healthy'
		});

		browserSourceKeys.forEach((key) => {
			edges.push({ from: `browser-decode-${key}`, to: 'browser-render' });
		});
	}

	return { nodes, edges };
}
