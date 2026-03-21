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
	'h264-encode': 'H.264 Encode',
	// GPU nodes
	'gpu_key': 'GPU Key',
	'gpu_layout': 'GPU Layout',
	'gpu_dsk': 'GPU DSK',
	'gpu_stmap': 'GPU ST Map',
	'gpu_raw_sink': 'GPU Raw Sink',
	'gpu_encode': 'GPU Encode'
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

/** Canonical GPU pipeline node ordering. */
const gpuPipelineNodeOrder = [
	'gpu_key',
	'gpu_layout',
	'gpu_dsk',
	'gpu_stmap',
	'gpu_raw_sink',
	'gpu_encode'
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
	const outputFPS: number = perf.broadcast?.output_fps ?? 0;

	/* ---------- Column 1: Ingest ---------- */

	sourceKeys.forEach((key, row) => {
		const src = sources[key];
		const avgFps = src.decode?.current?.avg_fps ?? 0;
		const ingestFps = src.decode?.current?.ingest_fps ?? 0;
		const kpis: Record<string, string> = {};

		// Show FPS: prefer ingest_fps (works for all source types), fall back to decode avg_fps
		const displayFps = ingestFps > 0 ? ingestFps : avgFps;
		if (displayFps > 0) {
			kpis.fps = displayFps.toFixed(1);
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
		const frameSyncKpis: Record<string, string> = {
			sources: String(sourceKeys.length)
		};
		// Show frame sync FPS and release stats if available
		if (perf.frame_sync?.release_fps) {
			frameSyncKpis.fps = perf.frame_sync.release_fps.toFixed(1);
		} else if (outputFPS > 0) {
			// Fall back to pipeline output FPS as a proxy
			frameSyncKpis.fps = outputFPS.toFixed(1);
		}

		nodes.push({
			id: 'frame-sync',
			label: 'Frame Sync',
			category: 'frame-sync',
			column: 'processing',
			row: processingRow++,
			kpis: frameSyncKpis,
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
		let lastNs = nodeData?.current?.last_ns ?? 0;
		const nodeId = `pipeline-${name}`;

		const kpis: Record<string, string> = {};

		// For the encode node, show the real async encode latency instead of the near-zero enqueue time
		// The async encode latency is in encode_last_ns (from AsyncMetricsProvider)
		if (name === 'h264-encode' && nodeData?.encode_last_ns) {
			lastNs = nodeData.encode_last_ns;
			kpis.latency = nsToMs(lastNs);
			if (nodeData.encode_total) kpis.frames = String(nodeData.encode_total);
		} else {
			kpis.latency = nsToMs(lastNs);
		}

		// Show pipeline output FPS on the encode node since it's the last processing step
		if (name === 'h264-encode' && outputFPS > 0) {
			kpis.fps = outputFPS.toFixed(1);
		}

		nodes.push({
			id: nodeId,
			label: pipelineNodeLabel(name),
			category: 'pipeline-node',
			column: 'processing',
			row: processingRow++,
			kpis,
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

	// GPU pipeline nodes (when GPU pipeline is active)
	const gpuData = perf.pipeline?.gpu;
	if (gpuData?.active && gpuData.nodes) {
		const gpuNodeNames = Object.keys(gpuData.nodes);
		// Sort by canonical order, then append any unknown GPU nodes
		const orderedGpuNodes = gpuPipelineNodeOrder.filter((name) => gpuNodeNames.includes(name));
		for (const name of gpuNodeNames) {
			if (!orderedGpuNodes.includes(name)) orderedGpuNodes.push(name);
		}

		let gpuPrevNodeId = prevNodeId;
		orderedGpuNodes.forEach((name) => {
			const nodeData = gpuData.nodes![name];
			const lastNs = nodeData?.current?.last_ns ?? 0;
			const nodeId = `gpu-${name}`;

			const kpis: Record<string, string> = {
				latency: nsToMs(lastNs)
			};

			nodes.push({
				id: nodeId,
				label: pipelineNodeLabel(name),
				category: 'gpu-pipeline-node',
				column: 'processing',
				row: processingRow++,
				badge: gpuData.backend?.toUpperCase(),
				kpis,
				health: pipelineNodeHealth(lastNs, frameBudgetNs),
				detail: nodeData
			});

			if (gpuPrevNodeId) {
				edges.push({ from: gpuPrevNodeId, to: nodeId });
			}
			gpuPrevNodeId = nodeId;
		});

		// GPU output connects to relay instead of CPU output
		if (gpuPrevNodeId && gpuPrevNodeId !== prevNodeId) {
			prevNodeId = gpuPrevNodeId;
		}
	}

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

		const prevKpis: Record<string, string> = {
			latency: lastEncMs.toFixed(1) + 'ms'
		};
		if (framesDropped > 0) prevKpis.dropped = String(framesDropped);

		nodes.push({
			id: `preview-${key}`,
			label: `Preview ${sourceLabel(key)}`,
			category: 'preview-encode',
			column: 'output',
			row: outputRow++,
			kpis: prevKpis,
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

	/* ---------- Column 5: Transport ---------- */
	/* ---------- Column 6: Browser Decode/Render ---------- */

	// browserDiag is Record<string, SourceDiagnostics> directly (NOT {sources: ...})
	// Each SourceDiagnostics has: videoDecoder (VideoDecoderDiagnostics), renderer (RendererDiagnostics), audio, transport
	if (browserDiag && Object.keys(browserDiag).length > 0) {
		const browserSourceKeys = Object.keys(browserDiag).sort();

		// Transport node (its own column, between output and browser)
		nodes.push({
			id: 'browser-transport',
			label: 'WebTransport',
			category: 'browser-transport',
			column: 'transport',
			row: 0,
			kpis: {
				streams: String(browserSourceKeys.length)
			},
			health: 'healthy'
		});
		edges.push({ from: 'program-relay', to: 'browser-transport' });

		// Per-source browser decode nodes (browser column)
		let browserRow = 0;
		let totalFramesDrawn = 0;
		let totalFramesSkipped = 0;

		browserSourceKeys.forEach((key) => {
			const diag = browserDiag[key];
			const vd = diag?.videoDecoder as any;
			const rd = diag?.renderer as any;

			// VideoDecoderDiagnostics fields (camelCase):
			// inputCount, outputCount, decodeErrors, inputFps, outputFps, decodeQueueSize, discardedDelta, bufferDropped
			const decodeErrors = vd?.decodeErrors ?? 0;
			const outputFps = vd?.outputFps ?? 0;
			const discarded = (vd?.discardedDelta ?? 0) + (vd?.discardedBufferFull ?? 0) + (vd?.bufferDropped ?? 0);

			// Renderer stats
			const framesDrawn = rd?.framesDrawn ?? 0;
			const framesSkipped = rd?.framesSkipped ?? 0;
			const renderFps = framesDrawn > 0 && rd?.avgFrameIntervalMs > 0
				? (1000 / rd.avgFrameIntervalMs) : 0;

			const kpis: Record<string, string> = {};
			// Always show FPS if available
			if (outputFps > 0) kpis.fps = outputFps.toFixed(1);
			if (decodeErrors > 0) kpis.errors = String(decodeErrors);
			if (discarded > 0) kpis.dropped = String(discarded);
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

			// Accumulate render stats
			totalFramesDrawn += framesDrawn;
			totalFramesSkipped += framesSkipped;
		});

		// Browser render node (aggregate, same column)
		const renderKpis: Record<string, string> = {};
		if (totalFramesDrawn > 0) renderKpis.drawn = String(totalFramesDrawn);
		if (totalFramesSkipped > 0) renderKpis.skipped = String(totalFramesSkipped);
		if (Object.keys(renderKpis).length === 0) renderKpis.sources = String(browserSourceKeys.length);

		nodes.push({
			id: 'browser-render',
			label: 'Render',
			category: 'browser-render',
			column: 'browser',
			row: browserRow++,
			kpis: renderKpis,
			health: totalFramesSkipped > 100 ? 'degraded' : 'healthy'
		});

		browserSourceKeys.forEach((key) => {
			edges.push({ from: `browser-decode-${key}`, to: 'browser-render' });
		});
	}

	return { nodes, edges };
}
