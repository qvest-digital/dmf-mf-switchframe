import type { GraphicsLayerState } from '$lib/api/types';

export type SourceMode = 'template' | 'image' | 'ticker' | 'html5' | 'sequence';

export interface AnimConfig {
	mode: string;
	minAlpha: number;
	maxAlpha: number;
	speedHz: number;
	toAlpha: number;
	durationMs: number;
}

export interface FlyConfig {
	direction: string;
	durationMs: number;
}

export interface TickerConfig {
	text: string;
	fontSize: number;
	speed: number;
	bold: boolean;
	loop: boolean;
}

/**
 * What the graphics panel needs to show a layer the way an operator set it
 * up. The engine stores it per layer without reading it, so every session
 * and both engines see the same.
 */
export interface LayerConfig {
	mode?: SourceMode;
	template?: string;
	fields?: Record<string, string>;
	anim?: Partial<AnimConfig>;
	fly?: Partial<FlyConfig>;
	ticker?: Partial<TickerConfig>;
}

export const DEFAULT_ANIM: AnimConfig = { mode: 'pulse', minAlpha: 0.3, maxAlpha: 1.0, speedHz: 1.0, toAlpha: 0.5, durationMs: 500 };
export const DEFAULT_FLY: FlyConfig = { direction: 'left', durationMs: 500 };
export const DEFAULT_TICKER: TickerConfig = { text: 'Breaking News: Welcome to Switchframe', fontSize: 32, speed: 100, bold: false, loop: true };

/** The config the engine holds for a layer; anything that is not an object reads as empty. */
export function storedConfig(layer: GraphicsLayerState | undefined): LayerConfig {
	const c = layer?.config;
	return c && typeof c === 'object' && !Array.isArray(c) ? (c as LayerConfig) : {};
}

/**
 * A layer's reference, the ID playlist events address. It never changes,
 * whatever the stacking order.
 */
export function layerRef(id: number): string {
	return `#${id}`;
}

/** A layer's display name: what the operator named it, else its reference. */
export function layerLabel(layer: Pick<GraphicsLayerState, 'id' | 'name'>): string {
	return layer.name?.trim() || `Layer ${layerRef(layer.id)}`;
}

/**
 * Moves sourceId before or after targetId in a list of layer IDs and returns
 * the new list. The input is not modified.
 */
export function moveLayer(order: number[], sourceId: number, targetId: number, place: 'before' | 'after'): number[] {
	if (sourceId === targetId || !order.includes(sourceId) || !order.includes(targetId)) return order.slice();
	const rest = order.filter((id) => id !== sourceId);
	const at = rest.indexOf(targetId) + (place === 'after' ? 1 : 0);
	rest.splice(at, 0, sourceId);
	return rest;
}

export interface LayerExportDoc {
	format: string;
	version: number;
	layers: unknown[];
}

/** Checks the outer shape of an export file before it goes to the engine, which validates the rest. */
export function parseLayerExport(text: string): LayerExportDoc {
	let doc: unknown;
	try {
		doc = JSON.parse(text);
	} catch {
		throw new Error('not a JSON file');
	}
	const d = doc as Partial<LayerExportDoc>;
	if (!d || typeof d !== 'object' || d.format !== 'switchframe-graphics' || !Array.isArray(d.layers)) {
		throw new Error('not a graphics layer export');
	}
	return d as LayerExportDoc;
}
