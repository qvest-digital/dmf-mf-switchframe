export type TallyStatus = 'program' | 'preview' | 'key_fill' | 'key_alpha' | 'idle';
export type SourceHealthStatus = 'healthy' | 'stale' | 'no_signal' | 'offline';
export type EngineId = 'A' | 'B';
export type EngineRole = 'standalone' | 'leader' | 'follower-healthy' | 'follower-partitioned';

export interface SRTSourceInfo {
	mode: 'listener' | 'caller';
	streamID: string;
	remoteAddr?: string;
	latencyMs: number;
	negotiatedLatencyMs: number;
	rttMs: number;
	rttVarMs: number;
	lossRate: number;
	bitrateKbps: number;
	recvBufMs: number;
	recvBufPackets: number;
	flightSize: number;
	connected: boolean;
	uptimeMs: number;
	packetsReceived: number;
	packetsLost: number;
	packetsDropped: number;
	packetsRetransmitted: number;
	packetsBelated: number;
	reconnectCount?: number;
}

export interface SourceInfo {
	key: string;
	label?: string;
	type: 'demo' | 'mxl' | 'srt' | 'replay' | 'clip';
	status: SourceHealthStatus;
	srt?: SRTSourceInfo;

	position?: number;
	delayMs?: number;
	keyConfig?: KeyConfig;
	isVirtual?: boolean;
	hasCaptions?: boolean;
	corrector?: CorrectorState;
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

export interface GateSettings {
	threshold: number;
	range: number;
	attack: number;
	release: number;
	hold: number;
}

export interface AudioChannel {
	level: number;  // dB (-inf to +12)
	trim: number;   // dB (-20 to +20), input gain
	muted: boolean;
	afv: boolean;   // audio-follows-video
	phaseInvert?: boolean; // 180-degree phase flip
	balance?: number;     // -1.0 (L) to +1.0 (R), 0 = center
	hpfFrequency?: number;
	hpfEnabled?: boolean;
	peakL: number;  // dBFS
	peakR: number;  // dBFS
	eq: [EQBand, EQBand, EQBand];
	compressor: CompressorSettings;
	gainReduction: number;  // compressor GR in dB
	gate: GateSettings;
	gateEnabled?: boolean;
	gateReduction?: number;  // gate GR in dB
	audioDelayMs?: number;  // lip-sync delay (0-500ms)
}

export interface LimiterSettings {
	threshold: number;
	ceiling: number;
	gainReduction: number;
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

export interface CreateSRTSourceConfig {
	type: 'srt';
	mode: 'caller';
	address: string;
	streamID: string;
	label?: string;
	latencyMs?: number;
}

export interface SRTSourceStats {
	mode: string;
	streamID: string;
	remoteAddr?: string;
	state: string;
	connected: boolean;
	uptimeMs: number;
	latencyMs: number;
	negotiatedLatencyMs: number;
	rttMs: number;
	rttVarMs: number;
	recvRateMbps: number;
	lossRatePct: number;
	packetsReceived: number;
	packetsLost: number;
	packetsDropped: number;
	packetsRetransmitted: number;
	packetsBelated: number;
	recvBufMs: number;
	recvBufPackets: number;
	flightSize: number;
	reconnectCount?: number;
}

export interface DestinationConfig {
	type: 'srt-caller' | 'srt-listener' | 'rtmp';
	address?: string;
	port?: number;
	url?: string;
	latency?: number;
	streamID?: string;
	encryption?: string;
	passphrase?: string;
	maxBandwidth?: number;
	maxConns?: number;
	name?: string;
	scte35Enabled?: boolean;
	engine?: string; // "a", "b", or "" (default = local engine)
}

export interface DestinationInfo {
	id: string;
	name?: string;
	type: string;
	address?: string;
	port?: number;
	url?: string;
	state: string;
	bytesWritten?: number;
	droppedPackets?: number;
	connections?: number;
	error?: string;
}

export interface DestinationStatus {
	id: string;
	config: DestinationConfig;
	state: string;
	bytesWritten: number;
	droppedPackets: number;
	overflowCount?: number;
	connections?: number;
	error?: string;
	createdAt: string;
	startedAt?: string;
}

export interface EasingConfig {
	type: string;
	x1?: number;
	y1?: number;
	x2?: number;
	y2?: number;
}

export interface GraphicsLayerState {
	id: number;
	template?: string;
	active: boolean;
	fadePosition?: number;
	animationMode?: string;
	animationHz?: number;
	zOrder: number;
	x: number;
	y: number;
	width: number;
	height: number;
	imageName?: string;
	imageWidth?: number;
	imageHeight?: number;
	html5Url?: string;
	html5Format?: 'ograf' | 'casparcg' | 'unknown';
	html5Status?: 'allocated' | 'loading' | 'ready' | 'playing' | 'stopped' | 'error';
	html5DataSchema?: Record<string, unknown>;
	sequenceLoaded?: boolean;
	sequencePhase?: string;        // 'idle' | 'in' | 'loop' | 'out' | 'done'
	sequenceProcessing?: boolean;
	sequenceProgress?: number;     // 0.0-1.0 processing progress
	sequenceSpeed?: number;        // playback rate: 1.0 = normal, 0.5 = half, 2.0 = double
}

export interface GraphicsState {
	layers?: GraphicsLayerState[];
	programWidth?: number;
	programHeight?: number;
}

export interface LayoutState {
	activePreset: string;
	slots: LayoutSlotState[];
	dvePresets?: DVEPresetInfo[];
	dveAnimating?: boolean;
}

export interface LayoutSlotState {
	id: number;
	sourceKey: string;
	enabled: boolean;
	x: number;
	y: number;
	width: number;
	height: number;
	zOrder: number;
	animating?: boolean;
	scaleMode?: string;
	cropAnchor?: [number, number];
	dve?: DVESlotState;
}

export interface DVESlotState {
	positionX: number;
	positionY: number;
	width: number;
	height: number;
	rotationDeg?: number;
	anchorX?: number;
	anchorY?: number;
	perspectiveX?: number;
	perspectiveY?: number;
	cropTop?: number;
	cropBottom?: number;
	cropLeft?: number;
	cropRight?: number;
	cropSoftness?: number;
	opacity?: number;
	border?: DVEBorderState;
	shadow?: DVEShadowState;
	defocusRadius?: number;
	mirrorH?: boolean;
	mirrorV?: boolean;
	mosaicSize?: number;
	saturation?: number;
	brightness?: number;
	contrast?: number;
	freeze?: boolean;
	strobeFrames?: number;
	mode3d?: string;
	rotateX?: number;
	rotateY?: number;
	rotateZ?: number;
	transform3d?: DVETransform3D;
}

export interface DVETransform3D {
	rotateX?: number;
	rotateY?: number;
	rotateZ?: number;
	fov?: number;
	distance?: number;
	shape?: string;
	lightEnabled?: boolean;
	lightX?: number;
	lightY?: number;
	ambient?: number;
	specular?: number;
	reflectionEnabled?: boolean;
	reflectionOpacity?: number;
	reflectionFade?: number;
}

export interface DVEBorderState {
	width: number;
	colorY: number;
	colorCb: number;
	colorCr: number;
	cornerRadius?: number;
	softness?: number;
	innerWidth?: number;
}

export interface DVEShadowState {
	enabled: boolean;
	offsetX?: number;
	offsetY?: number;
	blur?: number;
	opacity?: number;
	colorY?: number;
}

export interface DVEPresetInfo {
	id: string;
	name: string;
	builtin?: boolean;
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

export type MacroAction =
	// Switching
	| 'cut' | 'preview' | 'transition' | 'ftb'
	// Audio
	| 'set_audio' | 'audio_mute' | 'audio_afv' | 'audio_trim' | 'audio_master'
	| 'audio_eq' | 'audio_compressor' | 'audio_delay'
	// Graphics
	| 'graphics_on' | 'graphics_off' | 'graphics_auto_on' | 'graphics_auto_off'
	| 'graphics_add_layer' | 'graphics_remove_layer'
	| 'graphics_set_rect' | 'graphics_set_zorder'
	| 'graphics_fly_in' | 'graphics_fly_out' | 'graphics_fly_on' | 'graphics_slide'
	| 'graphics_animate' | 'graphics_animate_stop'
	| 'graphics_upload_frame'
	| 'graphics_ticker_start' | 'graphics_ticker_stop'
	| 'graphics_text_animate' | 'graphics_text_animate_stop'
	// Output
	| 'recording_start' | 'recording_stop'
	// Presets
	| 'preset_recall'
	// Keys
	| 'key_set' | 'key_delete'
	// Source
	| 'source_label' | 'source_delay' | 'source_position'
	// Replay (mark-based)
	| 'replay_mark_in' | 'replay_mark_out' | 'replay_play' | 'replay_stop'
	// Replay (clip-based)
	| 'replay_quick_clip' | 'replay_play_last' | 'replay_play_clip'
	// Timing
	| 'wait'
	// SCTE-35
	| 'scte35_cue' | 'scte35_return' | 'scte35_cancel' | 'scte35_hold' | 'scte35_extend'
	// Captions
	| 'caption_mode' | 'caption_text' | 'caption_clear'
	// Clips
	| 'clip_load' | 'clip_play' | 'clip_pause' | 'clip_stop' | 'clip_eject' | 'clip_seek'
	// ST Map
	| 'stmap_assign_source' | 'stmap_remove_source' | 'stmap_assign_program' | 'stmap_remove_program'
	// Layout
	| 'layout_preset' | 'layout_slot_on' | 'layout_slot_off' | 'layout_slot_source' | 'layout_clear'
	// Color Grade
	| 'colorgrade_set' | 'colorgrade_off'
	| 'corrector_set' | 'corrector_reset' | 'colorgrade_save'
	// Per-Source Color Grade
	| 'source_corrector_set' | 'source_corrector_reset';

export interface MacroStep {
	action: MacroAction;
	params: Record<string, unknown>;
}

export interface Macro {
	name: string;
	steps: MacroStep[];
}

export type MacroStepStatus = 'pending' | 'running' | 'done' | 'failed' | 'skipped';

export interface MacroStepState {
	action: MacroAction;
	summary: string;
	status: MacroStepStatus;
	error?: string;
	waitMs?: number;
	waitStartMs?: number;
}

export interface MacroExecutionState {
	running: boolean;
	macroName: string;
	steps: MacroStepState[];
	currentStep: number;
	error?: string;
}

export interface KeyConfig {
	type: 'chroma' | 'luma' | 'neural';
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

export type ReplayPlayerState = 'idle' | 'loading' | 'playing' | 'paused';

export interface ReplayBufferInfo {
	source: string;
	frameCount: number;
	gopCount: number;
	durationSecs: number;
	bytesUsed: number;
}

export interface ReplayState {
	state: ReplayPlayerState;
	source?: string;
	speed?: number;
	loop?: boolean;
	position?: number;
	markIn?: number;     // Unix ms
	markOut?: number;    // Unix ms
	markSource?: string;
	buffers?: ReplayBufferInfo[];
	shutterAngle?: number;    // 0-360 degrees
	gpuAccelerated?: boolean;
}

export type OperatorRole = 'director' | 'audio' | 'graphics' | 'captioner' | 'viewer';

export interface OperatorInfo {
	id: string;
	name: string;
	role: OperatorRole;
	connected: boolean;
}

export interface LockInfo {
	holderId: string;
	holderName: string;
	acquiredAt: number; // Unix ms
}

export interface PipelineFormatInfo {
	width: number;
	height: number;
	fpsNum: number;
	fpsDen: number;
	name: string;
	colorFormat?: string;
	codec?: string;
}

export interface EncoderInfo {
	name: string;
	displayName: string;
	isDefault: boolean;
}

export interface EncoderState {
	current: string;
	available: EncoderInfo[];
}

export type CaptionMode = 'off' | 'passthrough' | 'author' | 'auto';

export interface CaptionState {
	mode: CaptionMode;
	authorBuffer?: string;
	sourceCaptions?: Record<string, boolean>;
}

export interface ASRState {
	available: boolean;
	active: boolean;
	backend: string;
	availableBackends: string[];
	modelName?: string;
	backendConfig?: Record<string, unknown>;
	confidenceFilter?: string;
	speakerDetection?: string;
}

export type ClipUploadStage = 'uploading' | 'analyzing' | 'transcoding' | 'validating';

export interface ClipUploadProgress {
	stage: ClipUploadStage;
	percent: number;
	filename?: string;
}

export type ClipPlayerStatus = 'empty' | 'loaded' | 'playing' | 'paused' | 'holding';

export interface ClipPlayerState {
	id: number;
	clipId?: string;
	clipName?: string;
	state: ClipPlayerStatus;
	speed?: number;
	position?: number;
	loop?: boolean;
}

export interface ClipInfo {
	id: string;
	name: string;
	filename: string;
	source: 'upload' | 'replay' | 'recording';
	codec: string;
	audioCodec?: string;
	width: number;
	height: number;
	fpsNum: number;
	fpsDen: number;
	durationMs: number;
	sampleRate?: number;
	channels?: number;
	byteSize: number;
	loop: boolean;
	createdAt: string;
	ephemeral: boolean;
}

export interface RecordingFileInfo {
	filename: string;
	path: string;
	byteSize: number;
	modTime: string;
}

export interface EngineRecordingGroup {
	engineId: EngineId;
	engineUrl: string;
	recordings: RecordingFileInfo[];
}

export interface SCTE35State {
	enabled: boolean;
	scte104Enabled?: boolean;
	activeEvents: Record<string, SCTE35Active>;
	eventLog: SCTE35Event[];
	heartbeatOk: boolean;
	config: SCTE35Config;
}

export interface SCTE35Active {
	eventId: number;
	commandType: string;
	isOut: boolean;
	durationMs?: number;
	elapsedMs: number;
	remainingMs?: number;
	autoReturn: boolean;
	held: boolean;
	spliceTimePts: number;
	startedAt: number;
	preRollMs?: number;
	descriptors?: SCTE35DescriptorInfo[];
}

export interface SCTE35DescriptorInfo {
	segEventId: number;
	segmentationType: number;
	segmentationTypeName?: string;
	upidType: number;
	upidTypeName?: string;
	upid: string;
	durationMs?: number;
	subSegmentNum?: number;
	subSegmentsExpected?: number;
	cancelled?: boolean;
}

export interface SCTE35Event {
	eventId: number;
	commandType: string;
	isOut: boolean;
	durationMs?: number;
	autoReturn: boolean;
	descriptors?: SCTE35DescriptorInfo[];
	availNum?: number;
	availsExpected?: number;
	spliceTimePts?: number;
	timestamp: number;
	status: string;
	source?: string;
	destinationId?: string;
}

export interface SCTE35Config {
	heartbeatIntervalMs: number;
	defaultPreRollMs: number;
	pid: number;
	verifyEncoding: boolean;
	webhookUrl?: string;
}

export interface SCTE35CueRequest {
	commandType: 'splice_insert' | 'time_signal';
	isOut?: boolean;
	durationMs?: number;
	autoReturn?: boolean;
	preRollMs?: number;
	eventId?: number;
	descriptors?: SCTE35DescriptorRequest[];
}

export interface SCTE35DescriptorRequest {
	segmentationType: number;
	durationMs?: number;
	upidType: number;
	upid: string;
	subSegmentNum?: number;
	subSegmentsExpected?: number;
}

export interface SCTE35Rule {
	id: string;
	name: string;
	enabled: boolean;
	priority?: number;
	conditions?: SCTE35RuleCondition[];
	logic?: 'and' | 'or';
	action: 'pass' | 'delete' | 'replace';
	replaceWith?: Record<string, unknown>;
	destinations?: string[];
}

export interface SCTE35RuleCondition {
	field: string;
	operator: string;
	value: string;
}

export interface CommsParticipant {
	operatorId: string;
	name: string;
	muted: boolean;
	speaking: boolean;
}

export interface CommsState {
	active: boolean;
	participants: CommsParticipant[];
}

export interface CBRStatus {
	enabled: boolean;
	muxrateBps?: number;
	nullPacketsTotal?: number;
	realBytesTotal?: number;
	padBytesTotal?: number;
	burstTicksTotal?: number;
}

export interface ConnectionInfo {
	domain?: string;
	srtIngestPort?: number;
	srtOutputPorts?: number[];
}

export interface ControlRoomState {
	programSource: string;
	previewSource: string;
	transitionType: string;
	transitionDurationMs: number;
	transitionPosition: number;
	transitionEasing?: string;
	inTransition: boolean;
	ftbActive: boolean;
	audioChannels?: Record<string, AudioChannel>;
	masterLevel: number;
	programPeak: [number, number];
	gainReduction?: number;
	limiter?: LimiterSettings;
	momentaryLufs?: number;
	shortTermLufs?: number;
	integratedLufs?: number;
	tallyState: Record<string, TallyStatus>;
	sources: Record<string, SourceInfo>;
	presets?: PresetInfo[];
	recording?: RecordingStatus;
	srtOutput?: SRTOutputStatus;
	destinations?: DestinationInfo[];
	graphics?: GraphicsState;
	layout?: LayoutState;
	replay?: ReplayState;
	operators?: OperatorInfo[];
	locks?: Record<string, LockInfo>;
	pipelineFormat?: PipelineFormatInfo;
	encoder?: EncoderState;
	scte35?: SCTE35State;
	cbr?: CBRStatus;
	connectionInfo?: ConnectionInfo;
	captions?: CaptionState;
	asr?: ASRState;
	clipPlayers?: ClipPlayerState[];
	clipCount?: number;
	clipUpload?: ClipUploadProgress;
	macro?: MacroExecutionState;
	comms?: CommsState;
	stmap?: STMapState;
	colorGrade?: ColorGradeState;
	aiSegmentation?: AISegmentationState;
	neuralKey?: {
		available: boolean;
	};
	playout?: PlayoutState;
	peerReachable?: boolean;       // nil/undefined when no peer configured
	peerLastSeenMs?: number;
	engineLabel?: string;          // "" (standalone), "a", or "b"
	role?: EngineRole;             // standalone | leader | follower-healthy | follower-partitioned
	sessionId?: string;
	engineBUrl?: string;
	lastChangedBy?: string;
	seq: number;
	lastCommandSeq: number;
	lastExecutedSeq?: number;
	lastExecutedErr?: string;
	lateCommands?: number;
	timestamp: number;
}

// ── AI Segmentation Types ──

export interface AISegmentConfig {
	enabled: boolean;
	sensitivity: number;
	edgeSmooth: number;
	background: string;
}

export interface AISegmentationState {
	available: boolean;
	modelName?: string;
	sources?: Record<string, AISegmentConfig>;
}

// ── ST Map Types ──

export interface STMapState {
	sources: Record<string, string>;
	program?: STMapProgramState;
	available: string[];
}

export interface STMapProgramState {
	map: string;
	type: 'static' | 'animated';
	frame: number;
}

export interface STMapInfo {
	name: string;
	width: number;
	height: number;
	type: 'static' | 'animated';
	frame_count?: number;
}

export interface STMapGeneratorInfo {
	name: string;
	description: string;
	type: 'static' | 'animated';
	params: Record<string, STMapParamInfo>;
}

export interface STMapParamInfo {
	description: string;
	default: number;
	min: number;
	max: number;
}

export interface STMapGenerateRequest {
	type: string;
	params?: Record<string, number>;
	name: string;
	width?: number;
	height?: number;
	assign_source?: string;
	assign_program?: boolean;
	frame_count?: number;
}

// ── Color Grading Types ──

export interface CurvePoint {
	x: number;
	y: number;
}

export interface CorrectorState {
	lift: [number, number, number];
	gamma: [number, number, number];
	gain: [number, number, number];
	temperature: number;
	tint: number;
	saturation: number;
	contrast: number;
	shadows: [number, number, number];
	midtones: [number, number, number];
	highlights: [number, number, number];
	curveMaster?: CurvePoint[];
	curveR?: CurvePoint[];
	curveG?: CurvePoint[];
	curveB?: CurvePoint[];
	sharpness: number;
}

export interface LookMetadata {
	name: string;
	group: string;
	description: string;
	builtin: boolean;
}

export interface ColorGradeState {
	enabled: boolean;
	lookName: string;
	lookGroup: string;
	intensity: number;
	corrector?: CorrectorState;
	available: LookMetadata[];
}

// ── GPU Pipeline Types ──

export interface PerfGPUCurrent {
	last_ns: number;
}

export interface PerfWindowStats {
	min_ns: number;
	max_ns: number;
	mean_ns: number;
	p95_ns: number;
}

export interface PerfWindows {
	'1s': PerfWindowStats;
	'10s': PerfWindowStats;
	'60s': PerfWindowStats;
}

export interface PerfNodeSnapshot {
	current: { last_ns: number };
	windows: PerfWindows;
}

export interface PerfGPUSnapshot {
	active: boolean;
	backend: string;
	device: string;
	current: PerfGPUCurrent;
	nodes?: Record<string, PerfNodeSnapshot>;
}

export interface GPUPipelineDebugNode {
	name: string;
	last_ns: number;
	max_ns: number;
	latency_us: number;
	last_error?: string;
}

export interface GPUPipelineDebugSnapshot {
	gpu: boolean;
	backend: string;
	active_nodes: GPUPipelineDebugNode[];
	total_nodes: number;
	run_count: number;
	last_run_ns: number;
	max_run_ns: number;
	total_latency_us: number;
}

export interface SampleClip {
	name: string;
	title: string;
	path: string;
	duration: number;
}

export interface PlayoutClip {
	name: string;
	path: string;
	duration: number;
	source: 'clip-store' | 'sample' | 'recording';
}

// ── Playout Types ──

export interface PlayoutChannelState {
	id: number;
	state: 'stopped' | 'cued' | 'on_air' | 'completed';
	mode?: 'automated' | 'manual' | 'break';
	playlistName?: string;
	currentItemId?: string;
	currentTitle?: string;
	nextItemId?: string;
	nextTitle?: string;
	itemIndex: number;
	itemCount: number;
	itemProgress?: number;
	itemRemainMs?: number;
	activePod?: PodStatusBroadcast;
	pods?: PodStatusBroadcast[];
	podsArmed?: number;
	podsCompleted?: number;
}

export interface PlayoutState {
	channels: PlayoutChannelState[];
}

export interface SCTE35UPID {
	type: number;
	value: string;
}

export interface SCTE35SegDescriptor {
	segmentation_type: number;
	seg_event_id?: number;
	duration_ms?: number;
	upid_type?: number;
	upid?: string;
	additional_upids?: SCTE35UPID[];
	delivery_not_restricted?: boolean;
	web_delivery_allowed?: boolean;
	no_regional_blackout?: boolean;
	archive_allowed?: boolean;
	device_restrictions?: number;
	cancel?: boolean;
}

export interface SCTE35EventData {
	preset?: string;
	command_type?: number;
	event_id?: number;
	is_out?: boolean;
	break_duration_ms?: number;
	auto_return?: boolean;
	timing?: string;
	splice_time_pts?: number;
	pts_adjustment?: number;
	unique_program_id?: number;
	avail_num?: number;
	avails_expected?: number;
	tier?: number;
	descriptors?: SCTE35SegDescriptor[];
}

export interface SecondaryEvent {
	type: 'graphics_on' | 'graphics_off' | 'graphics_update' | 'audio_level' | 'audio_mute' | 'audio_afv' | 'caption_mode' | 'caption_text' | 'scte35_cue';
	layer?: number;
	template?: string;
	offset_ms: number;
	anchor: 'start' | 'end';
	source_key?: string;
	level_db?: number;
	muted?: boolean;
	afv?: boolean;
	caption_mode?: string;
	caption_text?: string;
	scte35?: SCTE35EventData;
}

export interface PlaylistItem {
	id: string;
	type: 'clip' | 'live' | 'slate';
	asset_uri?: string;
	source?: string;
	title: string;
	duration_ms?: number;
	in_point_ms?: number;
	out_point_ms?: number;
	hold?: boolean;
	loop?: boolean;
	transition?: { type: string; duration_ms?: number; wipe_config?: WipeConfig };
	scheduled_time?: string;
	secondary_events?: SecondaryEvent[];
}

export interface PodTemplateDef {
	label: string;
	duration_ms: number;
	auto_return: boolean;
	priority?: 'normal' | 'high' | 'mandatory';
}

export interface Playlist {
	id: string;
	name: string;
	default_transition: { type: string; duration_ms?: number };
	items: PlaylistItem[];
	idle_asset?: string;
	idle_loop?: boolean;
	pods?: PodTemplateDef[];
}

export interface CacheStatus {
	cacheDir: string;
	maxBytes: number;
	usedBytes: number;
	itemCount: number;
	fetchingCount: number;
	errorCount: number;
}

// ── Pod Types ──

export interface PodConfig {
	type: string;
	duration_ms: number;
	auto_return: boolean;
	allow_dump_out: boolean;
	priority: 'normal' | 'high' | 'mandatory';
	contractual?: boolean;
	ephemeral?: boolean;
	hold_until_manual_return?: boolean;
	position?: string;
}

export interface PodInfo {
	id: string;
	config: PodConfig;
	state: 'scheduled' | 'pre_roll' | 'splice_pending' | 'break_active' | 'return_pending' | 'rejoined' | 'dumped_out';
	armed: boolean;
	elapsed_ms: number;
	remaining_ms: number;
	fired_at?: number;
	completed_at?: number;
	scte35_event_id?: number;
}

export interface PodStatusBroadcast {
	id: string;
	type: string;
	state: string;
	armed: boolean;
	elapsedMs?: number;
	remainingMs?: number;
	durationMs: number;
	priority: string;
	position?: string;
}

export interface PodBoardStatus {
	activePod?: PodStatusBroadcast;
	podsArmed: number;
	podsCompleted: number;
	pods: PodStatusBroadcast[];
}

export interface PodTemplate {
	type: string;
	default_duration_ms: number;
	auto_return: boolean;
	allow_dump_out: boolean;
	priority: 'normal' | 'high' | 'mandatory';
	contractual?: boolean;
	ephemeral?: boolean;
	hold_until_manual_return?: boolean;
}

// ── As-Run Types ──

export type AsRunEventType =
	| 'item_start'
	| 'item_end'
	| 'secondary_event_fire'
	| 'splice_active'
	| 'pod_operation'
	| 'manual_override'
	| 'cache_event'
	| 'error';

export interface BlackDetectInfo {
	found: boolean;
	luma_mean: number;
	offset_ms: number;
}

export interface AsRunEvent {
	ts: string;
	type: AsRunEventType;
	channel_id: number;
	item_id?: string;
	title?: string;
	scheduled_ts?: string;
	delta_ms?: number;
	source?: string;

	// Secondary event fields.
	event?: string;
	target_pts?: number;
	actual_pts?: number;

	// Splice/pod fields.
	pod_id?: string;
	pod_type?: string;
	operation?: string;
	operator?: string;
	scheduled_duration_ms?: number;
	actual_duration_ms?: number;
	reason?: string;
	black_detect?: BlackDetectInfo;

	// Error/cache fields.
	message?: string;
}

// ── Wipe Pattern Types ──

export interface WipePattern {
	id: string;
	name: string;
	category: string;
	description?: string;
	builtin?: boolean;
}

export interface WipeConfig {
	pattern: string;
	soft_edge: number;
	border_width: number;
	border_color_y: number;
	border_color_cb: number;
	border_color_cr: number;
	reverse: boolean;
	offset_x: number;
	offset_y: number;
	multiply: number;
}

// ── Health Types ──

export interface ChannelHealth {
	channel_id: number;
	healthy: boolean;
	last_frame_ms: number;
	stalled: boolean;
}
