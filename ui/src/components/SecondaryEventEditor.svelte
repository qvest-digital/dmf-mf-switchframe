<script lang="ts">
	import type { SecondaryEvent, SCTE35EventData, SCTE35SegDescriptor } from '$lib/api/types';

	// ── SCTE-35 Preset definitions ──
	interface SCTE35Preset {
		label: string;
		category: string;
		data: Partial<SCTE35EventData>;
	}

	const scte35Presets: SCTE35Preset[] = [
		{ label: 'Program Start', category: 'Program', data: { preset: 'program_start', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x10, seg_event_id: 1 }] } },
		{ label: 'Program End', category: 'Program', data: { preset: 'program_end', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x11, seg_event_id: 1 }] } },
		{ label: 'Break Start', category: 'Break', data: { preset: 'break_start', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x22, seg_event_id: 1 }] } },
		{ label: 'Break End', category: 'Break', data: { preset: 'break_end', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x23, seg_event_id: 1 }] } },
		{ label: 'Cue Out', category: 'Splice', data: { preset: 'cue_out', command_type: 5, is_out: true, timing: 'immediate' } },
		{ label: 'Cue In', category: 'Splice', data: { preset: 'cue_in', command_type: 5, is_out: false, timing: 'immediate' } },
		{ label: 'Ad Start', category: 'Provider', data: { preset: 'provider_ad_start', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x30, seg_event_id: 1 }] } },
		{ label: 'Ad End', category: 'Provider', data: { preset: 'provider_ad_end', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x31, seg_event_id: 1 }] } },
		{ label: 'PO Start', category: 'Provider', data: { preset: 'provider_po_start', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x34, seg_event_id: 1 }] } },
		{ label: 'PO End', category: 'Provider', data: { preset: 'provider_po_end', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x35, seg_event_id: 1 }] } },
		{ label: 'PO Start', category: 'Distributor', data: { preset: 'distributor_po_start', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x36, seg_event_id: 1 }] } },
		{ label: 'PO End', category: 'Distributor', data: { preset: 'distributor_po_end', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x37, seg_event_id: 1 }] } },
		{ label: 'Content ID', category: 'Other', data: { preset: 'content_id', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x01, seg_event_id: 1 }] } },
		{ label: 'Chapter Start', category: 'Other', data: { preset: 'chapter_start', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x20, seg_event_id: 1 }] } },
		{ label: 'Chapter End', category: 'Other', data: { preset: 'chapter_end', command_type: 6, timing: 'immediate', descriptors: [{ segmentation_type: 0x21, seg_event_id: 1 }] } },
	];

	const presetCategories = [...new Set(scte35Presets.map(p => p.category))];

	const segTypeNames: Record<number, string> = {
		0x00: '0x00 Not Indicated', 0x01: '0x01 Content ID', 0x10: '0x10 Program Start',
		0x11: '0x11 Program End', 0x12: '0x12 Program Early Term', 0x13: '0x13 Program Breakaway',
		0x14: '0x14 Program Resumption', 0x15: '0x15 Program Runover Planned',
		0x16: '0x16 Program Runover Unplanned', 0x17: '0x17 Program Overlap Start',
		0x20: '0x20 Chapter Start', 0x21: '0x21 Chapter End',
		0x22: '0x22 Break Start', 0x23: '0x23 Break End',
		0x24: '0x24 Opening Credit Start', 0x25: '0x25 Opening Credit End',
		0x26: '0x26 Closing Credit Start', 0x27: '0x27 Closing Credit End',
		0x30: '0x30 Provider Ad Start', 0x31: '0x31 Provider Ad End',
		0x32: '0x32 Distributor Ad Start', 0x33: '0x33 Distributor Ad End',
		0x34: '0x34 Provider PO Start', 0x35: '0x35 Provider PO End',
		0x36: '0x36 Distributor PO Start', 0x37: '0x37 Distributor PO End',
		0x40: '0x40 Unscheduled Event Start', 0x41: '0x41 Unscheduled Event End',
		0x50: '0x50 Network Start', 0x51: '0x51 Network End',
	};

	const upidTypeNames: Record<number, string> = {
		0x00: 'Not Used', 0x01: 'User Defined', 0x03: 'Ad-ID', 0x06: 'ISAN',
		0x08: 'AiringID', 0x09: 'ADI', 0x0A: 'EIDR', 0x0B: 'ATSC Content ID',
		0x0C: 'MPU()', 0x0D: 'MID()', 0x0E: 'ADS Info', 0x10: 'UUID',
	};

	const commandTypeNames: Record<number, string> = {
		0x00: 'Splice Null', 0x05: 'Splice Insert', 0x06: 'Time Signal',
	};

	interface Props {
		events: SecondaryEvent[];
		onUpdate: (events: SecondaryEvent[]) => void;
		itemDurationMs?: number;
	}

	let { events, onUpdate, itemDurationMs }: Props = $props();

	let expanded = $state(false);
	let editingIndex = $state<number | null>(null);

	const eventTypeLabels: Record<string, string> = {
		graphics_on: 'GFX On',
		graphics_off: 'GFX Off',
		graphics_update: 'GFX Update',
		audio_level: 'Audio Level',
		audio_mute: 'Audio Mute',
		audio_afv: 'Audio AFV',
		caption_mode: 'Caption Mode',
		caption_text: 'Caption Text',
		scte35_cue: 'SCTE-35 Cue',
	};

	const eventTypeColors: Record<string, string> = {
		graphics_on: 'var(--accent-green)',
		graphics_off: 'var(--accent-green)',
		graphics_update: 'var(--accent-green)',
		audio_level: 'var(--accent-blue)',
		audio_mute: 'var(--accent-blue)',
		audio_afv: 'var(--accent-blue)',
		caption_mode: 'var(--accent-orange)',
		caption_text: 'var(--accent-orange)',
		scte35_cue: 'var(--tally-program)',
	};

	const eventTypes: SecondaryEvent['type'][] = [
		'graphics_on', 'graphics_off', 'graphics_update',
		'audio_level', 'audio_mute', 'audio_afv',
		'caption_mode', 'caption_text', 'scte35_cue',
	];

	function addEvent() {
		const newEvent: SecondaryEvent = {
			type: 'graphics_on',
			offset_ms: 0,
			anchor: 'start',
		};
		onUpdate([...events, newEvent]);
		editingIndex = events.length;
		expanded = true;
	}

	function removeEvent(idx: number) {
		const updated = events.filter((_, i) => i !== idx);
		onUpdate(updated);
		if (editingIndex === idx) editingIndex = null;
		else if (editingIndex !== null && editingIndex > idx) editingIndex--;
	}

	function updateEvent(idx: number, patch: Partial<SecondaryEvent>) {
		const updated = events.map((e, i) => (i === idx ? { ...e, ...patch } : e));
		onUpdate(updated);
	}

	function formatOffset(evt: SecondaryEvent): string {
		const sign = evt.offset_ms >= 0 ? '+' : '';
		const secs = (evt.offset_ms / 1000).toFixed(1);
		return `${evt.anchor === 'start' ? 'S' : 'E'}${sign}${secs}s`;
	}

	function timelinePosition(evt: SecondaryEvent): number {
		if (!itemDurationMs || itemDurationMs <= 0) return 0;
		const abs = evt.anchor === 'start' ? evt.offset_ms : itemDurationMs + evt.offset_ms;
		return Math.max(0, Math.min(100, (abs / itemDurationMs) * 100));
	}

	function isGraphicsType(type: string): boolean {
		return type === 'graphics_on' || type === 'graphics_off' || type === 'graphics_update';
	}

	function isAudioType(type: string): boolean {
		return type === 'audio_level' || type === 'audio_mute' || type === 'audio_afv';
	}

	function isCaptionType(type: string): boolean {
		return type === 'caption_mode' || type === 'caption_text';
	}

	// ── SCTE-35 helpers ──
	let scte35Advanced = $state(false);
	let scte35DescExpanded = $state<Record<number, boolean>>({});

	function applyPreset(idx: number, preset: SCTE35Preset) {
		updateEvent(idx, { scte35: { ...preset.data } as SCTE35EventData });
	}

	function updateScte35(idx: number, evt: SecondaryEvent, patch: Partial<SCTE35EventData>) {
		updateEvent(idx, { scte35: { ...evt.scte35, ...patch } });
	}

	function updateDescriptor(idx: number, evt: SecondaryEvent, dIdx: number, patch: Partial<SCTE35SegDescriptor>) {
		const descs = [...(evt.scte35?.descriptors ?? [])];
		descs[dIdx] = { ...descs[dIdx], ...patch };
		updateScte35(idx, evt, { descriptors: descs });
	}

	function addDescriptor(idx: number, evt: SecondaryEvent) {
		const descs = [...(evt.scte35?.descriptors ?? [])];
		descs.push({ segmentation_type: 0x22, seg_event_id: 1 });
		updateScte35(idx, evt, { descriptors: descs });
	}

	function removeDescriptor(idx: number, evt: SecondaryEvent, dIdx: number) {
		const descs = (evt.scte35?.descriptors ?? []).filter((_, i) => i !== dIdx);
		updateScte35(idx, evt, { descriptors: descs });
		const exp = { ...scte35DescExpanded };
		delete exp[dIdx];
		scte35DescExpanded = exp;
	}
</script>

<div class="sec-events">
	<button class="sec-header" onclick={() => (expanded = !expanded)}>
		<span class="sec-chevron">{expanded ? '\u25BE' : '\u25B8'}</span>
		<span class="sec-label">Events ({events.length})</span>
	</button>

	{#if expanded}
		<div class="sec-list">
			{#each events as evt, idx}
				<div class="sec-row">
					<span class="sec-type-dot" style="background: {eventTypeColors[evt.type] ?? 'var(--text-tertiary)'}"></span>
					<span class="sec-type-label">{eventTypeLabels[evt.type] ?? evt.type}</span>
					<span class="sec-offset">{formatOffset(evt)}</span>
					<button class="sec-btn sec-edit-btn" onclick={() => (editingIndex = editingIndex === idx ? null : idx)}>
						{editingIndex === idx ? 'Done' : 'Edit'}
					</button>
					<button class="sec-btn sec-del-btn" onclick={() => removeEvent(idx)}>&times;</button>
				</div>

				{#if editingIndex === idx}
					<div class="sec-editor">
						<label class="sec-field">
							<span class="sec-field-label">Type</span>
							<select
								class="sec-select"
								value={evt.type}
								onchange={(e) => updateEvent(idx, { type: (e.target as HTMLSelectElement).value as SecondaryEvent['type'] })}
							>
								{#each eventTypes as t}
									<option value={t}>{eventTypeLabels[t]}</option>
								{/each}
							</select>
						</label>

						<label class="sec-field">
							<span class="sec-field-label">Anchor</span>
							<select
								class="sec-select"
								value={evt.anchor}
								onchange={(e) => updateEvent(idx, { anchor: (e.target as HTMLSelectElement).value as 'start' | 'end' })}
							>
								<option value="start">Start</option>
								<option value="end">End</option>
							</select>
						</label>

						<label class="sec-field">
							<span class="sec-field-label">Offset (ms)</span>
							<input
								class="sec-input"
								type="number"
								value={evt.offset_ms}
								onchange={(e) => updateEvent(idx, { offset_ms: parseInt((e.target as HTMLInputElement).value) || 0 })}
							/>
						</label>

						{#if isGraphicsType(evt.type)}
							<label class="sec-field">
								<span class="sec-field-label">Layer</span>
								<input
									class="sec-input"
									type="number"
									min="0"
									value={evt.layer ?? 0}
									onchange={(e) => updateEvent(idx, { layer: parseInt((e.target as HTMLInputElement).value) || 0 })}
								/>
							</label>
							{#if evt.type === 'graphics_on' || evt.type === 'graphics_update'}
								<label class="sec-field">
									<span class="sec-field-label">Template</span>
									<input
										class="sec-input"
										type="text"
										value={evt.template ?? ''}
										onchange={(e) => updateEvent(idx, { template: (e.target as HTMLInputElement).value || undefined })}
									/>
								</label>
							{/if}
						{/if}

						{#if isAudioType(evt.type)}
							<label class="sec-field">
								<span class="sec-field-label">Source Key</span>
								<input
									class="sec-input"
									type="text"
									value={evt.source_key ?? ''}
									onchange={(e) => updateEvent(idx, { source_key: (e.target as HTMLInputElement).value || undefined })}
								/>
							</label>
							{#if evt.type === 'audio_level'}
								<label class="sec-field">
									<span class="sec-field-label">Level (dB)</span>
									<input
										class="sec-input"
										type="number"
										step="0.1"
										value={evt.level_db ?? 0}
										onchange={(e) => updateEvent(idx, { level_db: parseFloat((e.target as HTMLInputElement).value) || 0 })}
									/>
								</label>
							{/if}
							{#if evt.type === 'audio_mute'}
								<label class="sec-field sec-field-row">
									<input
										type="checkbox"
										checked={evt.muted ?? false}
										onchange={(e) => updateEvent(idx, { muted: (e.target as HTMLInputElement).checked })}
									/>
									<span class="sec-field-label">Muted</span>
								</label>
							{/if}
							{#if evt.type === 'audio_afv'}
								<label class="sec-field sec-field-row">
									<input
										type="checkbox"
										checked={evt.afv ?? false}
										onchange={(e) => updateEvent(idx, { afv: (e.target as HTMLInputElement).checked })}
									/>
									<span class="sec-field-label">AFV</span>
								</label>
							{/if}
						{/if}

						{#if isCaptionType(evt.type)}
							{#if evt.type === 'caption_mode'}
								<label class="sec-field">
									<span class="sec-field-label">Mode</span>
									<select
										class="sec-select"
										value={evt.caption_mode ?? 'off'}
										onchange={(e) => updateEvent(idx, { caption_mode: (e.target as HTMLSelectElement).value })}
									>
										<option value="off">Off</option>
										<option value="passthrough">Passthrough</option>
										<option value="author">Author</option>
										<option value="auto">Auto</option>
									</select>
								</label>
							{/if}
							{#if evt.type === 'caption_text'}
								<label class="sec-field">
									<span class="sec-field-label">Text</span>
									<input
										class="sec-input"
										type="text"
										value={evt.caption_text ?? ''}
										onchange={(e) => updateEvent(idx, { caption_text: (e.target as HTMLInputElement).value })}
									/>
								</label>
							{/if}
						{/if}

						{#if evt.type === 'scte35_cue'}
							<!-- Preset picker -->
							{#if !evt.scte35?.preset}
								<div class="scte-presets">
									{#each presetCategories as cat}
										<div class="scte-preset-group">
											<span class="scte-preset-cat">{cat}</span>
											<div class="scte-preset-pills">
												{#each scte35Presets.filter(p => p.category === cat) as preset}
													<button class="scte-preset-pill" onclick={() => applyPreset(idx, preset)}>{preset.label}</button>
												{/each}
											</div>
										</div>
									{/each}
									<button class="scte-preset-pill scte-preset-custom" onclick={() => updateScte35(idx, evt, { preset: 'custom', command_type: 5, is_out: true, timing: 'immediate' })}>Custom</button>
								</div>
							{:else}
								<!-- Full editor -->
								<div class="scte-editor">
									<div class="scte-preset-badge">
										<span class="scte-badge-text">{evt.scte35.preset?.replace(/_/g, ' ')}</span>
										<button class="scte-badge-clear" onclick={() => updateEvent(idx, { scte35: undefined })} title="Clear preset">&times;</button>
									</div>

									<!-- Command fields -->
									<div class="scte-row">
										<label class="sec-field">
											<span class="sec-field-label">Command</span>
											<select class="sec-select" value={String(evt.scte35.command_type ?? 5)}
												onchange={(e) => updateScte35(idx, evt, { command_type: parseInt((e.target as HTMLSelectElement).value) })}>
												{#each Object.entries(commandTypeNames) as [val, name]}
													<option value={val}>{name}</option>
												{/each}
											</select>
										</label>
										<label class="sec-field">
											<span class="sec-field-label">Event ID</span>
											<input class="sec-input" type="number" placeholder="Auto"
												value={evt.scte35.event_id ?? ''}
												onchange={(e) => {
													const v = parseInt((e.target as HTMLInputElement).value);
													updateScte35(idx, evt, { event_id: Number.isNaN(v) ? undefined : v });
												}} />
										</label>
										<label class="sec-field sec-field-row">
											<input type="checkbox" checked={evt.scte35.is_out ?? false}
												onchange={(e) => updateScte35(idx, evt, { is_out: (e.target as HTMLInputElement).checked })} />
											<span class="sec-field-label">Is Out</span>
										</label>
										<label class="sec-field">
											<span class="sec-field-label">Timing</span>
											<select class="sec-select" value={evt.scte35.timing ?? 'immediate'}
												onchange={(e) => updateScte35(idx, evt, { timing: (e.target as HTMLSelectElement).value })}>
												<option value="immediate">Immediate</option>
												<option value="scheduled">Scheduled</option>
											</select>
										</label>
									</div>

									<!-- Duration + Auto Return (always visible) -->
									<div class="scte-row">
										<label class="sec-field">
											<span class="sec-field-label">Duration (ms)</span>
											<input class="sec-input" type="number"
												value={evt.scte35.break_duration_ms ?? ''}
												onchange={(e) => {
													const v = parseInt((e.target as HTMLInputElement).value);
													updateScte35(idx, evt, { break_duration_ms: Number.isNaN(v) ? undefined : v });
												}} />
										</label>
										<label class="sec-field sec-field-row">
											<input type="checkbox" checked={evt.scte35.auto_return ?? false}
												onchange={(e) => updateScte35(idx, evt, { auto_return: (e.target as HTMLInputElement).checked })} />
											<span class="sec-field-label">Auto Return</span>
										</label>
									</div>

									<!-- Avail fields -->
									<div class="scte-row">
										<label class="sec-field">
											<span class="sec-field-label">Program ID</span>
											<input class="sec-input" type="number" value={evt.scte35.unique_program_id ?? 0}
												onchange={(e) => updateScte35(idx, evt, { unique_program_id: parseInt((e.target as HTMLInputElement).value) || 0 })} />
										</label>
										<label class="sec-field">
											<span class="sec-field-label">Avail #</span>
											<input class="sec-input" type="number" min="0" max="255" value={evt.scte35.avail_num ?? 0}
												onchange={(e) => updateScte35(idx, evt, { avail_num: parseInt((e.target as HTMLInputElement).value) || 0 })} />
										</label>
										<label class="sec-field">
											<span class="sec-field-label">Expected</span>
											<input class="sec-input" type="number" min="0" max="255" value={evt.scte35.avails_expected ?? 0}
												onchange={(e) => updateScte35(idx, evt, { avails_expected: parseInt((e.target as HTMLInputElement).value) || 0 })} />
										</label>
									</div>

									<!-- Advanced (collapsed) -->
									<button class="scte-advanced-toggle" onclick={() => scte35Advanced = !scte35Advanced}>
										{scte35Advanced ? '\u25BE' : '\u25B8'} Advanced
									</button>
									{#if scte35Advanced}
										<div class="scte-row">
											<label class="sec-field">
												<span class="sec-field-label">Tier (0-4095)</span>
												<input class="sec-input" type="number" min="0" max="4095" value={evt.scte35.tier ?? 0}
													onchange={(e) => updateScte35(idx, evt, { tier: parseInt((e.target as HTMLInputElement).value) || 0 })} />
											</label>
											<label class="sec-field">
												<span class="sec-field-label">PTS Adjustment</span>
												<input class="sec-input" type="number" value={evt.scte35.pts_adjustment ?? 0}
													onchange={(e) => updateScte35(idx, evt, { pts_adjustment: parseInt((e.target as HTMLInputElement).value) || 0 })} />
											</label>
											{#if evt.scte35.timing === 'scheduled'}
												<label class="sec-field">
													<span class="sec-field-label">Splice PTS</span>
													<input class="sec-input" type="number" value={evt.scte35.splice_time_pts ?? ''}
														onchange={(e) => {
															const v = parseInt((e.target as HTMLInputElement).value);
															updateScte35(idx, evt, { splice_time_pts: Number.isNaN(v) ? undefined : v });
														}} />
												</label>
											{/if}
										</div>
									{/if}

									<!-- Descriptors (time_signal) -->
									{#if (evt.scte35.command_type ?? 5) === 6}
										<div class="scte-descriptors">
											<span class="scte-desc-title">Descriptors ({evt.scte35.descriptors?.length ?? 0})</span>
											{#each evt.scte35.descriptors ?? [] as desc, dIdx}
												<div class="scte-desc-card">
													<div class="scte-desc-header">
														<button class="scte-desc-toggle" onclick={() => scte35DescExpanded = { ...scte35DescExpanded, [dIdx]: !scte35DescExpanded[dIdx] }}>
															{scte35DescExpanded[dIdx] ? '\u25BE' : '\u25B8'}
														</button>
														<span class="scte-desc-summary">{segTypeNames[desc.segmentation_type] ?? `0x${desc.segmentation_type.toString(16)}`}</span>
														<button class="sec-btn sec-del-btn" onclick={() => removeDescriptor(idx, evt, dIdx)}>&times;</button>
													</div>
													{#if scte35DescExpanded[dIdx]}
														<div class="scte-desc-body">
															<div class="scte-row">
																<label class="sec-field">
																	<span class="sec-field-label">Type</span>
																	<select class="sec-select" value={desc.segmentation_type}
																		onchange={(e) => updateDescriptor(idx, evt, dIdx, { segmentation_type: parseInt((e.target as HTMLSelectElement).value) })}>
																		{#each Object.entries(segTypeNames) as [val, name]}
																			<option value={val}>{name}</option>
																		{/each}
																	</select>
																</label>
																<label class="sec-field">
																	<span class="sec-field-label">Event ID</span>
																	<input class="sec-input" type="number" value={desc.seg_event_id ?? 0}
																		onchange={(e) => updateDescriptor(idx, evt, dIdx, { seg_event_id: parseInt((e.target as HTMLInputElement).value) || 0 })} />
																</label>
															</div>
															<div class="scte-row">
																<label class="sec-field">
																	<span class="sec-field-label">Duration (ms)</span>
																	<input class="sec-input" type="number" value={desc.duration_ms ?? ''}
																		onchange={(e) => {
																			const v = parseInt((e.target as HTMLInputElement).value);
																			updateDescriptor(idx, evt, dIdx, { duration_ms: Number.isNaN(v) ? undefined : v });
																		}} />
																</label>
																<label class="sec-field sec-field-row">
																	<input type="checkbox" checked={desc.cancel ?? false}
																		onchange={(e) => updateDescriptor(idx, evt, dIdx, { cancel: (e.target as HTMLInputElement).checked })} />
																	<span class="sec-field-label">Cancel</span>
																</label>
															</div>
															<!-- UPID -->
															<div class="scte-row">
																<label class="sec-field">
																	<span class="sec-field-label">UPID Type</span>
																	<select class="sec-select" value={desc.upid_type ?? 0}
																		onchange={(e) => updateDescriptor(idx, evt, dIdx, { upid_type: parseInt((e.target as HTMLSelectElement).value) })}>
																		{#each Object.entries(upidTypeNames) as [val, name]}
																			<option value={val}>{name}</option>
																		{/each}
																	</select>
																</label>
																<label class="sec-field">
																	<span class="sec-field-label">UPID Value</span>
																	<input class="sec-input" type="text" value={desc.upid ?? ''}
																		placeholder={desc.upid_type === 3 ? '12-char ASCII' : desc.upid_type === 0x10 ? '32-char hex' : 'hex or ASCII'}
																		onchange={(e) => updateDescriptor(idx, evt, dIdx, { upid: (e.target as HTMLInputElement).value || undefined })} />
																</label>
															</div>
															<!-- Delivery restrictions -->
															<div class="scte-row">
																<label class="sec-field sec-field-row">
																	<input type="checkbox" checked={desc.delivery_not_restricted ?? false}
																		onchange={(e) => updateDescriptor(idx, evt, dIdx, { delivery_not_restricted: (e.target as HTMLInputElement).checked })} />
																	<span class="sec-field-label">Not Restricted</span>
																</label>
															</div>
															{#if !(desc.delivery_not_restricted)}
																<div class="scte-row">
																	<label class="sec-field sec-field-row">
																		<input type="checkbox" checked={desc.web_delivery_allowed ?? false}
																			onchange={(e) => updateDescriptor(idx, evt, dIdx, { web_delivery_allowed: (e.target as HTMLInputElement).checked })} />
																		<span class="sec-field-label">Web</span>
																	</label>
																	<label class="sec-field sec-field-row">
																		<input type="checkbox" checked={desc.no_regional_blackout ?? false}
																			onchange={(e) => updateDescriptor(idx, evt, dIdx, { no_regional_blackout: (e.target as HTMLInputElement).checked })} />
																		<span class="sec-field-label">No Blackout</span>
																	</label>
																	<label class="sec-field sec-field-row">
																		<input type="checkbox" checked={desc.archive_allowed ?? false}
																			onchange={(e) => updateDescriptor(idx, evt, dIdx, { archive_allowed: (e.target as HTMLInputElement).checked })} />
																		<span class="sec-field-label">Archive</span>
																	</label>
																	<label class="sec-field">
																		<span class="sec-field-label">Device</span>
																		<select class="sec-select" value={desc.device_restrictions ?? 0}
																			onchange={(e) => updateDescriptor(idx, evt, dIdx, { device_restrictions: parseInt((e.target as HTMLSelectElement).value) })}>
																			<option value="0">No restrictions</option>
																			<option value="1">Restrict Group 1</option>
																			<option value="2">Restrict Group 2</option>
																			<option value="3">None</option>
																		</select>
																	</label>
																</div>
															{/if}
														</div>
													{/if}
												</div>
											{/each}
											<button class="scte-add-desc-btn" onclick={() => addDescriptor(idx, evt)}>+ Descriptor</button>
										</div>
									{/if}
								</div>
							{/if}
						{/if}
					</div>
				{/if}
			{/each}

			<button class="sec-add-btn" onclick={addEvent}>+ Add Event</button>
		</div>

		{#if itemDurationMs && itemDurationMs > 0 && events.length > 0}
			<div class="sec-timeline">
				<div class="sec-timeline-bar">
					{#each events as evt}
						<div
							class="sec-timeline-marker"
							style="left: {timelinePosition(evt)}%; background: {eventTypeColors[evt.type] ?? 'var(--text-tertiary)'}"
							title="{eventTypeLabels[evt.type]} @ {formatOffset(evt)}"
						></div>
					{/each}
				</div>
			</div>
		{/if}
	{/if}
</div>

<style>
	.sec-events {
		padding: 0 5px 3px 23px;
	}

	.sec-header {
		display: flex;
		align-items: center;
		gap: 4px;
		background: none;
		border: none;
		padding: 1px 0;
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 8px;
		color: var(--text-tertiary);
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.sec-header:hover {
		color: var(--text-secondary);
	}

	.sec-chevron {
		font-size: 9px;
		line-height: 1;
	}

	.sec-label {
		/* inherits from header */
	}

	.sec-list {
		display: flex;
		flex-direction: column;
		gap: 1px;
		margin-top: 2px;
	}

	.sec-row {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 2px 4px;
		border-radius: 2px;
	}

	.sec-row:hover {
		background: var(--bg-hover);
	}

	.sec-type-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.sec-type-label {
		font-family: var(--font-ui);
		font-size: 9px;
		color: var(--text-secondary);
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.sec-offset {
		font-family: var(--font-mono);
		font-size: 8px;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.sec-btn {
		background: none;
		border: 1px solid var(--border-subtle);
		border-radius: 2px;
		padding: 0 4px;
		font-family: var(--font-ui);
		font-size: 8px;
		cursor: pointer;
		line-height: 1.6;
	}

	.sec-edit-btn {
		color: var(--text-tertiary);
	}

	.sec-edit-btn:hover {
		color: var(--accent-blue);
		border-color: var(--accent-blue);
	}

	.sec-del-btn {
		color: var(--text-tertiary);
		font-size: 10px;
		line-height: 1.2;
		padding: 0 3px;
	}

	.sec-del-btn:hover {
		color: var(--tally-program);
		border-color: var(--tally-program);
	}

	.sec-editor {
		display: flex;
		flex-wrap: wrap;
		gap: 4px 8px;
		padding: 4px 4px 6px 10px;
		margin: 1px 0 3px;
		border-left: 2px solid var(--border-subtle);
	}

	.sec-field {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 80px;
	}

	.sec-field-row {
		flex-direction: row;
		align-items: center;
		gap: 4px;
	}

	.sec-field-label {
		font-family: var(--font-ui);
		font-size: 8px;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.sec-select,
	.sec-input {
		background: rgba(0, 0, 0, 0.2);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-mono);
		font-size: 9px;
		padding: 2px 4px;
		outline: none;
	}

	.sec-select:focus,
	.sec-input:focus {
		border-color: var(--accent-blue);
	}

	.sec-input[type='number'] {
		width: 64px;
	}

	.sec-input[type='text'] {
		width: 100px;
	}

	.sec-add-btn {
		margin-top: 2px;
		background: none;
		border: 1px dashed var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 8px;
		padding: 2px 8px;
		cursor: pointer;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.sec-add-btn:hover {
		color: var(--accent-blue);
		border-color: var(--accent-blue);
	}

	.sec-timeline {
		margin-top: 4px;
		padding: 0 4px;
	}

	.sec-timeline-bar {
		position: relative;
		height: 6px;
		background: rgba(255, 255, 255, 0.05);
		border-radius: 3px;
		overflow: visible;
	}

	.sec-timeline-marker {
		position: absolute;
		top: -1px;
		width: 4px;
		height: 8px;
		border-radius: 1px;
		transform: translateX(-2px);
	}

	/* ── SCTE-35 Editor ── */
	.scte-presets {
		display: flex;
		flex-direction: column;
		gap: 3px;
		width: 100%;
	}

	.scte-preset-group {
		display: flex;
		align-items: baseline;
		gap: 4px;
	}

	.scte-preset-cat {
		font-family: var(--font-ui);
		font-size: 7px;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		width: 52px;
		flex-shrink: 0;
		text-align: right;
	}

	.scte-preset-pills {
		display: flex;
		flex-wrap: wrap;
		gap: 2px;
	}

	.scte-preset-pill {
		background: rgba(229, 9, 20, 0.1);
		border: 1px solid rgba(229, 9, 20, 0.3);
		border-radius: 3px;
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: 8px;
		padding: 1px 5px;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.scte-preset-pill:hover {
		background: rgba(229, 9, 20, 0.25);
		border-color: rgba(229, 9, 20, 0.6);
	}

	.scte-preset-custom {
		background: rgba(255, 255, 255, 0.05);
		border-color: var(--border-subtle);
		margin-left: 56px;
	}

	.scte-preset-custom:hover {
		background: rgba(255, 255, 255, 0.1);
		border-color: var(--text-tertiary);
	}

	.scte-editor {
		display: flex;
		flex-direction: column;
		gap: 4px;
		width: 100%;
	}

	.scte-preset-badge {
		display: flex;
		align-items: center;
		gap: 4px;
		margin-bottom: 2px;
	}

	.scte-badge-text {
		font-family: var(--font-ui);
		font-size: 8px;
		font-weight: 600;
		color: var(--tally-program);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		background: rgba(229, 9, 20, 0.12);
		padding: 1px 6px;
		border-radius: 3px;
	}

	.scte-badge-clear {
		background: none;
		border: none;
		color: var(--text-tertiary);
		font-size: 11px;
		cursor: pointer;
		padding: 0 2px;
		line-height: 1;
	}

	.scte-badge-clear:hover {
		color: var(--tally-program);
	}

	.scte-row {
		display: flex;
		flex-wrap: wrap;
		gap: 4px 8px;
	}

	.scte-advanced-toggle {
		background: none;
		border: none;
		padding: 1px 0;
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 8px;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		text-align: left;
	}

	.scte-advanced-toggle:hover {
		color: var(--text-secondary);
	}

	.scte-descriptors {
		display: flex;
		flex-direction: column;
		gap: 3px;
		margin-top: 2px;
		padding-top: 4px;
		border-top: 1px solid var(--border-subtle);
	}

	.scte-desc-title {
		font-family: var(--font-ui);
		font-size: 8px;
		font-weight: 600;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.scte-desc-card {
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: rgba(0, 0, 0, 0.15);
		overflow: hidden;
	}

	.scte-desc-header {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 2px 4px;
	}

	.scte-desc-toggle {
		background: none;
		border: none;
		color: var(--text-tertiary);
		font-size: 9px;
		cursor: pointer;
		padding: 0;
		line-height: 1;
	}

	.scte-desc-summary {
		flex: 1;
		font-family: var(--font-mono);
		font-size: 8px;
		color: var(--text-secondary);
	}

	.scte-desc-body {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 4px 6px 6px;
		border-top: 1px solid var(--border-subtle);
	}

	.scte-add-desc-btn {
		background: none;
		border: 1px dashed rgba(229, 9, 20, 0.3);
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 8px;
		padding: 2px 8px;
		cursor: pointer;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.scte-add-desc-btn:hover {
		color: var(--tally-program);
		border-color: rgba(229, 9, 20, 0.6);
	}
</style>
