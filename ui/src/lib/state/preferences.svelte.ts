const STORAGE_KEY = 'switchframe_confirm_mode';

let confirmMode = $state(false);

export function getConfirmMode(): boolean {
	return confirmMode;
}

export function setConfirmMode(enabled: boolean): void {
	confirmMode = enabled;
	if (typeof localStorage !== 'undefined') {
		localStorage.setItem(STORAGE_KEY, enabled ? '1' : '0');
	}
}

// Initialize from localStorage on module load
if (typeof localStorage !== 'undefined') {
	confirmMode = localStorage.getItem(STORAGE_KEY) === '1';
}
