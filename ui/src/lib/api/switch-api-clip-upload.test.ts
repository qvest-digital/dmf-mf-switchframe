/**
 * uploadClip in dual-engine mode: the clip is uploaded to every engine in
 * parallel under one client-chosen clip ID, and the call resolves only once
 * every engine has stored it. ID-based requests fanned out later then address
 * the same clip on all engines, and no engine trails another by a whole
 * upload, which a load sent in that window would find missing. A clip that
 * only some engines stored is removed again, since it could not be loaded on
 * the others.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { setEngineUrls, setApiBaseUrl } from './base-url';
import { uploadClip, newClipId, clipNameFor, listClipsOnEveryEngine, setPartialFailureNotify } from './switch-api';

interface SentUpload {
	url: string;
	id: string | null;
	file: File | null;
}

const A = 'https://engine-a.example.com/api/clips/upload';
const B = 'https://engine-b.example.com/api/clips/upload';

let sent: SentUpload[];
/** Answers an upload; by default with a clip under the ID that was sent. */
let respond: (url: string, attempt: number, id: string) => { status: number; body: unknown };
/** Engines whose upload is held open until release() is called. */
let held: Map<string, FakeXHR | undefined>;
import { changesInFlight } from '$lib/util/activity';

let fetchMock: ReturnType<typeof vi.fn>;

class FakeXHR {
	status = 0;
	responseText = '';
	upload: { onprogress: ((e: ProgressEvent) => void) | null } = { onprogress: null };
	onload: (() => void) | null = null;
	onerror: (() => void) | null = null;
	onabort: (() => void) | null = null;
	url = '';
	private attempt = 0;
	private id = '';

	open(_method: string, url: string) {
		this.url = url;
	}

	setRequestHeader() {}

	send(body: FormData) {
		this.attempt = sent.filter((s) => s.url === this.url).length;
		this.id = body.get('id') as string;
		sent.push({ url: this.url, id: this.id, file: body.get('file') as File | null });
		if (held.has(this.url)) {
			held.set(this.url, this);
			return;
		}
		this.finish();
	}

	progress(percent: number) {
		this.upload.onprogress?.({ lengthComputable: true, loaded: percent, total: 100 } as ProgressEvent);
	}

	finish() {
		const r = respond(this.url, this.attempt, this.id);
		this.status = r.status;
		this.responseText = JSON.stringify(r.body);
		queueMicrotask(() => this.onload?.());
	}
}

function hold(url: string) {
	held.set(url, undefined);
}

function release(url: string) {
	const xhr = held.get(url);
	held.delete(url);
	xhr?.finish();
}

const stored = (id: string) => ({ status: 201, body: { id, name: 'bunny' } });
const failing = (status: number, error: string) => ({ status, body: { error } });

function deletes(): string[] {
	return fetchMock.mock.calls
		.filter(([, opts]) => (opts as RequestInit | undefined)?.method === 'DELETE')
		.map(([url]) => url as string);
}

describe('uploadClip (dual engine)', () => {
	beforeEach(() => {
		sent = [];
		held = new Map();
		respond = (_url, _attempt, id) => stored(id);
		vi.stubGlobal('XMLHttpRequest', FakeXHR);
		fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204, json: () => Promise.resolve({}) });
		vi.stubGlobal('fetch', fetchMock);
		setApiBaseUrl('');
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		vi.useRealTimers();
		setEngineUrls(null);
		setPartialFailureNotify(null);
	});

	it('uploads to every engine at once under one fresh clip ID', async () => {
		hold(A);
		const done = uploadClip(new File(['data'], 'bunny.mov'));
		await Promise.resolve();

		// The secondary does not wait for the primary to finish.
		expect(sent.map((s) => s.url)).toEqual([A, B]);
		expect(sent[0].id).toMatch(/^[0-9a-f]{32}$/);
		expect(sent[1].id).toBe(sent[0].id);
		expect(sent[1].file?.name).toBe('bunny.mov');

		release(A);
		expect((await done).id).toBe(sent[0].id);
	});

	// A page that reloads itself while an upload runs would drop the upload.
	it('counts an upload as a change in flight until every engine answered', async () => {
		hold(B);
		const done = uploadClip(new File(['data'], 'bunny.mov'));
		await Promise.resolve();
		expect(changesInFlight()).toBe(true);

		release(B);
		await done;
		expect(changesInFlight()).toBe(false);
	});

	it('waits for a secondary that takes longer than the primary, however long', async () => {
		vi.useFakeTimers();
		hold(B);
		let resolved = false;
		const done = uploadClip(new File(['data'], 'bunny.mov')).then(() => { resolved = true; });

		await vi.advanceTimersByTimeAsync(5 * 60_000);
		expect(resolved).toBe(false);

		release(B);
		await done;
		expect(resolved).toBe(true);
	});

	it('reports the progress of the engine furthest behind', async () => {
		hold(A);
		hold(B);
		const progress: number[] = [];
		const done = uploadClip(new File(['data'], 'bunny.mov'), (p) => progress.push(p));
		await Promise.resolve();

		held.get(A)!.progress(100);
		expect(progress.at(-1)).toBe(0);
		held.get(B)!.progress(40);
		expect(progress.at(-1)).toBe(40);

		release(A);
		release(B);
		await done;
	});

	it('stops reporting the progress of an engine that failed for good', async () => {
		vi.useFakeTimers();
		hold(A);
		respond = (url, _attempt, id) => (url === B ? failing(500, 'boom') : stored(id));
		const progress: number[] = [];
		const done = uploadClip(new File(['data'], 'bunny.mov'), (p) => progress.push(p));
		const failed = expect(done).rejects.toThrow();
		await vi.advanceTimersByTimeAsync(2500);

		held.get(A)!.progress(70);
		expect(progress.at(-1)).toBe(70);

		release(A);
		await failed;
	});

	it('accepts a 409 when the engine already holds the clip', async () => {
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		fetchMock.mockImplementation((url: string) =>
			Promise.resolve({ ok: true, json: () => Promise.resolve({ id: url.split('/').pop(), name: 'bunny' }) }));
		respond = (url, _attempt, id) => (url === B ? failing(409, 'clip: already exists') : stored(id));

		const clip = await uploadClip(new File(['data'], 'bunny.mov'));

		expect(fetchMock).toHaveBeenCalledWith(`https://engine-b.example.com/api/clips/${clip.id}`, expect.anything());
		expect(sent.filter((s) => s.url === B)).toHaveLength(1);
		expect(notify).not.toHaveBeenCalled();
	});

	it('does not take a different clip under the same ID for this upload', async () => {
		vi.useFakeTimers();
		fetchMock.mockImplementation((url: string, opts?: RequestInit) =>
			Promise.resolve(opts?.method === 'DELETE'
				? { ok: true, status: 204, json: () => Promise.resolve({}) }
				: { ok: true, json: () => Promise.resolve({ id: url.split('/').pop(), name: 'other' }) }));
		respond = (url, _attempt, id) => (url === B ? failing(409, 'clip: already exists') : stored(id));

		const done = uploadClip(new File(['data'], 'bunny.mov'));
		const failed = expect(done).rejects.toThrow(/Engine B/);
		await vi.advanceTimersByTimeAsync(2500);
		await failed;
	});

	it('succeeds on a retry after a failed attempt', async () => {
		vi.useFakeTimers();
		respond = (url, attempt, id) => (url === B && attempt === 0 ? failing(500, 'boom') : stored(id));

		const done = uploadClip(new File(['data'], 'bunny.mov'));
		await vi.advanceTimersByTimeAsync(2500);
		await done;

		const toB = sent.filter((s) => s.url === B);
		expect(toB).toHaveLength(2);
		expect(toB[1].id).toBe(toB[0].id);
		expect(deletes()).toEqual([]);
	});

	it('removes the clip from the engines that stored it when another fails twice', async () => {
		vi.useFakeTimers();
		fetchMock.mockImplementation((_url: string, opts?: RequestInit) =>
			Promise.resolve(opts?.method === 'DELETE'
				? { ok: true, status: 204, json: () => Promise.resolve({}) }
				: { ok: false, status: 404, json: () => Promise.resolve({}) }));
		respond = (url, _attempt, id) => (url === B ? failing(409, 'upload already in progress') : stored(id));

		const done = uploadClip(new File(['data'], 'bunny.mov'));
		const failed = expect(done).rejects.toThrow(/Engine B.*upload already in progress/);
		await vi.advanceTimersByTimeAsync(2500);
		await failed;

		expect(sent.filter((s) => s.url === B)).toHaveLength(2);
		expect(deletes()).toEqual([`https://engine-a.example.com/api/clips/${sent[0].id}`]);
	});

	it('fails rather than keep a clip only the secondary stored', async () => {
		vi.useFakeTimers();
		respond = (url, _attempt, id) => (url === A ? failing(500, 'boom') : stored(id));

		const done = uploadClip(new File(['data'], 'bunny.mov'));
		const failed = expect(done).rejects.toThrow(/Engine A/);
		await vi.advanceTimersByTimeAsync(2500);
		await failed;

		expect(deletes()).toEqual([`https://engine-b.example.com/api/clips/${sent[0].id}`]);
	});

	it('names an engine the clip could not be removed from', async () => {
		vi.useFakeTimers();
		fetchMock.mockImplementation((_url: string, opts?: RequestInit) =>
			Promise.resolve(opts?.method === 'DELETE'
				? { ok: false, status: 500, json: () => Promise.resolve({}) }
				: { ok: false, status: 404, json: () => Promise.resolve({}) }));
		respond = (url, _attempt, id) => (url === B ? failing(500, 'boom') : stored(id));

		const done = uploadClip(new File(['data'], 'bunny.mov'));
		const failed = expect(done).rejects.toThrow(/Engine B: boom; clip left on Engine A/);
		await vi.advanceTimersByTimeAsync(2500);
		await failed;
	});

	it('does not wait forever on a rollback that never answers', async () => {
		vi.useFakeTimers();
		fetchMock.mockImplementation((_url: string, opts?: RequestInit) => opts?.method === 'DELETE'
			? new Promise((_resolve, reject) => opts.signal?.addEventListener('abort', () => reject(new Error('aborted'))))
			: Promise.resolve({ ok: false, status: 404, json: () => Promise.resolve({}) }));
		respond = (url, _attempt, id) => (url === B ? failing(500, 'boom') : stored(id));

		const done = uploadClip(new File(['data'], 'bunny.mov'));
		const failed = expect(done).rejects.toThrow(/clip left on Engine A/);
		await vi.advanceTimersByTimeAsync(2500 + 3000);
		await failed;
	});

	it('rejects when no engine stores the clip', async () => {
		vi.useFakeTimers();
		respond = () => failing(500, 'boom');

		const done = uploadClip(new File(['data'], 'bunny.mov'));
		const failed = expect(done).rejects.toThrow('boom');
		await vi.advanceTimersByTimeAsync(2500);
		await failed;
		expect(deletes()).toEqual([]);
	});
});

describe('listClipsOnEveryEngine', () => {
	const clip = (id: string) => ({ id, name: id, source: 'upload' });
	let lists: Record<string, unknown>;

	beforeEach(() => {
		setApiBaseUrl('');
		setEngineUrls([
			{ id: 'A', url: 'https://engine-a.example.com' },
			{ id: 'B', url: 'https://engine-b.example.com' },
		]);
		vi.stubGlobal('fetch', vi.fn((url: string) => {
			const body = lists[new URL(url).host];
			return Promise.resolve(body instanceof Error
				? Promise.reject(body)
				: { ok: true, status: 200, json: () => Promise.resolve(body) });
		}));
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		setEngineUrls(null);
	});

	// A clip still uploading, from this client or another, is on one engine
	// only; a load for it would find it missing on the other.
	it('leaves out a clip one engine does not hold yet', async () => {
		lists = {
			'engine-a.example.com': [clip('one'), clip('two')],
			'engine-b.example.com': [clip('one')],
		};

		expect((await listClipsOnEveryEngine()).map((c) => c.id)).toEqual(['one']);
	});

	// Replays, imported recordings and clips from before shared IDs carry a
	// different ID on each engine; players load them by name.
	it('matches clips by name, not by ID', async () => {
		lists = {
			'engine-a.example.com': [{ id: 'a1', name: 'cam1 replay', source: 'replay' }, clip('two')],
			'engine-b.example.com': [{ id: 'b7', name: 'cam1 replay', source: 'replay' }],
		};

		expect((await listClipsOnEveryEngine()).map((c) => c.name)).toEqual(['cam1 replay']);
	});

	it('counts an engine that does not answer as unreachable', async () => {
		vi.useFakeTimers();
		vi.stubGlobal('fetch', vi.fn((url: string, opts?: RequestInit) => new Promise((resolve, reject) => {
			opts?.signal?.addEventListener('abort', () => reject(new Error('aborted')));
			if (url.includes('engine-a')) resolve({ ok: true, status: 200, json: () => Promise.resolve([clip('one')]) });
		})));

		const listed = listClipsOnEveryEngine();
		await vi.advanceTimersByTimeAsync(3_000);

		expect((await listed).map((c) => c.id)).toEqual(['one']);
		vi.useRealTimers();
	});

	it('lists what the reachable engine holds when the other is down', async () => {
		lists = {
			'engine-a.example.com': [clip('one'), clip('two')],
			'engine-b.example.com': new Error('network error'),
		};

		expect((await listClipsOnEveryEngine()).map((c) => c.id)).toEqual(['one', 'two']);
	});
});

describe('clipNameFor', () => {
	// Mirrors the engine: the file name without its extension, or "untitled".
	it('drops the extension as the engine does', () => {
		expect(clipNameFor('bunny.mov')).toBe('bunny');
		expect(clipNameFor('a.b.ts')).toBe('a.b');
		expect(clipNameFor('noext')).toBe('noext');
		expect(clipNameFor('.mov')).toBe('untitled');
	});
});

describe('newClipId', () => {
	it('has the form the engines accept', () => {
		expect(newClipId()).toMatch(/^[0-9a-f]{32}$/);
		expect(newClipId()).not.toBe(newClipId());
	});
});
