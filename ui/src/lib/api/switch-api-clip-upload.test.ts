/**
 * uploadClip in dual-engine mode: the clip is uploaded to every engine in
 * parallel under one client-chosen clip ID, and the call resolves only once
 * every engine has stored it. ID-based requests fanned out later then address
 * the same clip on all engines, and no engine trails another by a whole
 * upload, which a load sent in that window would find missing.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { setEngineUrls, setApiBaseUrl } from './base-url';
import { uploadClip, newClipId, setPartialFailureNotify } from './switch-api';

interface SentUpload {
	url: string;
	id: string | null;
	file: File | null;
}

const A = 'https://engine-a.example.com/api/clips/upload';
const B = 'https://engine-b.example.com/api/clips/upload';

let sent: SentUpload[];
let respond: (url: string, attempt: number) => { status: number; body: unknown };
/** Engines whose upload is held open until release() is called. */
let held: Map<string, FakeXHR>;

class FakeXHR {
	status = 0;
	responseText = '';
	upload: { onprogress: ((e: ProgressEvent) => void) | null } = { onprogress: null };
	onload: (() => void) | null = null;
	onerror: (() => void) | null = null;
	onabort: (() => void) | null = null;
	url = '';
	private attempt = 0;

	open(_method: string, url: string) {
		this.url = url;
	}

	setRequestHeader() {}

	send(body: FormData) {
		this.attempt = sent.filter((s) => s.url === this.url).length;
		sent.push({
			url: this.url,
			id: body.get('id') as string | null,
			file: body.get('file') as File | null,
		});
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
		const r = respond(this.url, this.attempt);
		this.status = r.status;
		this.responseText = JSON.stringify(r.body);
		queueMicrotask(() => this.onload?.());
	}
}

function hold(url: string) {
	held.set(url, undefined as unknown as FakeXHR);
}

function release(url: string) {
	const xhr = held.get(url);
	held.delete(url);
	xhr?.finish();
}

const clipID = '0123456789abcdef0123456789abcdef';
const clipA = { id: clipID, name: 'bunny' };

describe('uploadClip (dual engine)', () => {
	beforeEach(() => {
		sent = [];
		held = new Map();
		respond = () => ({ status: 201, body: clipA });
		vi.stubGlobal('XMLHttpRequest', FakeXHR);
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

	it('uploads to every engine at once under the same clip ID', async () => {
		hold(A);
		const file = new File(['data'], 'bunny.mov');

		const done = uploadClip(file, undefined, clipID);
		await Promise.resolve();

		// The secondary does not wait for the primary to finish.
		expect(sent.map((s) => s.url)).toEqual([A, B]);
		expect(sent.every((s) => s.id === clipID)).toBe(true);
		expect(sent[1].file?.name).toBe('bunny.mov');

		release(A);
		expect((await done).id).toBe(clipID);
	});

	it('resolves only once every engine has stored the clip', async () => {
		hold(B);
		let resolved = false;
		const done = uploadClip(new File(['data'], 'bunny.mov'), undefined, clipID).then(() => {
			resolved = true;
		});

		await new Promise((r) => setTimeout(r, 0));
		expect(resolved).toBe(false);

		release(B);
		await done;
		expect(resolved).toBe(true);
	});

	it('reports the progress of the engine furthest behind', async () => {
		hold(A);
		hold(B);
		const progress: number[] = [];
		const done = uploadClip(new File(['data'], 'bunny.mov'), (p) => progress.push(p), clipID);
		await Promise.resolve();

		held.get(A)!.progress(100);
		expect(progress.at(-1)).toBe(0);
		held.get(B)!.progress(40);
		expect(progress.at(-1)).toBe(40);

		release(A);
		release(B);
		await done;
	});

	it('generates a clip ID when none is given', async () => {
		await uploadClip(new File(['data'], 'bunny.mov'));

		expect(sent[0].id).toMatch(/^[0-9a-f]{32}$/);
		expect(sent[1].id).toBe(sent[0].id);
	});

	it('accepts a 409 when the engine already holds the clip', async () => {
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve(clipA) });
		vi.stubGlobal('fetch', fetchMock);
		respond = (url) =>
			url === B
				? { status: 409, body: { error: 'clip: already exists' } }
				: { status: 201, body: clipA };

		await uploadClip(new File(['data'], 'bunny.mov'), undefined, clipID);

		expect(fetchMock).toHaveBeenCalledWith(`https://engine-b.example.com/api/clips/${clipID}`, expect.anything());
		expect(sent.filter((s) => s.url === B)).toHaveLength(1);
		expect(notify).not.toHaveBeenCalled();
	});

	it('retries a 409 for an upload still in progress and then notifies', async () => {
		vi.useFakeTimers();
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 404, json: () => Promise.resolve({}) }));
		respond = (url) =>
			url === B
				? { status: 409, body: { error: 'upload already in progress' } }
				: { status: 201, body: clipA };

		const done = uploadClip(new File(['data'], 'bunny.mov'), undefined, clipID);
		await vi.advanceTimersByTimeAsync(2500);
		await done;

		expect(sent.filter((s) => s.url === B)).toHaveLength(2);
		expect(notify).toHaveBeenCalledTimes(1);
		expect(notify.mock.calls[0][0]).toContain('Engine B');
	});

	it('succeeds on a retry after a failed attempt', async () => {
		vi.useFakeTimers();
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		respond = (url, attempt) =>
			url === B && attempt === 0
				? { status: 500, body: { error: 'boom' } }
				: { status: 201, body: clipA };

		const done = uploadClip(new File(['data'], 'bunny.mov'), undefined, clipID);
		await vi.advanceTimersByTimeAsync(2500);
		await done;

		const toB = sent.filter((s) => s.url === B);
		expect(toB).toHaveLength(2);
		expect(toB.every((s) => s.id === clipID)).toBe(true);
		expect(notify).not.toHaveBeenCalled();
	});

	it('returns a stored clip and notifies when the primary fails twice', async () => {
		vi.useFakeTimers();
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		respond = (url) =>
			url === A
				? { status: 500, body: { error: 'boom' } }
				: { status: 201, body: clipA };

		const done = uploadClip(new File(['data'], 'bunny.mov'), undefined, clipID);
		await vi.advanceTimersByTimeAsync(2500);

		expect((await done).id).toBe(clipID);
		expect(notify).toHaveBeenCalledTimes(1);
		expect(notify.mock.calls[0][0]).toContain('Engine A');
	});

	it('rejects when no engine stores the clip', async () => {
		vi.useFakeTimers();
		respond = () => ({ status: 500, body: { error: 'boom' } });

		const done = uploadClip(new File(['data'], 'bunny.mov'), undefined, clipID);
		const failed = expect(done).rejects.toThrow('boom');
		await vi.advanceTimersByTimeAsync(2500);
		await failed;
	});
});

describe('newClipId', () => {
	it('has the form the engines accept', () => {
		expect(newClipId()).toMatch(/^[0-9a-f]{32}$/);
		expect(newClipId()).not.toBe(newClipId());
	});
});
