const MSG_TRANSITION_POSITION = 0x02;
const MSG_GRAPHICS_LAYER_POSITION = 0x03;
const MSG_DVE_SLOT_TRANSFORM = 0x04;
const MSG_DVE_SINGLE_PARAM = 0x05;
const MSG_DVE_SLOT_SOURCE = 0x06;
const MSG_DVE_SLOT_ENABLE = 0x07;
const MSG_DVE_SCALE_MODE = 0x08;
const MSG_DVE_BORDER = 0x09;

// DVE single-parameter IDs for knob/slider control.
export const DVE_PARAM_POSITION_X = 0x0000;
export const DVE_PARAM_POSITION_Y = 0x0001;
export const DVE_PARAM_WIDTH = 0x0002;
export const DVE_PARAM_HEIGHT = 0x0003;
export const DVE_PARAM_ROTATION_DEG = 0x0004;
export const DVE_PARAM_PERSPECTIVE_X = 0x0005;
export const DVE_PARAM_PERSPECTIVE_Y = 0x0006;
export const DVE_PARAM_CROP_TOP = 0x0008;
export const DVE_PARAM_CROP_BOTTOM = 0x0009;
export const DVE_PARAM_CROP_LEFT = 0x000a;
export const DVE_PARAM_CROP_RIGHT = 0x000b;
export const DVE_PARAM_OPACITY = 0x000c;
export const DVE_PARAM_DEFOCUS_RADIUS = 0x000d;
export const DVE_PARAM_BORDER_WIDTH = 0x000e;
export const DVE_PARAM_CORNER_RADIUS = 0x000f;

export type WriteErrorCallback = (msgType: string, engineId?: string) => void;

export interface FastControl {
	sendTransitionPosition(position: number): void;
	sendGraphicsLayerPosition(layerId: number, x: number, y: number, w: number, h: number): void;
	sendDVESlotTransform(
		slotId: number,
		posX: number,
		posY: number,
		width: number,
		height: number,
		rotation: number,
		opacity: number,
	): void;
	sendDVESingleParam(slotId: number, paramId: number, value: number): void;
	sendDVESlotSource(slotId: number, sourceKey: string): void;
	sendDVESlotEnable(slotId: number, enabled: boolean): void;
	sendDVEScaleMode(slotId: number, mode: number, anchorX: number, anchorY: number): void;
	sendDVEBorder(
		slotId: number,
		width: number,
		radius: number,
		colorY: number,
		colorCb: number,
		colorCr: number,
	): void;
	/** Write raw datagram bytes through this control's shared writer (for clock sync). */
	writeDatagram(data: Uint8Array): void;
	close(): void;
	/** REST commit for reliable final transition position on mouseup/touchend. */
	commitTransitionPosition?(position: number): void;
	/** REST commit for reliable final DVE slot transform on mouseup/touchend. */
	commitDVESlotTransform?(slotId: number, posX: number, posY: number, width: number, height: number, rotation: number, opacity: number): void;
}

export interface FastControlOptions {
	onWriteError?: WriteErrorCallback;
	ptpNow?: () => number;
	engineId?: string;
	/** REST commit callback for reliable final transition position delivery. */
	commitTransitionPosition?: (position: number) => void;
	/** REST commit callback for reliable final DVE slot transform delivery. */
	commitDVESlotTransform?: (slotId: number, posX: number, posY: number, width: number, height: number, rotation: number, opacity: number) => void;
}

export function encodeTransitionPosition(position: number): Uint8Array {
	const buf = new ArrayBuffer(5);
	const view = new DataView(buf);
	view.setUint8(0, MSG_TRANSITION_POSITION);
	view.setFloat32(1, position);
	return new Uint8Array(buf);
}

export function encodeGraphicsLayerPosition(
	layerId: number,
	x: number,
	y: number,
	w: number,
	h: number,
): Uint8Array {
	const buf = new ArrayBuffer(11);
	const view = new DataView(buf);
	view.setUint8(0, MSG_GRAPHICS_LAYER_POSITION);
	view.setUint16(1, layerId);
	view.setUint16(3, x);
	view.setUint16(5, y);
	view.setUint16(7, w);
	view.setUint16(9, h);
	return new Uint8Array(buf);
}

export function encodeDVESlotTransform(
	slotId: number,
	posX: number,
	posY: number,
	width: number,
	height: number,
	rotation: number,
	opacity: number,
): Uint8Array {
	const buf = new ArrayBuffer(26);
	const view = new DataView(buf);
	view.setUint8(0, MSG_DVE_SLOT_TRANSFORM);
	view.setUint8(1, slotId);
	view.setFloat32(2, posX);
	view.setFloat32(6, posY);
	view.setFloat32(10, width);
	view.setFloat32(14, height);
	view.setFloat32(18, rotation);
	view.setFloat32(22, opacity);
	return new Uint8Array(buf);
}

export function encodeDVESingleParam(
	slotId: number,
	paramId: number,
	value: number,
): Uint8Array {
	const buf = new ArrayBuffer(8);
	const view = new DataView(buf);
	view.setUint8(0, MSG_DVE_SINGLE_PARAM);
	view.setUint8(1, slotId);
	view.setUint16(2, paramId);
	view.setFloat32(4, value);
	return new Uint8Array(buf);
}

export function encodeDVESlotSource(slotId: number, sourceKey: string): Uint8Array {
	const keyBytes = new TextEncoder().encode(sourceKey);
	const buf = new ArrayBuffer(3 + keyBytes.length);
	const view = new DataView(buf);
	const arr = new Uint8Array(buf);
	view.setUint8(0, MSG_DVE_SLOT_SOURCE);
	view.setUint8(1, slotId);
	arr[2] = keyBytes.length;
	arr.set(keyBytes, 3);
	return new Uint8Array(buf);
}

export function encodeDVESlotEnable(slotId: number, enabled: boolean): Uint8Array {
	const buf = new ArrayBuffer(3);
	const view = new DataView(buf);
	view.setUint8(0, MSG_DVE_SLOT_ENABLE);
	view.setUint8(1, slotId);
	view.setUint8(2, enabled ? 1 : 0);
	return new Uint8Array(buf);
}

export function encodeDVEScaleMode(
	slotId: number,
	mode: number,
	anchorX: number,
	anchorY: number,
): Uint8Array {
	const buf = new ArrayBuffer(11);
	const view = new DataView(buf);
	view.setUint8(0, MSG_DVE_SCALE_MODE);
	view.setUint8(1, slotId);
	view.setUint8(2, mode);
	view.setFloat32(3, anchorX);
	view.setFloat32(7, anchorY);
	return new Uint8Array(buf);
}

export function encodeDVEBorder(
	slotId: number,
	width: number,
	radius: number,
	colorY: number,
	colorCb: number,
	colorCr: number,
): Uint8Array {
	const buf = new ArrayBuffer(13);
	const view = new DataView(buf);
	view.setUint8(0, MSG_DVE_BORDER);
	view.setUint8(1, slotId);
	view.setFloat32(2, width);
	view.setFloat32(6, radius);
	view.setUint8(10, colorY);
	view.setUint8(11, colorCb);
	view.setUint8(12, colorCr);
	return new Uint8Array(buf);
}

export function createFastControl(transport: WebTransport, opts?: FastControlOptions): FastControl {
	const writer = transport.datagrams.writable.getWriter();
	const onErr = opts?.onWriteError;
	let closed = false;
	let pendingWrite: Promise<void> | null = null;

	function withTimestamp(data: Uint8Array): Uint8Array {
		if (!opts?.ptpNow) return data;
		const ts = opts.ptpNow();
		if (ts <= 0) return data;
		const buf = new Uint8Array(8 + data.length);
		const view = new DataView(buf.buffer);
		const hi = Math.floor(ts / 2 ** 32);
		const lo = ts % 2 ** 32;
		view.setUint32(0, hi);
		view.setUint32(4, lo);
		buf.set(data, 8);
		return buf;
	}

	function write(data: Uint8Array, msgType: string) {
		if (closed) return;
		pendingWrite = writer.write(withTimestamp(data)).catch(() => {
			onErr?.(msgType, opts?.engineId);
		}).finally(() => {
			pendingWrite = null;
		});
	}

	return {
		sendTransitionPosition(position: number) {
			write(encodeTransitionPosition(position), 'transition_position');
		},

		sendGraphicsLayerPosition(layerId: number, x: number, y: number, w: number, h: number) {
			write(encodeGraphicsLayerPosition(layerId, x, y, w, h), 'graphics_layer_position');
		},

		sendDVESlotTransform(
			slotId: number,
			posX: number,
			posY: number,
			width: number,
			height: number,
			rotation: number,
			opacity: number,
		) {
			write(encodeDVESlotTransform(slotId, posX, posY, width, height, rotation, opacity), 'dve_slot_transform');
		},

		sendDVESingleParam(slotId: number, paramId: number, value: number) {
			write(encodeDVESingleParam(slotId, paramId, value), 'dve_single_param');
		},

		sendDVESlotSource(slotId: number, sourceKey: string) {
			write(encodeDVESlotSource(slotId, sourceKey), 'dve_slot_source');
		},

		sendDVESlotEnable(slotId: number, enabled: boolean) {
			write(encodeDVESlotEnable(slotId, enabled), 'dve_slot_enable');
		},

		sendDVEScaleMode(slotId: number, mode: number, anchorX: number, anchorY: number) {
			write(encodeDVEScaleMode(slotId, mode, anchorX, anchorY), 'dve_scale_mode');
		},

		sendDVEBorder(
			slotId: number,
			width: number,
			radius: number,
			colorY: number,
			colorCb: number,
			colorCr: number,
		) {
			write(encodeDVEBorder(slotId, width, radius, colorY, colorCb, colorCr), 'dve_border');
		},

		writeDatagram(data: Uint8Array) {
			if (closed) return;
			writer.write(data).catch(() => {});
		},

		commitTransitionPosition: opts?.commitTransitionPosition,
		commitDVESlotTransform: opts?.commitDVESlotTransform,

		close() {
			closed = true;
			if (pendingWrite) {
				pendingWrite.catch(() => {}).finally(() => {
					writer.releaseLock();
				});
			} else {
				writer.releaseLock();
			}
		},
	};
}
