import { describe, it, expect, vi } from 'vitest';
import { builtinTemplates, templateList } from './templates';

describe('templates', () => {
	it('has 6 built-in templates', () => {
		expect(Object.keys(builtinTemplates)).toHaveLength(6);
		expect(templateList).toHaveLength(6);
	});

	it('all templates have unique ids', () => {
		const ids = templateList.map(t => t.id);
		expect(new Set(ids).size).toBe(ids.length);
	});

	it('all templates in list are in builtinTemplates', () => {
		for (const tpl of templateList) {
			expect(builtinTemplates[tpl.id]).toBe(tpl);
		}
	});

	it('each template renders without error', () => {
		// Mock CanvasRenderingContext2D
		const mockCtx = {
			fillStyle: '',
			font: '',
			textBaseline: '',
			textAlign: 'start',
			globalAlpha: 1,
			fillRect: vi.fn(),
			fillText: vi.fn(),
			beginPath: vi.fn(),
			arc: vi.fn(),
			fill: vi.fn(),
			save: vi.fn(),
			restore: vi.fn(),
		} as unknown as CanvasRenderingContext2D;

		for (const tpl of templateList) {
			const values: Record<string, string> = {};
			for (const field of tpl.fields) {
				values[field.key] = field.defaultValue;
			}
			expect(() => tpl.render(mockCtx, 1920, 1080, values)).not.toThrow();
		}
	});

	it('news-lower-third has 2 fields', () => {
		expect(builtinTemplates['news-lower-third'].fields).toHaveLength(2);
	});

	it('network-bug supports animation', () => {
		expect(builtinTemplates['network-bug'].supportsAnimation).toBe(true);
	});

	it('network-bug has 1 field', () => {
		expect(builtinTemplates['network-bug'].fields).toHaveLength(1);
	});

	it('score-bug has 6 fields', () => {
		expect(builtinTemplates['score-bug'].fields).toHaveLength(6);
	});

	it('lower-third does not support animation', () => {
		expect(builtinTemplates['lower-third'].supportsAnimation).toBeFalsy();
	});
});
