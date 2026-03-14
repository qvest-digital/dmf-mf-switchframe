import type { MacroAction } from '$lib/api/types';

export type ActionMeta = {
	label: string;
	category: string;
	description: string;
};

export const ACTION_META: Record<MacroAction, ActionMeta> = {
	// Switching
	cut: { label: 'Cut', category: 'Switching', description: 'Switch program to source' },
	preview: { label: 'Preview', category: 'Switching', description: 'Set preview source' },
	transition: { label: 'Transition', category: 'Switching', description: 'Dissolve, wipe, dip, or stinger to source' },
	ftb: { label: 'Fade to Black', category: 'Switching', description: 'Fade to black' },
	// Audio
	set_audio: { label: 'Set Level', category: 'Audio', description: 'Set audio level' },
	audio_mute: { label: 'Mute', category: 'Audio', description: 'Mute/unmute source' },
	audio_afv: { label: 'AFV', category: 'Audio', description: 'Audio-follow-video toggle' },
	audio_trim: { label: 'Trim', category: 'Audio', description: 'Set input trim' },
	audio_master: { label: 'Master', category: 'Audio', description: 'Set master level' },
	audio_eq: { label: 'EQ', category: 'Audio', description: 'Set EQ band' },
	audio_compressor: { label: 'Compressor', category: 'Audio', description: 'Set compressor' },
	audio_delay: { label: 'Audio Delay', category: 'Audio', description: 'Set audio delay (lip-sync)' },
	// Graphics
	graphics_on: { label: 'Graphics On', category: 'Graphics', description: 'Show DSK overlay' },
	graphics_off: { label: 'Graphics Off', category: 'Graphics', description: 'Hide DSK overlay' },
	graphics_auto_on: { label: 'Auto On', category: 'Graphics', description: 'Auto-transition graphics on' },
	graphics_auto_off: { label: 'Auto Off', category: 'Graphics', description: 'Auto-transition graphics off' },
	graphics_add_layer: { label: 'Add Layer', category: 'Graphics', description: 'Add a new graphics layer' },
	graphics_remove_layer: { label: 'Remove Layer', category: 'Graphics', description: 'Remove a graphics layer' },
	graphics_set_rect: { label: 'Set Position', category: 'Graphics', description: 'Set layer position and size' },
	graphics_set_zorder: { label: 'Set Z-Order', category: 'Graphics', description: 'Set layer stacking order' },
	graphics_fly_in: { label: 'Fly In', category: 'Graphics', description: 'Animate layer flying in from edge' },
	graphics_fly_out: { label: 'Fly Out', category: 'Graphics', description: 'Animate layer flying out to edge' },
	graphics_fly_on: { label: 'Fly On', category: 'Graphics', description: 'Atomically activate and fly in layer' },
	graphics_slide: { label: 'Slide', category: 'Graphics', description: 'Slide layer to new position' },
	graphics_animate: { label: 'Animate', category: 'Graphics', description: 'Start pulse or transition animation' },
	graphics_animate_stop: { label: 'Stop Animation', category: 'Graphics', description: 'Stop current animation' },
	graphics_upload_frame: { label: 'Upload Frame', category: 'Graphics', description: 'Upload rendered graphic to layer' },
	graphics_ticker_start: { label: 'Start Ticker', category: 'Graphics', description: 'Start scrolling ticker on layer' },
	graphics_ticker_stop: { label: 'Stop Ticker', category: 'Graphics', description: 'Stop scrolling ticker on layer' },
	graphics_text_animate: { label: 'Text Animate', category: 'Graphics', description: 'Start text animation (typewriter/fade-word)' },
	graphics_text_animate_stop: { label: 'Stop Text Anim', category: 'Graphics', description: 'Stop text animation on layer' },
	// Output
	recording_start: { label: 'Start Recording', category: 'Output', description: 'Start recording' },
	recording_stop: { label: 'Stop Recording', category: 'Output', description: 'Stop recording' },
	// Presets
	preset_recall: { label: 'Recall Preset', category: 'Presets', description: 'Recall a saved preset' },
	// Keys
	key_set: { label: 'Set Key', category: 'Keys', description: 'Apply chroma/luma key to source' },
	key_delete: { label: 'Remove Key', category: 'Keys', description: 'Remove key from source' },
	// Source
	source_label: { label: 'Set Label', category: 'Source', description: 'Rename source label' },
	source_delay: { label: 'Source Delay', category: 'Source', description: 'Set source video delay' },
	source_position: { label: 'Set Position', category: 'Source', description: 'Reorder source position' },
	// Replay
	replay_mark_in: { label: 'Mark In', category: 'Replay', description: 'Set replay mark-in point' },
	replay_mark_out: { label: 'Mark Out', category: 'Replay', description: 'Set replay mark-out point' },
	replay_play: { label: 'Play', category: 'Replay', description: 'Play replay clip' },
	replay_stop: { label: 'Stop', category: 'Replay', description: 'Stop replay' },
	replay_quick_clip: { label: 'Quick Clip', category: 'Replay', description: 'Quick clip from buffer' },
	replay_play_last: { label: 'Play Last', category: 'Replay', description: 'Replay last clip' },
	replay_play_clip: { label: 'Play Clip', category: 'Replay', description: 'Play saved clip by ID' },
	// Timing
	wait: { label: 'Wait', category: 'Timing', description: 'Pause between steps' },
	// SCTE-35
	scte35_cue: { label: 'Ad Break Cue', category: 'SCTE-35', description: 'Start an ad break' },
	scte35_return: { label: 'Return', category: 'SCTE-35', description: 'End ad break' },
	scte35_cancel: { label: 'Cancel', category: 'SCTE-35', description: 'Cancel a pending splice' },
	scte35_hold: { label: 'Hold', category: 'SCTE-35', description: 'Hold break indefinitely' },
	scte35_extend: { label: 'Extend', category: 'SCTE-35', description: 'Extend break duration' },
	// Captions
	caption_mode: { label: 'Caption Mode', category: 'Captions', description: 'Set caption mode' },
	caption_text: { label: 'Caption Text', category: 'Captions', description: 'Send caption text' },
	caption_clear: { label: 'Clear Captions', category: 'Captions', description: 'Clear caption display' },
};

export const CATEGORIES = ['Switching', 'Timing', 'Audio', 'Graphics', 'Output', 'Presets', 'Keys', 'Source', 'Replay', 'SCTE-35', 'Captions'] as const;

export const SOURCE_ACTIONS: MacroAction[] = [
	'cut', 'preview', 'transition', 'set_audio',
	'audio_mute', 'audio_afv', 'audio_trim',
	'audio_eq', 'audio_compressor', 'audio_delay',
	'key_set', 'key_delete', 'source_label',
	'source_delay', 'source_position',
	'replay_mark_in', 'replay_mark_out', 'replay_play',
	'replay_quick_clip', 'replay_play_clip',
];

/** Graphics actions that take a layerId parameter. */
export const GRAPHICS_LAYER_ACTIONS: MacroAction[] = [
	'graphics_on', 'graphics_off', 'graphics_auto_on', 'graphics_auto_off',
	'graphics_remove_layer', 'graphics_set_rect', 'graphics_set_zorder',
	'graphics_fly_in', 'graphics_fly_out', 'graphics_fly_on', 'graphics_slide',
	'graphics_animate', 'graphics_animate_stop', 'graphics_upload_frame',
	'graphics_ticker_start', 'graphics_ticker_stop',
	'graphics_text_animate', 'graphics_text_animate_stop',
];

export const FLY_DIRECTIONS = [
	{ value: 'left', label: 'Left' },
	{ value: 'right', label: 'Right' },
	{ value: 'top', label: 'Top' },
	{ value: 'bottom', label: 'Bottom' },
];

export const WIPE_DIRECTIONS = [
	{ value: 'h-left', label: 'Left' },
	{ value: 'h-right', label: 'Right' },
	{ value: 'v-top', label: 'Top' },
	{ value: 'v-bottom', label: 'Bottom' },
	{ value: 'box-center-out', label: 'Box Out' },
	{ value: 'box-edges-in', label: 'Box In' },
];
