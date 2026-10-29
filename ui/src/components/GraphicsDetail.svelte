<script lang="ts">
	import type { GraphicsLayerState } from '$lib/api/types';
	import type { GraphicsPublisher } from '$lib/graphics/publisher';
	import { templateList, builtinTemplates } from '$lib/graphics/templates';
	import { graphicsListHTML5Templates, type HTML5TemplateInfo } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import {
		type AnimConfig, type FlyConfig, type TickerConfig, type SourceMode, DEFAULT_TICKER,
	} from '$lib/graphics/layer-config';
	import {
		ICON_LAYERS, ICON_TEXT, ICON_IMAGE, ICON_TICKER, ICON_CODE, ICON_FILM,
		ICON_CHEVRON_DOWN, ICON_CHEVRON_RIGHT, ICON_UPLOAD, ICON_TRASH,
		renderIcon,
	} from '$lib/icons';

	function normalizeSchemaFields(schema: any): any[] {
		if (schema?.fields) return schema.fields as any[];
		const props = schema?.properties ?? schema?.dataSchema?.properties;
		if (props) {
			return Object.entries(props).map(([key, prop]: [string, any]) => {
				let fieldType = 'text';
				let defaultVal: any = prop.default ?? '';

				if (prop.type === 'array' || prop.type === 'object') {
					fieldType = 'textarea';
					if (typeof defaultVal === 'object' && defaultVal !== null) {
						defaultVal = JSON.stringify(defaultVal, null, 2);
					}
				} else if (prop.type === 'boolean') {
					fieldType = 'boolean';
					defaultVal = prop.default ?? false;
				} else if (prop.type === 'number' || prop.type === 'integer') {
					fieldType = 'number';
					defaultVal = prop.default ?? 0;
				} else if (prop.enum) {
					fieldType = 'select';
				} else if (prop.type === 'string') {
					fieldType = key.toLowerCase().includes('color') ? 'color' : 'text';
				}

				return {
					key,
					label: prop.title || key,
					type: fieldType,
					default: defaultVal,
					options: prop.enum,
				};
			});
		}
		return [];
	}

	type TickerStartConfig = TickerConfig;

	interface Props {
		layer: GraphicsLayerState;
		sourceMode: SourceMode;
		templateId: string;
		fields: Record<string, string>;
		animConfig: AnimConfig;
		flyConfig: FlyConfig;
		tickerConfig?: TickerConfig;
		onTickerConfigChange?: (key: keyof TickerConfig, value: string | number | boolean) => void;
		publisher: GraphicsPublisher;
		onModeChange: (mode: SourceMode) => void;
		onTemplateChange: (tplId: string) => void;
		onFieldChange: (key: string, value: string) => void;
		onAnimConfigChange: (key: string, value: string | number) => void;
		onFlyConfigChange: (key: string, value: string | number) => void;
		onCutOn: () => void;
		onAutoOn: () => void;
		onCutOff: () => void;
		onAutoOff: () => void;
		onFlyIn: () => void;
		onFlyOut: () => void;
		onAnimate: () => void;
		onAnimateStop: () => void;
		onImageUpload: (file: File) => void;
		onImageDelete: () => void;
		onRectChange: (rect: { x: number; y: number; width: number; height: number }) => void;
		onTickerStart: (config: TickerStartConfig) => void;
		onTickerStop: () => void;
		onTickerUpdateText: (text: string) => void;
		tickerActive: boolean;
		onHTML5Load: (url: string) => void;
		onHTML5UpdateData: (data: Record<string, unknown>) => void;
		onHTML5Play: () => void;
		onHTML5Stop: () => void;
		onHTML5Next: () => void;
		onHTML5Unload: () => void;
		onHTML5Custom?: (action: string, payload: Record<string, unknown>) => void;
		onSequenceUpload?: (file: File, fps: number) => void;
		onSequenceDelete?: () => void;
		onSequenceSpeed?: (speed: number) => void;
	}

	let {
		layer, sourceMode, templateId, fields, animConfig, flyConfig,
		tickerConfig = DEFAULT_TICKER, onTickerConfigChange = () => {}, publisher,
		onModeChange, onTemplateChange, onFieldChange, onAnimConfigChange, onFlyConfigChange,
		onCutOn, onAutoOn, onCutOff, onAutoOff,
		onFlyIn, onFlyOut, onAnimate, onAnimateStop,
		onImageUpload, onImageDelete, onRectChange,
		onTickerStart, onTickerStop, onTickerUpdateText, tickerActive,
		onHTML5Load, onHTML5UpdateData, onHTML5Play, onHTML5Stop, onHTML5Next, onHTML5Unload,
		onHTML5Custom,
		onSequenceUpload, onSequenceDelete, onSequenceSpeed,
	}: Props = $props();

	let previewCanvas = $state<HTMLCanvasElement | null>(null);
	let flyOpen = $state(false);
	let animOpen = $state(false);

	const tickerText = $derived(tickerConfig.text);

	let fileInput = $state<HTMLInputElement | null>(null);
	let seqFileInput = $state<HTMLInputElement | null>(null);
	let seqFPS = $state(59.94);

	let html5Url = $state('');
	let html5DataJson = $state('{}');
	let html5Templates = $state<HTML5TemplateInfo[]>([]);
	let html5TemplatesLoaded = $state(false);

	let html5FormValues = $state<Record<string, any>>({});
	let html5LocalSchema = $state<any>(null);
	let html5Schema = $derived(html5LocalSchema ?? layer.html5DataSchema as any);
	let html5Fields = $derived(normalizeSchemaFields(html5Schema));
	let html5Actions = $derived((html5Schema?.actions as any[]) ?? []);
	let hasSchema = $derived(html5Fields.length > 0);

	$effect(() => {
		if (sourceMode === 'html5' && !html5TemplatesLoaded) {
			graphicsListHTML5Templates().then((t) => {
				html5Templates = t;
				html5TemplatesLoaded = true;
			}).catch(() => {
				html5TemplatesLoaded = true;
			});
		}
	});

	let html5SchemaFingerprint = $derived(
		html5Fields.map((f: any) => f.key).join(',')
	);
	let lastSchemaFingerprint = '';
	$effect(() => {
		const fp = html5SchemaFingerprint;
		if (html5Fields.length > 0 && fp !== lastSchemaFingerprint) {
			const defaults: Record<string, any> = {};
			const jsonDefaults: Record<string, any> = {};
			for (const field of html5Fields) {
				if (field.type === 'textarea' && Array.isArray(field.default)) {
					defaults[field.key] = JSON.stringify(field.default, null, 2);
					jsonDefaults[field.key] = field.default;
				} else {
					defaults[field.key] = field.default ?? '';
					jsonDefaults[field.key] = field.default ?? '';
				}
			}
			html5FormValues = defaults;
			html5DataJson = JSON.stringify(jsonDefaults, null, 2);
			lastSchemaFingerprint = fp;
		}
	});

	const tpl = $derived(builtinTemplates[templateId]);
	const busy = $derived(
		!!layer.animationMode ||
		!!layer.sequenceProcessing ||
		(layer.active && layer.fadePosition != null && layer.fadePosition > 0 && layer.fadePosition < 1)
	);

	const tplAccent: Record<string, string> = {
		'lower-third': '#3b82f6',
		'news-lower-third': '#dc2626',
		'full-screen': '#8b5cf6',
		'ticker': '#0891b2',
		'network-bug': '#f59e0b',
		'score-bug': '#16a34a',
	};

	const tplShortName: Record<string, string> = {
		'lower-third': 'Lower 3rd',
		'news-lower-third': 'News L3',
		'full-screen': 'Full Scr',
		'ticker': 'Ticker',
		'network-bug': 'Bug',
		'score-bug': 'Score',
	};

	$effect(() => {
		const canvas = previewCanvas;
		const t = tpl;
		const f = fields;
		if (!t || !canvas) return;
		try {
			publisher.renderPreview(canvas, t, f);
		} catch {
			// Canvas rendering may fail in test environments
		}
	});

	const fieldLayout = $derived.by(() => {
		if (!tpl) return 'single';
		if (tpl.fields.length >= 4) return 'grid-3';
		if (tpl.fields.length >= 2) return 'grid-2';
		return 'single';
	});

	function handleFileSelect(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (file) {
			onImageUpload(file);
			input.value = '';
		}
	}

	function handleSeqFileSelect(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (file) {
			onSequenceUpload?.(file, seqFPS);
			input.value = '';
		}
	}

	const modeItems: { id: SourceMode; label: string; icon: string }[] = [
		{ id: 'template', label: 'TPL', icon: ICON_TEXT },
		{ id: 'image', label: 'IMG', icon: ICON_IMAGE },
		{ id: 'ticker', label: 'TICK', icon: ICON_TICKER },
		{ id: 'html5', label: 'H5', icon: ICON_CODE },
		{ id: 'sequence', label: 'SEQ', icon: ICON_FILM },
	];

	function html5FormatColor(format?: string): string {
		switch (format) {
			case 'ograf': return '#16a34a';
			case 'casparcg': return '#3b82f6';
			default: return '#6b7280';
		}
	}

	function html5FormatLabel(format?: string): string {
		switch (format) {
			case 'ograf': return 'OGraf';
			case 'casparcg': return 'CasparCG';
			default: return 'Unknown';
		}
	}

	function html5StatusColor(status?: string): string {
		switch (status) {
			case 'loading': return '#eab308';
			case 'ready': return '#16a34a';
			case 'playing': return '#06b6d4';
			case 'stopped': return '#6b7280';
			case 'error': return '#dc2626';
			default: return '#6b7280';
		}
	}

	function handleHTML5Load() {
		if (html5Url.trim()) {
			onHTML5Load(html5Url.trim());
		}
	}

	function handleHTML5UpdateData() {
		try {
			const data = JSON.parse(html5DataJson);
			onHTML5UpdateData(data);
		} catch {
			notify('error', 'Invalid JSON data');
		}
	}

	function handleFormUpdate() {
		const data = { ...html5FormValues };
		for (const field of html5Fields) {
			if (field.type === 'textarea' && typeof data[field.key] === 'string') {
				try {
					data[field.key] = JSON.parse(data[field.key]);
				} catch {
					// Keep as string if not valid JSON
				}
			}
		}
		onHTML5UpdateData(data);
	}

	function handleFormAction(action: any) {
		if (action.update) {
			for (const [key, val] of Object.entries(action.update)) {
				if (val === '+1') {
					html5FormValues[key] = (Number(html5FormValues[key]) || 0) + 1;
				} else if (val === '-1') {
					html5FormValues[key] = (Number(html5FormValues[key]) || 0) - 1;
				} else if (typeof val === 'string' && /^[+-]\d+$/.test(val)) {
					html5FormValues[key] = (Number(html5FormValues[key]) || 0) + Number(val);
				} else if (val === 'toggle') {
					html5FormValues[key] = !html5FormValues[key];
				} else {
					html5FormValues[key] = val;
				}
			}
			handleFormUpdate();
		}
		if (action.action) {
			onHTML5Custom?.(action.action, { ...html5FormValues });
		}
	}
</script>

<div class="detail-pane">
	<!-- ═══ MODE TABS (segmented control) ═══ -->
	<div class="mode-bar">
		{#each modeItems as m}
			<button
				class="mode-seg"
				class:active={sourceMode === m.id}
				onclick={() => onModeChange(m.id)}
				aria-pressed={sourceMode === m.id}
				title={m.label}
			>
				<span class="mode-icon">{@html renderIcon(m.icon, 11, 'stroke')}</span>
				<span class="mode-label">{m.label}</span>
				{#if m.id === 'image' && layer.imageName}
					<span class="mode-dot loaded"></span>
				{:else if m.id === 'ticker' && tickerActive}
					<span class="mode-dot live"></span>
				{:else if m.id === 'html5' && layer.html5Status === 'playing'}
					<span class="mode-dot live"></span>
				{:else if m.id === 'html5' && layer.html5Url}
					<span class="mode-dot loaded"></span>
				{:else if m.id === 'sequence' && layer.sequenceProcessing}
					<span class="mode-dot processing"></span>
				{:else if m.id === 'sequence' && layer.sequenceLoaded}
					<span class="mode-dot loaded"></span>
				{/if}
			</button>
		{/each}
	</div>

	<!-- ═══ SCROLLABLE CONTENT AREA ═══ -->
	<div class="content-scroll">
		<div class="content-body">
			<!-- Preview column for template mode -->
			{#if sourceMode === 'template'}
				<div class="tpl-layout">
					<div class="tpl-controls">
						<div class="tpl-strip">
							{#each templateList as t}
								<button
									class="tpl-card"
									class:selected={templateId === t.id}
									onclick={() => onTemplateChange(t.id)}
									title={t.name}
									aria-label={t.name}
									aria-pressed={templateId === t.id}
									style="--tpl-accent: {tplAccent[t.id] ?? '#888'}"
								>
									<span class="tpl-accent-bar"></span>
									<span class="tpl-card-name">{tplShortName[t.id] ?? t.name}</span>
								</button>
							{/each}
						</div>

						{#if tpl}
							<div class="fields-section" class:grid-2={fieldLayout === 'grid-2'} class:grid-3={fieldLayout === 'grid-3'}>
								{#each tpl.fields as field}
									<label class="field-item">
										<span class="field-lbl">{field.label}</span>
										<input
											type="text"
											class="field-inp"
											value={fields[field.key] ?? ''}
											maxlength={field.maxLength}
											oninput={(e) => onFieldChange(field.key, (e.target as HTMLInputElement).value)}
										/>
									</label>
								{/each}
							</div>
						{/if}
					</div>

					<div class="preview-col">
						<canvas
							bind:this={previewCanvas}
							class="detail-preview"
							width={384}
							height={216}
							aria-label="Layer {layer.id} preview"
						></canvas>
					</div>
				</div>

			<!-- ═══ IMAGE MODE ═══ -->
			{:else if sourceMode === 'image'}
				<input
					bind:this={fileInput}
					type="file"
					accept="image/png"
					class="file-input-hidden"
					onchange={handleFileSelect}
				/>
				<div class="image-section">
					<button class="act-btn upload" onclick={() => fileInput?.click()}>
						<span class="btn-icon">{@html renderIcon(ICON_UPLOAD, 11, 'stroke')}</span>
						UPLOAD PNG
					</button>
					{#if layer.imageName}
						<span class="image-info">{layer.imageName} ({layer.imageWidth}&times;{layer.imageHeight})</span>
						<button class="act-btn del" onclick={onImageDelete}>
							<span class="btn-icon">{@html renderIcon(ICON_TRASH, 11, 'stroke')}</span>
							DEL
						</button>
					{:else}
						<span class="ctl-unit">No image uploaded</span>
					{/if}
				</div>

				{#if layer.imageName}
					<div class="rect-row">
						<label class="ctl-param" title="X position">
							<span>X</span>
							<input type="number" min="0" step="10"
								value={layer.x}
								onchange={(e) => onRectChange({ x: parseInt((e.target as HTMLInputElement).value) || 0, y: layer.y, width: layer.width, height: layer.height })}
							/>
						</label>
						<label class="ctl-param" title="Y position">
							<span>Y</span>
							<input type="number" min="0" step="10"
								value={layer.y}
								onchange={(e) => onRectChange({ x: layer.x, y: parseInt((e.target as HTMLInputElement).value) || 0, width: layer.width, height: layer.height })}
							/>
						</label>
						<label class="ctl-param" title="Width">
							<span>W</span>
							<input type="number" min="1" step="10"
								value={layer.width}
								onchange={(e) => onRectChange({ x: layer.x, y: layer.y, width: parseInt((e.target as HTMLInputElement).value) || 0, height: layer.height })}
							/>
						</label>
						<label class="ctl-param" title="Height">
							<span>H</span>
							<input type="number" min="1" step="10"
								value={layer.height}
								onchange={(e) => onRectChange({ x: layer.x, y: layer.y, width: layer.width, height: parseInt((e.target as HTMLInputElement).value) || 0 })}
							/>
						</label>
					</div>
				{/if}

			<!-- ═══ TICKER MODE ═══ -->
			{:else if sourceMode === 'ticker'}
				<div class="ticker-section">
					<input
						class="field-inp"
						type="text"
						placeholder="Ticker text..."
						value={tickerConfig.text}
						oninput={(e) => onTickerConfigChange('text', (e.target as HTMLInputElement).value)}
						aria-label="Ticker text"
					/>
					<div class="ticker-params">
						<label class="ctl-param" title="Speed (px/s)">
							<span>spd</span>
							<input type="number" min="20" max="500" step="10" value={tickerConfig.speed} onchange={(e) => onTickerConfigChange('speed', Number((e.target as HTMLInputElement).value) || DEFAULT_TICKER.speed)} />
						</label>
						<label class="ctl-param" title="Font size">
							<span>sz</span>
							<input type="number" min="16" max="72" step="2" value={tickerConfig.fontSize} onchange={(e) => onTickerConfigChange('fontSize', Number((e.target as HTMLInputElement).value) || DEFAULT_TICKER.fontSize)} />
						</label>
						<label class="ctl-check" title="Bold">
							<input type="checkbox" checked={tickerConfig.bold} onchange={(e) => onTickerConfigChange('bold', (e.target as HTMLInputElement).checked)} />
							<span>B</span>
						</label>
						<label class="ctl-check" title="Loop">
							<input type="checkbox" checked={tickerConfig.loop} onchange={(e) => onTickerConfigChange('loop', (e.target as HTMLInputElement).checked)} />
							<span>Loop</span>
						</label>
						{#if tickerActive}
							<button class="act-btn anim-stop" onclick={onTickerStop}>STOP</button>
							<button class="act-btn fly" onclick={() => onTickerUpdateText(tickerText)}>UPDATE</button>
						{:else}
							<button class="act-btn anim-start" onclick={() => onTickerStart({ ...tickerConfig })}>START</button>
						{/if}
					</div>
				</div>

			<!-- ═══ SEQUENCE MODE ═══ -->
			{:else if sourceMode === 'sequence'}
				<div class="seq-section">
					{#if layer.sequenceProcessing}
						<div class="seq-status processing">
							<span class="seq-spinner"></span>
							<span>Processing... {Math.round((layer.sequenceProgress ?? 0) * 100)}%</span>
						</div>
						<div class="seq-progress-bar">
							<div class="seq-progress-fill" style="width: {(layer.sequenceProgress ?? 0) * 100}%"></div>
						</div>
					{:else if layer.sequenceLoaded}
						<div class="seq-status loaded">
							<span class="seq-badge">SEQ</span>
							<span>Sequence loaded</span>
							{#if layer.sequencePhase && layer.sequencePhase !== 'idle'}
								<span class="seq-phase-badge">{layer.sequencePhase.toUpperCase()}</span>
							{/if}
						</div>
						<div class="seq-speed">
							<label class="ctl-label">Speed</label>
							<input
								type="range"
								min="0.1" max="4" step="0.1"
								value={layer.sequenceSpeed ?? 1}
								oninput={(e) => onSequenceSpeed?.(parseFloat(e.currentTarget.value))}
								class="ctl-range"
							/>
							<span class="ctl-unit">{(layer.sequenceSpeed ?? 1).toFixed(1)}x</span>
						</div>
						<button class="act-btn del" onclick={() => onSequenceDelete?.()}>
							<span class="btn-icon">{@html renderIcon(ICON_TRASH, 11, 'stroke')}</span>
							DELETE SEQUENCE
						</button>
					{:else}
						<div class="seq-fps-row">
							<label class="ctl-label">Source FPS</label>
							<select class="ctl-select" bind:value={seqFPS}>
								<option value={23.976}>23.976</option>
								<option value={24}>24</option>
								<option value={25}>25</option>
								<option value={29.97}>29.97</option>
								<option value={30}>30</option>
								<option value={50}>50</option>
								<option value={59.94}>59.94</option>
								<option value={60}>60</option>
							</select>
						</div>
						<input
							type="file"
							accept=".zip"
							bind:this={seqFileInput}
							onchange={handleSeqFileSelect}
							style="display:none"
						/>
						<button class="act-btn upload" onclick={() => seqFileInput?.click()}>
							<span class="btn-icon">{@html renderIcon(ICON_UPLOAD, 11, 'stroke')}</span>
							UPLOAD ZIP
						</button>
						<p class="help-text">
							ZIP with PNG frames. Use in/ and out/ folders for animate on/off.
						</p>
					{/if}
				</div>

			<!-- ═══ HTML5 MODE ═══ -->
			{:else if sourceMode === 'html5'}
				<div class="html5-section">
					{#if html5Templates.length > 0}
						<select
							class="field-inp"
							aria-label="Select HTML5 template"
							onchange={(e) => {
								const val = (e.target as HTMLSelectElement).value;
								if (val && val !== '__custom__') {
									html5Url = val;
									const tmpl = html5Templates.find(t => t.url === val);
									html5LocalSchema = tmpl?.dataSchema ?? null;
									html5FormValues = {};
									const schemaFields = normalizeSchemaFields(tmpl?.dataSchema);
									if (schemaFields.length > 0) {
										const defaults: Record<string, any> = {};
										for (const f of schemaFields) defaults[f.key] = f.default ?? '';
										html5DataJson = JSON.stringify(defaults, null, 2);
									}
								} else {
									html5LocalSchema = null;
									html5FormValues = {};
								}
							}}
						>
							<option value="">-- Select template --</option>
							{#each html5Templates as tmpl}
								<option value={tmpl.url}>{tmpl.name} ({tmpl.format})</option>
							{/each}
							<option value="__custom__">Custom URL...</option>
						</select>
					{/if}

					<div class="html5-url-row">
						<input
							class="field-inp"
							type="text"
							placeholder="file:///path/to/template.html or http://..."
							bind:value={html5Url}
							aria-label="HTML5 template URL"
							onkeydown={(e) => { if (e.key === 'Enter') handleHTML5Load(); }}
						/>
						<button class="act-btn upload" onclick={handleHTML5Load}>LOAD</button>
					</div>

					{#if layer.html5Url}
						<div class="html5-status-row">
							<span class="html5-badge" style="background: {html5FormatColor(layer.html5Format)}">
								{html5FormatLabel(layer.html5Format)}
							</span>
							<span class="html5-badge" style="background: {html5StatusColor(layer.html5Status)}">
								{layer.html5Status ?? 'unknown'}
							</span>
							<button class="act-btn del html5-unload" onclick={onHTML5Unload}>UNLOAD</button>
						</div>

						{#if layer.html5Status === 'ready' || layer.html5Status === 'playing'}
							{#if hasSchema}
								<div class="html5-form">
									<div class="html5-form-fields">
										{#each html5Fields as field (field.key)}
											<label class="html5-field">
												<span class="html5-field-label">{field.label}</span>
												{#if field.type === 'text'}
													<input type="text" class="field-inp"
														bind:value={html5FormValues[field.key]}
													/>
												{:else if field.type === 'number'}
													<input type="number" class="field-inp"
														value={html5FormValues[field.key] ?? 0}
														oninput={(e) => { html5FormValues[field.key] = Number((e.target as HTMLInputElement).value) || 0; }}
													/>
												{:else if field.type === 'color'}
													<div class="html5-color-row">
														<input type="color"
															bind:value={html5FormValues[field.key]}
														/>
														<span class="html5-color-hex">{html5FormValues[field.key]}</span>
													</div>
												{:else if field.type === 'select'}
													<select class="field-inp"
														bind:value={html5FormValues[field.key]}>
														{#each (field.options ?? []) as opt}
															<option value={opt}>{opt}</option>
														{/each}
													</select>
												{:else if field.type === 'boolean'}
													<input type="checkbox"
														class="html5-checkbox"
														bind:checked={html5FormValues[field.key]}
													/>
												{:else if field.type === 'textarea'}
													<textarea class="field-inp html5-data-input"
														bind:value={html5FormValues[field.key]}
														rows="2"
													></textarea>
												{:else}
													<input type="text" class="field-inp"
														bind:value={html5FormValues[field.key]}
													/>
												{/if}
											</label>
										{/each}
									</div>
									{#if html5Actions.length > 0}
										<div class="html5-form-actions">
											{#each html5Actions as action (action.id)}
												<button class="act-btn fly" onclick={() => handleFormAction(action)}>
													{action.label}
												</button>
											{/each}
										</div>
									{/if}
									<button class="act-btn upload" onclick={handleFormUpdate}>UPDATE</button>
								</div>
							{:else}
								<div class="html5-data-section">
									<textarea
										class="html5-data-input"
										placeholder={'{"key": "value"}'}
										bind:value={html5DataJson}
										aria-label="HTML5 template data (JSON)"
										rows="3"
									></textarea>
									<button class="act-btn upload" onclick={handleHTML5UpdateData}>UPDATE</button>
								</div>
							{/if}

							<div class="html5-transport">
								<button class="act-btn anim-start" onclick={onHTML5Play} disabled={layer.html5Status === 'playing'}>PLAY</button>
								<button class="act-btn anim-stop" onclick={onHTML5Stop} disabled={layer.html5Status !== 'playing'}>STOP</button>
								<button class="act-btn fly" onclick={onHTML5Next}>NEXT</button>
							</div>
						{/if}
					{/if}
				</div>
			{/if}

			<!-- ═══ EFFECTS (any mode, when active) ═══ -->
			{#if layer.active}
				<div class="effects-section">
					<div class="effects-row">
						<button class="disclosure" onclick={() => { flyOpen = !flyOpen; if (flyOpen) animOpen = false; }} aria-expanded={flyOpen} class:active-disc={flyOpen}>
							<span class="disc-arrow">{@html renderIcon(flyOpen ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 10, 'stroke')}</span>
							FLY
						</button>
						<button class="disclosure" onclick={() => { animOpen = !animOpen; if (animOpen) flyOpen = false; }} aria-expanded={animOpen} class:active-disc={animOpen}>
							<span class="disc-arrow">{@html renderIcon(animOpen ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 10, 'stroke')}</span>
							ANIM
							{#if layer.animationMode}
								<span class="disclosure-badge">{layer.animationMode}</span>
							{/if}
						</button>
					</div>

					{#if flyOpen}
						<div class="collapse-content">
							<select
								class="ctl-select"
								value={flyConfig.direction}
								onchange={(e) => onFlyConfigChange('direction', (e.target as HTMLSelectElement).value)}
								aria-label="Fly direction"
							>
								<option value="left">Left</option>
								<option value="right">Right</option>
								<option value="top">Top</option>
								<option value="bottom">Bottom</option>
							</select>
							<input
								class="ctl-num"
								type="number"
								min="100"
								max="3000"
								step="100"
								value={flyConfig.durationMs}
								oninput={(e) => onFlyConfigChange('durationMs', parseInt((e.target as HTMLInputElement).value) || 500)}
								aria-label="Fly duration"
							/>
							<span class="ctl-unit">ms</span>
							<button class="act-btn fly" onclick={onFlyIn} disabled={busy}>FLY IN</button>
							<button class="act-btn fly" onclick={onFlyOut} disabled={!layer.active || busy}>FLY OUT</button>
						</div>
					{/if}

					{#if animOpen}
						<div class="collapse-content">
							<select
								class="ctl-select"
								value={animConfig.mode}
								onchange={(e) => onAnimConfigChange('mode', (e.target as HTMLSelectElement).value)}
								aria-label="Animation mode"
							>
								<option value="pulse">Pulse</option>
								<option value="transition">Transition</option>
							</select>
							{#if animConfig.mode === 'pulse'}
								<label class="ctl-param" title="Min opacity">
									<span>min</span>
									<input type="number" min="0" max="1" step="0.1"
										value={animConfig.minAlpha}
										oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onAnimConfigChange('minAlpha', Number.isNaN(v) ? 0 : v); }}
									/>
								</label>
								<label class="ctl-param" title="Max opacity">
									<span>max</span>
									<input type="number" min="0" max="1" step="0.1"
										value={animConfig.maxAlpha}
										oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onAnimConfigChange('maxAlpha', Number.isNaN(v) ? 1 : v); }}
									/>
								</label>
								<label class="ctl-param" title="Speed (Hz)">
									<span>Hz</span>
									<input type="number" min="0.1" max="5" step="0.1"
										value={animConfig.speedHz}
										oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onAnimConfigChange('speedHz', Number.isNaN(v) ? 1 : v); }}
									/>
								</label>
							{:else}
								<label class="ctl-param" title="Target opacity">
									<span>alpha</span>
									<input type="number" min="0" max="1" step="0.1"
										value={animConfig.toAlpha}
										oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onAnimConfigChange('toAlpha', Number.isNaN(v) ? 0.5 : v); }}
									/>
								</label>
								<label class="ctl-param" title="Duration (ms)">
									<span>ms</span>
									<input type="number" min="100" max="5000" step="100"
										value={animConfig.durationMs}
										oninput={(e) => onAnimConfigChange('durationMs', parseInt((e.target as HTMLInputElement).value) || 500)}
									/>
								</label>
							{/if}
							{#if layer.animationMode}
								<button class="act-btn anim-stop" onclick={onAnimateStop}>STOP</button>
							{:else}
								<button class="act-btn anim-start" onclick={onAnimate} disabled={!layer.active || busy}>ANIMATE</button>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
		</div>
	</div>

	<!-- ═══ FIXED DSK FOOTER ═══ -->
	<div class="dsk-footer" class:active={layer.active}>
		<button class="dsk-btn on" onclick={onCutOn} disabled={layer.active || busy}>CUT ON</button>
		<button class="dsk-btn auto-on" onclick={onAutoOn} disabled={layer.active || busy}>AUTO ON</button>
		<button class="dsk-btn off" onclick={onCutOff} disabled={!layer.active || busy}>CUT OFF</button>
		<button class="dsk-btn auto-off" onclick={onAutoOff} disabled={!layer.active || busy}>AUTO OFF</button>
	</div>
</div>

<style>
	.detail-pane {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	/* ── Mode segmented control ── */
	.mode-bar {
		display: flex;
		gap: 1px;
		padding: 4px 6px;
		flex-shrink: 0;
		background: var(--bg-base);
		border-bottom: 1px solid var(--border-subtle);
	}

	.mode-seg {
		display: flex;
		align-items: center;
		gap: 3px;
		position: relative;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-tertiary);
		background: transparent;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		padding: 3px 8px;
		cursor: pointer;
		transition: all var(--transition-fast);
	}

	.mode-seg:hover {
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	.mode-seg.active {
		color: var(--text-primary);
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.mode-icon {
		display: flex;
	}

	.mode-label {
		white-space: nowrap;
	}

	.mode-dot {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.mode-dot.loaded {
		background: var(--color-success);
	}

	.mode-dot.live {
		background: var(--tally-program);
		animation: dot-blink 1.2s ease-in-out infinite;
	}

	@keyframes dot-blink {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.3; }
	}

	/* ── Scrollable content area ── */
	.content-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.content-body {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 4px 6px;
	}

	/* ── Template layout: controls + preview side by side ── */
	.tpl-layout {
		display: flex;
		gap: 6px;
	}

	.tpl-controls {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.preview-col {
		flex-shrink: 0;
		width: 200px;
		display: flex;
		flex-direction: column;
		align-items: center;
	}

	.detail-preview {
		width: 100%;
		aspect-ratio: 16 / 9;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: #0a0a0a;
		object-fit: contain;
	}

	/* ── Template strip ── */
	.tpl-strip {
		display: flex;
		gap: 2px;
		overflow-x: auto;
		flex-shrink: 0;
	}

	.tpl-card {
		position: relative;
		display: flex;
		align-items: center;
		justify-content: center;
		min-width: 46px;
		height: 22px;
		padding: 0 5px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 0.5rem;
		font-weight: 500;
		letter-spacing: 0.02em;
		overflow: hidden;
		transition: border-color var(--transition-fast), background var(--transition-fast), color var(--transition-fast);
	}

	.tpl-card:hover {
		background: var(--bg-elevated);
		color: var(--text-primary);
	}

	.tpl-card.selected {
		border-color: var(--tpl-accent);
		color: var(--text-primary);
		background: var(--bg-elevated);
	}

	.tpl-accent-bar {
		position: absolute;
		top: 0;
		left: 0;
		right: 0;
		height: 2px;
		background: var(--tpl-accent);
		opacity: 0.4;
		transition: opacity var(--transition-fast);
	}

	.tpl-card.selected .tpl-accent-bar {
		opacity: 1;
	}

	.tpl-card-name {
		white-space: nowrap;
	}

	/* ── Field editors ── */
	.fields-section {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.fields-section.grid-2 {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 2px;
	}

	.fields-section.grid-3 {
		display: grid;
		grid-template-columns: 1fr 1fr 1fr;
		gap: 2px;
	}

	.field-item {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.field-lbl {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.field-inp {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 5px;
		width: 100%;
	}

	.field-inp:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	/* ── DSK footer (always visible) ── */
	.dsk-footer {
		display: grid;
		grid-template-columns: repeat(4, 1fr);
		gap: 2px;
		padding: 4px 6px;
		flex-shrink: 0;
		border-top: 1px solid var(--border-subtle);
		background: var(--bg-base);
	}

	.dsk-footer.active {
		border-top-color: var(--color-success);
	}

	.dsk-btn {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.03em;
		padding: 4px 2px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
		transition: border-color var(--transition-fast), background var(--transition-fast);
		white-space: nowrap;
	}

	.dsk-btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.dsk-footer.active .dsk-btn.off:not(:disabled),
	.dsk-footer.active .dsk-btn.auto-off:not(:disabled) {
		border-color: var(--color-success);
		background: color-mix(in srgb, var(--color-success) 15%, var(--bg-elevated));
	}

	.dsk-btn.on:not(:disabled):hover,
	.dsk-btn.auto-on:not(:disabled):hover {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
	}

	.dsk-btn.off:not(:disabled):hover,
	.dsk-btn.auto-off:not(:disabled):hover {
		border-color: var(--color-success);
		background: color-mix(in srgb, var(--color-success) 25%, var(--bg-elevated));
	}

	/* ── Action buttons (shared) ── */
	.act-btn {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		letter-spacing: 0.03em;
		padding: 3px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
		transition: border-color var(--transition-fast), background var(--transition-fast);
		white-space: nowrap;
		display: inline-flex;
		align-items: center;
		gap: 3px;
	}

	.act-btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.btn-icon {
		display: flex;
	}

	.act-btn.upload:not(:disabled):hover,
	.act-btn.fly:not(:disabled):hover {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.act-btn.del:not(:disabled):hover {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
	}

	.act-btn.anim-start:not(:disabled):hover {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.act-btn.anim-stop {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
		color: #fff;
	}

	.act-btn.anim-stop:hover {
		background: var(--tally-program-light);
	}

	/* ── Image section ── */
	.image-section {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-wrap: wrap;
	}

	.image-info {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
	}

	.file-input-hidden {
		display: none;
	}

	/* ── Rect controls (compact horizontal row) ── */
	.rect-row {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	/* ── Ticker section ── */
	.ticker-section {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.ticker-params {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-wrap: wrap;
	}

	/* ── Effects section ── */
	.effects-section {
		border-top: 1px solid var(--border-subtle);
		padding-top: 4px;
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.effects-row {
		display: flex;
		gap: 3px;
	}

	.disclosure {
		display: flex;
		align-items: center;
		gap: 2px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-tertiary);
		text-transform: uppercase;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		cursor: pointer;
		padding: 2px 6px;
		transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
	}

	.disclosure:hover {
		color: var(--text-primary);
		border-color: var(--border-prominent, var(--border-default));
	}

	.disclosure.active-disc {
		color: var(--text-primary);
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.disc-arrow {
		display: flex;
	}

	.disclosure-badge {
		font-family: var(--font-mono);
		font-size: 0.4rem;
		font-weight: 600;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		padding: 0 3px;
		border-radius: 2px;
	}

	.collapse-content {
		display: flex;
		align-items: center;
		gap: 3px;
		flex-wrap: wrap;
		padding: 2px 0;
	}

	/* ── Shared controls ── */
	.ctl-select {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
	}

	.ctl-num {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
		width: 46px;
	}

	.ctl-unit {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.ctl-param {
		display: flex;
		align-items: center;
		gap: 2px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-secondary);
	}

	.ctl-param input {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 1px 3px;
		width: 36px;
	}

	.ctl-check {
		display: flex;
		align-items: center;
		gap: 2px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-secondary);
		cursor: pointer;
	}

	.ctl-check input[type="checkbox"] {
		width: 12px;
		height: 12px;
		accent-color: var(--accent-blue);
	}

	/* ── HTML5 section ── */
	.html5-section {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.html5-url-row {
		display: flex;
		gap: 3px;
		align-items: center;
	}

	.html5-url-row .field-inp {
		flex: 1;
	}

	.html5-status-row {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-wrap: wrap;
	}

	.html5-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: #fff;
		padding: 1px 5px;
		border-radius: 3px;
		white-space: nowrap;
	}

	.html5-unload {
		font-size: var(--text-2xs);
		padding: 1px 5px;
		margin-left: auto;
	}

	.html5-data-section {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.html5-data-input {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 3px 5px;
		width: 100%;
		resize: vertical;
		min-height: 40px;
	}

	.html5-data-input:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.html5-form {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.html5-form-fields {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 3px 6px;
	}

	.html5-field {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.html5-field-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.html5-color-row {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.html5-color-row input[type="color"] {
		width: 28px;
		height: 20px;
		padding: 0;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: none;
		cursor: pointer;
	}

	.html5-color-hex {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
	}

	.html5-checkbox {
		width: 14px;
		height: 14px;
		accent-color: var(--accent-blue);
	}

	.html5-form-actions {
		display: flex;
		flex-wrap: wrap;
		gap: 3px;
	}

	.html5-transport {
		display: flex;
		gap: 3px;
	}

	/* ── Sequence section ── */
	.seq-section {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.seq-status {
		display: flex;
		align-items: center;
		gap: 6px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
	}

	.seq-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: #fff;
		background: var(--color-success);
		padding: 1px 5px;
		border-radius: 3px;
		white-space: nowrap;
	}

	.seq-phase-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		padding: 1px 5px;
		border-radius: 3px;
		white-space: nowrap;
	}

	.seq-spinner {
		width: 12px;
		height: 12px;
		border: 2px solid var(--border-default);
		border-top-color: var(--accent-blue);
		border-radius: 50%;
		animation: seq-spin 0.8s linear infinite;
	}

	@keyframes seq-spin {
		to { transform: rotate(360deg); }
	}

	.seq-fps-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.seq-progress-bar {
		height: 4px;
		background: var(--bg-tertiary);
		border-radius: 2px;
		overflow: hidden;
	}

	.seq-progress-fill {
		height: 100%;
		background: var(--accent-blue);
		border-radius: 2px;
		transition: width 0.3s ease-out;
	}

	.help-text {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		line-height: 1.4;
		margin: 0;
	}

	.mode-dot.processing {
		background: var(--accent-blue);
		animation: dot-blink 0.8s ease-in-out infinite;
	}
</style>
