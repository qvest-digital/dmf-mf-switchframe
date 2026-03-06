export type TallyStatus = 'program' | 'preview' | 'idle';
export type SourceHealthStatus = 'healthy' | 'stale' | 'no_signal' | 'offline';

export interface SourceInfo {
	key: string;
	label?: string;
	status: SourceHealthStatus;
	lastFrameTime: number;
	delayMs?: number;
}

export interface EQBand {
	frequency: number;
	gain: number;
	q: number;
	enabled: boolean;
}

export interface CompressorSettings {
	threshold: number;
	ratio: number;
	attack: number;
	release: number;
	makeupGain: number;
}

export interface AudioChannel {
	level: number;  // dB (-inf to +12)
	trim: number;   // dB (-20 to +20), input gain
	muted: boolean;
	afv: boolean;   // audio-follows-video
	peakL: number;  // dBFS
	peakR: number;  // dBFS
	eq: [EQBand, EQBand, EQBand];
	compressor: CompressorSettings;
	gainReduction: number;  // compressor GR in dB
}

export interface RecordingStatus {
	active: boolean;
	filename?: string;
	bytesWritten?: number;
	durationSecs?: number;
	droppedPackets?: number;
	error?: string;
}

export interface SRTOutputStatus {
	active: boolean;
	mode?: 'caller' | 'listener';
	address?: string;
	port?: number;
	state?: string;
	connections?: number;
	bytesWritten?: number;
	droppedPackets?: number;
	overflowCount?: number;
	error?: string;
}

export interface SRTOutputConfig {
	mode: 'caller' | 'listener';
	address?: string;
	port: number;
	latency?: number;
	streamID?: string;
}

export interface GraphicsState {
	active: boolean;
	template?: string;
	fadePosition?: number;
}

export interface PresetInfo {
	id: string;
	name: string;
}

export interface Preset {
	id: string;
	name: string;
	programSource: string;
	previewSource: string;
	transitionType: string;
	transitionDurMs: number;
	audioChannels: Record<string, AudioChannelPreset>;
	masterLevel: number;
	createdAt: string;
}

export interface AudioChannelPreset {
	level: number;
	muted: boolean;
	afv: boolean;
}

export interface RecallPresetResponse {
	preset: Preset;
	warnings?: string[];
}

export interface MacroStep {
	action: 'cut' | 'preview' | 'transition' | 'wait' | 'set_audio';
	params: Record<string, unknown>;
}

export interface Macro {
	name: string;
	steps: MacroStep[];
}

export interface KeyConfig {
	type: 'chroma' | 'luma';
	enabled: boolean;
	keyColorY?: number;
	keyColorCb?: number;
	keyColorCr?: number;
	similarity?: number;
	smoothness?: number;
	spillSuppress?: number;
	lowClip?: number;
	highClip?: number;
	softness?: number;
	fillSource?: string;
}

export interface ControlRoomState {
	programSource: string;
	previewSource: string;
	transitionType: string;
	transitionDurationMs: number;
	transitionPosition: number;
	inTransition: boolean;
	ftbActive: boolean;
	audioChannels?: Record<string, AudioChannel>;
	masterLevel: number;
	programPeak: [number, number];
	gainReduction?: number;
	tallyState: Record<string, TallyStatus>;
	sources: Record<string, SourceInfo>;
	presets?: PresetInfo[];
	recording?: RecordingStatus;
	srtOutput?: SRTOutputStatus;
	graphics?: GraphicsState;
	seq: number;
	timestamp: number;
}
