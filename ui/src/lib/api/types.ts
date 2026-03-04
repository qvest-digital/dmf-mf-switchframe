export type TallyStatus = 'program' | 'preview' | 'idle';
export type SourceHealthStatus = 'healthy' | 'stale' | 'no_signal' | 'offline';

export interface SourceInfo {
	key: string;
	label?: string;
	status: SourceHealthStatus;
	lastFrameTime: number;
	width?: number;
	height?: number;
	codec?: string;
}

export interface ControlRoomState {
	programSource: string;
	previewSource: string;
	transitionType: string;
	transitionDurationMs: number;
	transitionPosition: number;
	inTransition: boolean;
	audioLevels: Record<string, number> | null;
	tallyState: Record<string, TallyStatus>;
	sources: Record<string, SourceInfo>;
	seq: number;
	timestamp: number;
}
