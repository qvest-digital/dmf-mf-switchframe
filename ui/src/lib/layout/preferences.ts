export type LayoutMode = 'traditional' | 'simple';

const STORAGE_KEY = 'switchframe-layout';

export function getLayoutMode(): LayoutMode {
	if (typeof window !== 'undefined') {
		const urlParam = new URL(window.location.href).searchParams.get('mode');
		if (urlParam === 'simple' || urlParam === 'traditional') {
			localStorage.setItem(STORAGE_KEY, urlParam);
			return urlParam;
		}

		const stored = localStorage.getItem(STORAGE_KEY);
		if (stored === 'simple') return 'simple';
		if (stored === 'traditional') return 'traditional';
	}
	return 'traditional';
}

export function setLayoutMode(mode: LayoutMode): void {
	localStorage.setItem(STORAGE_KEY, mode);
}
