import { describe, it, expect } from 'vitest';
import { buildGraph, sourceType, sourceLabel, pipelineNodeLabel } from './builder';

/**
 * Minimal perf fixture matching the /api/perf response shape.
 * Includes 2 sources (one demo, one SRT), 3 pipeline nodes,
 * audio, broadcast, output, and one preview encoder.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function minimalPerf(): any {
	return {
		timestamp: '2026-03-17T12:00:00Z',
		uptime_ms: 60000,
		frame_budget_ns: 33_333_333,
		sources: {
			'demo:cam1': {
				health: 'healthy',
				decode: {
					current: { last_ns: 2_000_000, drops: 0, avg_fps: 29.97, avg_frame_bytes: 50000 },
					windows: {
						'1s': { min_ns: 1_800_000, max_ns: 2_200_000, mean_ns: 2_000_000, p95_ns: 2_100_000 },
						'10s': { min_ns: 1_500_000, max_ns: 2_500_000, mean_ns: 2_000_000, p95_ns: 2_300_000 },
						'60s': { min_ns: 1_000_000, max_ns: 3_000_000, mean_ns: 2_000_000, p95_ns: 2_500_000 }
					}
				}
			},
			'srt:camera2': {
				health: 'healthy',
				decode: {
					current: { last_ns: 3_000_000, drops: 1, avg_fps: 30.0, avg_frame_bytes: 60000 },
					windows: {
						'1s': { min_ns: 2_500_000, max_ns: 3_500_000, mean_ns: 3_000_000, p95_ns: 3_200_000 },
						'10s': { min_ns: 2_000_000, max_ns: 4_000_000, mean_ns: 3_000_000, p95_ns: 3_500_000 },
						'60s': { min_ns: 1_500_000, max_ns: 5_000_000, mean_ns: 3_000_000, p95_ns: 4_000_000 }
					}
				},
				srt: {
					rtt_ms: 12.5,
					loss_rate_pct: 0.3,
					recv_buf_ms: 45.0
				}
			}
		},
		pipeline: {
			current: { last_ns: 8_000_000, queue_len: 2 },
			windows: {
				'1s': { min_ns: 7_000_000, max_ns: 9_000_000, mean_ns: 8_000_000, p95_ns: 8_500_000 },
				'10s': { min_ns: 6_000_000, max_ns: 10_000_000, mean_ns: 8_000_000, p95_ns: 9_000_000 },
				'60s': { min_ns: 5_000_000, max_ns: 12_000_000, mean_ns: 8_000_000, p95_ns: 10_000_000 }
			},
			nodes: {
				'upstream-key': {
					current: { last_ns: 500_000 },
					windows: {
						'1s': { min_ns: 400_000, max_ns: 600_000, mean_ns: 500_000, p95_ns: 550_000 },
						'10s': { min_ns: 300_000, max_ns: 700_000, mean_ns: 500_000, p95_ns: 600_000 },
						'60s': { min_ns: 200_000, max_ns: 800_000, mean_ns: 500_000, p95_ns: 700_000 }
					}
				},
				'dsk-compositor': {
					current: { last_ns: 1_000_000 },
					windows: {
						'1s': { min_ns: 800_000, max_ns: 1_200_000, mean_ns: 1_000_000, p95_ns: 1_100_000 },
						'10s': { min_ns: 700_000, max_ns: 1_300_000, mean_ns: 1_000_000, p95_ns: 1_200_000 },
						'60s': { min_ns: 600_000, max_ns: 1_500_000, mean_ns: 1_000_000, p95_ns: 1_300_000 }
					}
				},
				'h264-encode': {
					current: { last_ns: 5_000_000 },
					windows: {
						'1s': { min_ns: 4_000_000, max_ns: 6_000_000, mean_ns: 5_000_000, p95_ns: 5_500_000 },
						'10s': { min_ns: 3_000_000, max_ns: 7_000_000, mean_ns: 5_000_000, p95_ns: 6_000_000 },
						'60s': { min_ns: 2_000_000, max_ns: 8_000_000, mean_ns: 5_000_000, p95_ns: 7_000_000 }
					}
				}
			},
			deadline_violations: 0,
			budget_pct: 30.0
		},
		e2e: {
			current: { last_ns: 15_000_000 },
			windows: {
				'1s': { min_ns: 12_000_000, max_ns: 18_000_000, mean_ns: 15_000_000, p95_ns: 17_000_000 },
				'10s': { min_ns: 10_000_000, max_ns: 20_000_000, mean_ns: 15_000_000, p95_ns: 18_000_000 },
				'60s': { min_ns: 8_000_000, max_ns: 25_000_000, mean_ns: 15_000_000, p95_ns: 20_000_000 }
			}
		},
		audio: {
			mode: 'mixing',
			mix_cycle: {
				current: { last_ns: 800_000 },
				windows: {
					'1s': { min_ns: 600_000, max_ns: 1_000_000, mean_ns: 800_000, p95_ns: 900_000 },
					'10s': { min_ns: 500_000, max_ns: 1_200_000, mean_ns: 800_000, p95_ns: 1_000_000 },
					'60s': { min_ns: 400_000, max_ns: 1_500_000, mean_ns: 800_000, p95_ns: 1_200_000 }
				}
			},
			counters: {
				output: 1800,
				passthrough: 0,
				mixed: 1800,
				decode_errors: 0,
				encode_errors: 0
			},
			loudness: {
				momentary_lufs: -18.5,
				short_term_lufs: -20.0,
				integrated_lufs: -22.0
			}
		},
		broadcast: {
			frames: 1800,
			output_fps: 29.97,
			gap: {
				current: { max_ns: 34_000_000 },
				windows: {
					'1s': { min_ns: 33_000_000, max_ns: 35_000_000, mean_ns: 33_500_000, p95_ns: 34_500_000 },
					'10s': { min_ns: 32_000_000, max_ns: 36_000_000, mean_ns: 33_500_000, p95_ns: 35_000_000 },
					'60s': { min_ns: 30_000_000, max_ns: 40_000_000, mean_ns: 33_500_000, p95_ns: 36_000_000 }
				}
			}
		},
		output: {
			viewer: { video_sent: 1800, video_dropped: 0, audio_dropped: 0 },
			muxer_pts: 5400000,
			srt: { bytes_written: 0, overflow_count: 0 },
			recording: { active: false }
		}
	};
}

/* ---------- Helper tests ---------- */

describe('sourceType', () => {
	it('returns SRT for srt: prefix', () => {
		expect(sourceType('srt:camera1')).toBe('SRT');
	});

	it('returns MXL for mxl: prefix', () => {
		expect(sourceType('mxl:flow1')).toBe('MXL');
	});

	it('returns Clip for clip-player- prefix', () => {
		expect(sourceType('clip-player-0')).toBe('Clip');
	});

	it('returns Demo for demo: prefix', () => {
		expect(sourceType('demo:cam1')).toBe('Demo');
	});

	it('returns Source for unknown prefix', () => {
		expect(sourceType('camera1')).toBe('Source');
	});
});

describe('sourceLabel', () => {
	it('strips srt: prefix', () => {
		expect(sourceLabel('srt:camera1')).toBe('camera1');
	});

	it('strips demo: prefix', () => {
		expect(sourceLabel('demo:cam1')).toBe('cam1');
	});

	it('strips mxl: prefix', () => {
		expect(sourceLabel('mxl:flow1')).toBe('flow1');
	});

	it('strips clip-player- prefix', () => {
		expect(sourceLabel('clip-player-0')).toBe('0');
	});

	it('returns as-is for no prefix', () => {
		expect(sourceLabel('camera1')).toBe('camera1');
	});
});

describe('pipelineNodeLabel', () => {
	it('maps upstream-key', () => {
		expect(pipelineNodeLabel('upstream-key')).toBe('Upstream Key');
	});

	it('maps h264-encode', () => {
		expect(pipelineNodeLabel('h264-encode')).toBe('H.264 Encode');
	});

	it('returns raw name for unknown node', () => {
		expect(pipelineNodeLabel('custom-node')).toBe('custom-node');
	});
});

/* ---------- buildGraph tests ---------- */

describe('buildGraph', () => {
	describe('source + decode nodes', () => {
		it('creates a source and decode node for each source', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const sourceNodes = graph.nodes.filter((n) => n.category === 'source');
			const decodeNodes = graph.nodes.filter((n) => n.category === 'decode');

			expect(sourceNodes).toHaveLength(2);
			expect(decodeNodes).toHaveLength(2);

			// Check IDs
			expect(sourceNodes.map((n) => n.id).sort()).toEqual(['source-demo:cam1', 'source-srt:camera2']);
			expect(decodeNodes.map((n) => n.id).sort()).toEqual(['decode-demo:cam1', 'decode-srt:camera2']);
		});

		it('assigns correct columns', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const sourceNodes = graph.nodes.filter((n) => n.category === 'source');
			const decodeNodes = graph.nodes.filter((n) => n.category === 'decode');

			sourceNodes.forEach((n) => expect(n.column).toBe('ingest'));
			decodeNodes.forEach((n) => expect(n.column).toBe('decode'));
		});

		it('aligns source and decode rows', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const keys = Object.keys(perf.sources).sort();
			keys.forEach((key, row) => {
				const srcNode = graph.nodes.find((n) => n.id === `source-${key}`);
				const decNode = graph.nodes.find((n) => n.id === `decode-${key}`);
				expect(srcNode?.row).toBe(row);
				expect(decNode?.row).toBe(row);
			});
		});
	});

	describe('edges from source to decode', () => {
		it('creates edges from source to decode for each source', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const keys = Object.keys(perf.sources).sort();
			keys.forEach((key) => {
				const edge = graph.edges.find((e) => e.from === `source-${key}` && e.to === `decode-${key}`);
				expect(edge).toBeDefined();
			});
		});
	});

	describe('fan-in edges from decode to frame-sync', () => {
		it('creates edges from each decode to frame-sync', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const frameSyncNode = graph.nodes.find((n) => n.id === 'frame-sync');
			expect(frameSyncNode).toBeDefined();
			expect(frameSyncNode?.category).toBe('frame-sync');

			const keys = Object.keys(perf.sources).sort();
			keys.forEach((key) => {
				const edge = graph.edges.find((e) => e.from === `decode-${key}` && e.to === 'frame-sync');
				expect(edge).toBeDefined();
			});
		});
	});

	describe('pipeline nodes', () => {
		it('creates only active pipeline nodes (not missing ones)', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const pipelineNodes = graph.nodes.filter((n) => n.category === 'pipeline-node');
			expect(pipelineNodes).toHaveLength(3); // upstream-key, dsk-compositor, h264-encode

			const ids = pipelineNodes.map((n) => n.id);
			expect(ids).toContain('pipeline-upstream-key');
			expect(ids).toContain('pipeline-dsk-compositor');
			expect(ids).toContain('pipeline-h264-encode');

			// layout-compositor, raw-sink, raw-monitor-sink are absent from the fixture
			expect(ids).not.toContain('pipeline-layout-compositor');
			expect(ids).not.toContain('pipeline-raw-sink');
			expect(ids).not.toContain('pipeline-raw-monitor-sink');
		});

		it('chains pipeline nodes in order', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			// The chain should be: frame-sync -> upstream-key -> dsk-compositor -> h264-encode
			expect(graph.edges.find((e) => e.from === 'frame-sync' && e.to === 'pipeline-upstream-key')).toBeDefined();
			expect(
				graph.edges.find(
					(e) => e.from === 'pipeline-upstream-key' && e.to === 'pipeline-dsk-compositor'
				)
			).toBeDefined();
			expect(
				graph.edges.find(
					(e) => e.from === 'pipeline-dsk-compositor' && e.to === 'pipeline-h264-encode'
				)
			).toBeDefined();
		});

		it('shows pipeline node latency in KPIs', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const encodeNode = graph.nodes.find((n) => n.id === 'pipeline-h264-encode');
			expect(encodeNode?.kpis.latency).toBe('5.0ms');
		});
	});

	describe('audio mixer', () => {
		it('creates audio mixer node with correct KPIs', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const audioNode = graph.nodes.find((n) => n.id === 'audio-mixer');
			expect(audioNode).toBeDefined();
			expect(audioNode?.category).toBe('audio-mixer');
			expect(audioNode?.column).toBe('processing');
			expect(audioNode?.kpis.mode).toBe('mixing');
			expect(audioNode?.kpis.latency).toBe('0.8ms');
			expect(audioNode?.kpis.lufs).toBe('-18.5');
		});

		it('connects audio mixer to program relay', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const edge = graph.edges.find((e) => e.from === 'audio-mixer' && e.to === 'program-relay');
			expect(edge).toBeDefined();
		});
	});

	describe('program relay', () => {
		it('creates program relay with output FPS', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const relayNode = graph.nodes.find((n) => n.id === 'program-relay');
			expect(relayNode).toBeDefined();
			expect(relayNode?.category).toBe('program-relay');
			expect(relayNode?.column).toBe('output');
			expect(relayNode?.kpis.fps).toBe('30.0');
		});

		it('connects last pipeline node to relay', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const edge = graph.edges.find(
				(e) => e.from === 'pipeline-h264-encode' && e.to === 'program-relay'
			);
			expect(edge).toBeDefined();
		});
	});

	describe('recording', () => {
		it('omits recording when not active', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const recordingNode = graph.nodes.find((n) => n.id === 'recording');
			expect(recordingNode).toBeUndefined();
		});

		it('includes recording when active', () => {
			const perf = minimalPerf();
			perf.output.recording.active = true;
			const graph = buildGraph(perf);

			const recordingNode = graph.nodes.find((n) => n.id === 'recording');
			expect(recordingNode).toBeDefined();
			expect(recordingNode?.category).toBe('recording');
			expect(recordingNode?.column).toBe('output');

			const edge = graph.edges.find((e) => e.from === 'program-relay' && e.to === 'recording');
			expect(edge).toBeDefined();
		});
	});

	describe('SRT output', () => {
		it('omits SRT output when no bytes written and no overflow', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const srtNode = graph.nodes.find((n) => n.id === 'srt-output');
			expect(srtNode).toBeUndefined();
		});

		it('includes SRT output when bytes_written > 0', () => {
			const perf = minimalPerf();
			perf.output.srt.bytes_written = 1000000;
			const graph = buildGraph(perf);

			const srtNode = graph.nodes.find((n) => n.id === 'srt-output');
			expect(srtNode).toBeDefined();
			expect(srtNode?.category).toBe('srt-output');

			const edge = graph.edges.find((e) => e.from === 'program-relay' && e.to === 'srt-output');
			expect(edge).toBeDefined();
		});

		it('includes SRT output when overflow > 0', () => {
			const perf = minimalPerf();
			perf.output.srt.overflow_count = 3;
			const graph = buildGraph(perf);

			const srtNode = graph.nodes.find((n) => n.id === 'srt-output');
			expect(srtNode).toBeDefined();
			expect(srtNode?.health).toBe('error');
		});
	});

	describe('badges', () => {
		it('applies correct badges to source nodes', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const demoCam = graph.nodes.find((n) => n.id === 'source-demo:cam1');
			expect(demoCam?.badge).toBe('Demo');

			const srtCam = graph.nodes.find((n) => n.id === 'source-srt:camera2');
			expect(srtCam?.badge).toBe('SRT');
		});
	});

	describe('SRT source KPIs', () => {
		it('includes SRT KPIs on SRT sources', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const srtNode = graph.nodes.find((n) => n.id === 'source-srt:camera2');
			expect(srtNode?.kpis.rtt).toBe('12.5ms');
			expect(srtNode?.kpis.loss).toBe('0.30%');
		});

		it('does not include SRT KPIs on non-SRT sources', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const demoNode = graph.nodes.find((n) => n.id === 'source-demo:cam1');
			expect(demoNode?.kpis.rtt).toBeUndefined();
			expect(demoNode?.kpis.loss).toBeUndefined();
		});
	});

	describe('videoProcCh buffer indicator', () => {
		it('includes buffer indicator on frame-sync to first pipeline node edge', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const edge = graph.edges.find(
				(e) => e.from === 'frame-sync' && e.to === 'pipeline-upstream-key'
			);
			expect(edge?.buffer).toBeDefined();
			expect(edge?.buffer?.name).toBe('videoProcCh');
			expect(edge?.buffer?.capacity).toBe(8);
			expect(edge?.buffer?.fill).toBe(2);
			expect(edge?.buffer?.health).toBe('healthy');
		});

		it('reports degraded health when buffer is half full', () => {
			const perf = minimalPerf();
			perf.pipeline.current.queue_len = 5; // 5/8 = 62.5% > 50%
			const graph = buildGraph(perf);

			const edge = graph.edges.find(
				(e) => e.from === 'frame-sync' && e.to === 'pipeline-upstream-key'
			);
			expect(edge?.buffer?.health).toBe('degraded');
		});

		it('reports error health when buffer is nearly full', () => {
			const perf = minimalPerf();
			perf.pipeline.current.queue_len = 7; // 7/8 = 87.5% > 80%
			const graph = buildGraph(perf);

			const edge = graph.edges.find(
				(e) => e.from === 'frame-sync' && e.to === 'pipeline-upstream-key'
			);
			expect(edge?.buffer?.health).toBe('error');
		});
	});

	describe('preview encoder nodes', () => {
		it('includes preview encoder nodes when present', () => {
			const perf = minimalPerf();
			perf.preview = {
				'demo:cam1': {
					frames_in: 900,
					frames_out: 895,
					frames_dropped: 5,
					last_encode_ms: 3.2,
					avg_encode_ms: 2.8
				}
			};
			const graph = buildGraph(perf);

			const previewNode = graph.nodes.find((n) => n.id === 'preview-demo:cam1');
			expect(previewNode).toBeDefined();
			expect(previewNode?.category).toBe('preview-encode');
			expect(previewNode?.column).toBe('output');
			expect(previewNode?.kpis.encode).toBe('3.2ms');
			expect(previewNode?.kpis.dropped).toBe('5');

			// Edge from decode -> preview
			const edge = graph.edges.find(
				(e) => e.from === 'decode-demo:cam1' && e.to === 'preview-demo:cam1'
			);
			expect(edge).toBeDefined();
		});

		it('does not include preview nodes when perf.preview is absent', () => {
			const perf = minimalPerf();
			// preview is not set in minimalPerf
			const graph = buildGraph(perf);

			const previewNodes = graph.nodes.filter((n) => n.category === 'preview-encode');
			expect(previewNodes).toHaveLength(0);
		});
	});

	describe('empty sources', () => {
		it('handles empty sources gracefully', () => {
			const perf = minimalPerf();
			perf.sources = {};
			const graph = buildGraph(perf);

			const sourceNodes = graph.nodes.filter((n) => n.category === 'source');
			const decodeNodes = graph.nodes.filter((n) => n.category === 'decode');
			const frameSyncNode = graph.nodes.find((n) => n.id === 'frame-sync');

			expect(sourceNodes).toHaveLength(0);
			expect(decodeNodes).toHaveLength(0);
			expect(frameSyncNode).toBeUndefined();

			// Should still have pipeline nodes, audio mixer, program relay
			expect(graph.nodes.find((n) => n.id === 'audio-mixer')).toBeDefined();
			expect(graph.nodes.find((n) => n.id === 'program-relay')).toBeDefined();
		});
	});

	describe('missing pipeline nodes', () => {
		it('handles missing pipeline nodes gracefully', () => {
			const perf = minimalPerf();
			perf.pipeline.nodes = {};
			const graph = buildGraph(perf);

			const pipelineNodes = graph.nodes.filter((n) => n.category === 'pipeline-node');
			expect(pipelineNodes).toHaveLength(0);

			// frame-sync should still connect to nothing in pipeline, but relay exists
			expect(graph.nodes.find((n) => n.id === 'program-relay')).toBeDefined();
		});

		it('connects frame-sync directly to relay when no pipeline nodes exist', () => {
			const perf = minimalPerf();
			perf.pipeline.nodes = {};
			const graph = buildGraph(perf);

			// frame-sync should be the prevNodeId, which connects to program-relay
			const edge = graph.edges.find(
				(e) => e.from === 'frame-sync' && e.to === 'program-relay'
			);
			expect(edge).toBeDefined();
		});
	});

	describe('browser nodes', () => {
		it('includes browser nodes when diagnostics provided', () => {
			const perf = minimalPerf();
			// browserDiag is Record<string, SourceDiagnostics> directly (flat map)
			const browserDiag: any = {
				'demo:cam1': { videoDecoder: { decodeErrors: 0 }, renderer: null, audio: null, transport: null },
				'srt:camera2': { videoDecoder: { decodeErrors: 2 }, renderer: null, audio: null, transport: null }
			};
			const graph = buildGraph(perf, browserDiag);

			// WebTransport node
			const transportNode = graph.nodes.find((n) => n.id === 'browser-transport');
			expect(transportNode).toBeDefined();
			expect(transportNode?.category).toBe('browser-transport');
			expect(transportNode?.column).toBe('browser');

			// Per-source browser decode
			const browserDecodes = graph.nodes.filter((n) => n.category === 'browser-decode');
			expect(browserDecodes).toHaveLength(2);

			// Render node
			const renderNode = graph.nodes.find((n) => n.id === 'browser-render');
			expect(renderNode).toBeDefined();
			expect(renderNode?.category).toBe('browser-render');

			// Edges: relay -> transport
			expect(
				graph.edges.find((e) => e.from === 'program-relay' && e.to === 'browser-transport')
			).toBeDefined();

			// Edges: transport -> browser-decode
			expect(
				graph.edges.find(
					(e) => e.from === 'browser-transport' && e.to === 'browser-decode-demo:cam1'
				)
			).toBeDefined();

			// Edges: browser-decode -> render
			expect(
				graph.edges.find(
					(e) => e.from === 'browser-decode-srt:camera2' && e.to === 'browser-render'
				)
			).toBeDefined();
		});

		it('does not include browser nodes when diagnostics is null', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf, null);

			const browserNodes = graph.nodes.filter((n) => n.column === 'browser');
			expect(browserNodes).toHaveLength(0);
		});

		it('shows decode errors for browser decode nodes', () => {
			const perf = minimalPerf();
			const browserDiag: any = {
				'srt:camera2': { videoDecoder: { decodeErrors: 6 }, renderer: null, audio: null, transport: null }
			};
			const graph = buildGraph(perf, browserDiag);

			const browserDecode = graph.nodes.find((n) => n.id === 'browser-decode-srt:camera2');
			expect(browserDecode?.health).toBe('error');
			expect(browserDecode?.kpis.errors).toBe('6');
		});
	});

	describe('null/undefined perf', () => {
		it('returns empty graph for null perf', () => {
			const graph = buildGraph(null);
			expect(graph.nodes).toHaveLength(0);
			expect(graph.edges).toHaveLength(0);
		});

		it('returns empty graph for undefined perf', () => {
			const graph = buildGraph(undefined);
			expect(graph.nodes).toHaveLength(0);
			expect(graph.edges).toHaveLength(0);
		});
	});

	describe('source health', () => {
		it('marks offline source as error', () => {
			const perf = minimalPerf();
			perf.sources['demo:cam1'].health = 'offline';
			const graph = buildGraph(perf);

			const srcNode = graph.nodes.find((n) => n.id === 'source-demo:cam1');
			expect(srcNode?.health).toBe('error');
		});

		it('marks stale source as degraded', () => {
			const perf = minimalPerf();
			perf.sources['demo:cam1'].health = 'stale';
			const graph = buildGraph(perf);

			const srcNode = graph.nodes.find((n) => n.id === 'source-demo:cam1');
			expect(srcNode?.health).toBe('degraded');
		});

		it('marks online source as healthy', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			const srcNode = graph.nodes.find((n) => n.id === 'source-demo:cam1');
			expect(srcNode?.health).toBe('healthy');
		});
	});

	describe('decode health', () => {
		it('marks decode with few cumulative drops as healthy', () => {
			const perf = minimalPerf();
			// srt:camera2 has drops: 1 in the fixture — cumulative, not instant
			const graph = buildGraph(perf);

			const decNode = graph.nodes.find((n) => n.id === 'decode-srt:camera2');
			expect(decNode?.health).toBe('healthy');
		});

		it('marks decode with high latency as degraded', () => {
			const perf = minimalPerf();
			perf.sources['demo:cam1'].decode.current.last_ns = 12_000_000;
			const graph = buildGraph(perf);

			const decNode = graph.nodes.find((n) => n.id === 'decode-demo:cam1');
			expect(decNode?.health).toBe('degraded');
		});
	});

	describe('complete graph structure', () => {
		it('has all expected node counts for the fixture', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			// 2 sources + 2 decodes + 1 frame-sync + 3 pipeline + 1 audio + 1 relay = 10
			expect(graph.nodes).toHaveLength(10);
		});

		it('has all expected edge counts for the fixture', () => {
			const perf = minimalPerf();
			const graph = buildGraph(perf);

			// 2 source->decode + 2 decode->frame-sync + 3 pipeline chain edges
			// + 1 h264-encode->relay + 1 audio->relay = 9
			expect(graph.edges).toHaveLength(9);
		});
	});
});
