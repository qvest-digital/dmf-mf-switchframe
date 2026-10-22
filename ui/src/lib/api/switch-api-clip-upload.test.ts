/**
 * uploadClip in dual-engine mode: the clip is uploaded to every engine and
 * secondaries store it under the primary's clip ID, so ID-based requests
 * fanned out later address the same clip on all engines.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { setEngineUrls, setApiBaseUrl } from './base-url';
import { uploadClip, setPartialFailureNotify } from './switch-api';

interface SentUpload {
	url: string;
	id: string | null;
	file: File | null;
}

let sent: SentUpload[];
let respond: (url: string, attempt: number) => { status: number; body: unknown };

class FakeXHR {
	status = 0;
	responseText = '';
	upload: { onprogress: ((e: ProgressEvent) => void) | null } = { onprogress: null };
	onload: (() => void) | null = null;
	onerror: (() => void) | null = null;
	onabort: (() => void) | null = null;
	private url = '';

	open(_method: string, url: string) {
		this.url = url;
	}

	setRequestHeader() {}

	send(body: FormData) {
		const attempt = sent.filter((s) => s.url === this.url).length;
		sent.push({
			url: this.url,
			id: body.get('id') as string | null,
			file: body.get('file') as File | null,
		});
		const r = respond(this.url, attempt);
		this.status = r.status;
		this.responseText = JSON.stringify(r.body);
		queueMicrotask(() => this.onload?.());
	}
}

const clipA = { id: '0123456789abcdef0123456789abcdef', name: 'bunny' };

describe('uploadClip (dual engine)', () => {
	beforeEach(() => {
		sent = [];
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

	it('uploads to the secondary under the primary clip ID', async () => {
		const file = new File(['data'], 'bunny.mov');

		const result = await uploadClip(file);

		expect(result.id).toBe(clipA.id);
		expect(sent.map((s) => s.url)).toEqual([
			'https://engine-a.example.com/api/clips/upload',
			'https://engine-b.example.com/api/clips/upload',
		]);
		expect(sent[0].id).toBeNull();
		expect(sent[1].id).toBe(clipA.id);
		expect(sent[1].file?.name).toBe('bunny.mov');
	});

	it('accepts a 409 when the secondary already holds the clip', async () => {
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve(clipA) });
		vi.stubGlobal('fetch', fetchMock);
		respond = (url) =>
			url.startsWith('https://engine-b')
				? { status: 409, body: { error: 'clip: already exists' } }
				: { status: 201, body: clipA };

		await uploadClip(new File(['data'], 'bunny.mov'));

		expect(fetchMock).toHaveBeenCalledWith(`https://engine-b.example.com/api/clips/${clipA.id}`, expect.anything());
		expect(sent.filter((s) => s.url.startsWith('https://engine-b'))).toHaveLength(1);
		expect(notify).not.toHaveBeenCalled();
	});

	it('retries a 409 for an upload still in progress and then notifies', async () => {
		vi.useFakeTimers();
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 404, json: () => Promise.resolve({}) }));
		respond = (url) =>
			url.startsWith('https://engine-b')
				? { status: 409, body: { error: 'upload already in progress' } }
				: { status: 201, body: clipA };

		const done = uploadClip(new File(['data'], 'bunny.mov'));
		await vi.advanceTimersByTimeAsync(2500);
		await done;

		expect(sent.filter((s) => s.url.startsWith('https://engine-b'))).toHaveLength(2);
		expect(notify).toHaveBeenCalledTimes(1);
	});

	it('notifies when the secondary upload fails twice', async () => {
		vi.useFakeTimers();
		const notify = vi.fn();
		setPartialFailureNotify(notify);
		respond = (url) =>
			url.startsWith('https://engine-b')
				? { status: 500, body: { error: 'boom' } }
				: { status: 201, body: clipA };

		const done = uploadClip(new File(['data'], 'bunny.mov'));
		await vi.advanceTimersByTimeAsync(2500);
		await done;

		const toB = sent.filter((s) => s.url.startsWith('https://engine-b'));
		expect(toB).toHaveLength(2);
		expect(toB.every((s) => s.id === clipA.id)).toBe(true);
		expect(notify).toHaveBeenCalledTimes(1);
	});
});
