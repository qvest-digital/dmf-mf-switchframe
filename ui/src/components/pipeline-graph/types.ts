/** Column positions in the pipeline graph (left to right). */
export type GraphColumn = 'ingest' | 'decode' | 'processing' | 'output' | 'browser';

/** Health status determines node border color. */
export type HealthStatus = 'healthy' | 'degraded' | 'error';

/** Node categories determine which KPIs to show and health thresholds. */
export type NodeCategory =
	| 'source'
	| 'decode'
	| 'frame-sync'
	| 'pipeline-node'
	| 'audio-mixer'
	| 'program-relay'
	| 'preview-encode'
	| 'recording'
	| 'srt-output'
	| 'confidence'
	| 'browser-transport'
	| 'browser-decode'
	| 'browser-render';

export interface GraphNode {
	id: string;
	label: string;
	category: NodeCategory;
	column: GraphColumn;
	row: number;
	badge?: string;
	kpis: Record<string, string>;
	health: HealthStatus;
	detail?: Record<string, unknown>;
}

export interface BufferIndicator {
	name: string;
	fill: number;
	capacity: number;
	health: HealthStatus;
}

export interface GraphEdge {
	from: string;
	to: string;
	buffer?: BufferIndicator;
}

export interface PipelineGraph {
	nodes: GraphNode[];
	edges: GraphEdge[];
}
