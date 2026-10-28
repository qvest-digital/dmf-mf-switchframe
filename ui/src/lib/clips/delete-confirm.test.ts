import { describe, it, expect } from 'vitest';
import { deleteConfirmation } from './delete-confirm';

describe('deleteConfirmation', () => {
	it('warns that a library clip goes for every future booking', () => {
		const c = deleteConfirmation({ name: 'Opener', libraryPath: 'Opener.mp4' });
		expect(c?.message).toContain('"Opener"');
		expect(c?.message).toContain('shared clip library');
		expect(c?.message).toContain('every future booking');
	});

	it('asks nothing for a clip stored only in this switcher', () => {
		expect(deleteConfirmation({ name: 'Local' })).toBeNull();
	});
});
