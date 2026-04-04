const MSG_LAYOUT_SLOT_POSITION = 0x01;
const MSG_TRANSITION_POSITION = 0x02;
const MSG_GRAPHICS_LAYER_POSITION = 0x03;
const MSG_DVE_SLOT_TRANSFORM = 0x04;
const MSG_DVE_SINGLE_PARAM = 0x05;

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

export interface FastControl {
	sendSlotPosition(slotId: number, x: number, y: number, w: number, h: number): void;
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
	close(): void;
}

export function encodeSlotPosition(
	slotId: number,
	x: number,
	y: number,
	w: number,
	h: number,
): Uint8Array {
	const buf = new ArrayBuffer(10);
	const view = new DataView(buf);
	view.setUint8(0, MSG_LAYOUT_SLOT_POSITION);
	view.setUint8(1, slotId);
	view.setUint16(2, x);
	view.setUint16(4, y);
	view.setUint16(6, w);
	view.setUint16(8, h);
	return new Uint8Array(buf);
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

export function createFastControl(transport: WebTransport): FastControl {
	const writer = transport.datagrams.writable.getWriter();

	return {
		sendSlotPosition(slotId: number, x: number, y: number, w: number, h: number) {
			writer.write(encodeSlotPosition(slotId, x, y, w, h)).catch(() => {});
		},

		sendTransitionPosition(position: number) {
			writer.write(encodeTransitionPosition(position)).catch(() => {});
		},

		sendGraphicsLayerPosition(layerId: number, x: number, y: number, w: number, h: number) {
			writer.write(encodeGraphicsLayerPosition(layerId, x, y, w, h)).catch(() => {});
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
			writer
				.write(encodeDVESlotTransform(slotId, posX, posY, width, height, rotation, opacity))
				.catch(() => {});
		},

		sendDVESingleParam(slotId: number, paramId: number, value: number) {
			writer.write(encodeDVESingleParam(slotId, paramId, value)).catch(() => {});
		},

		close() {
			writer.releaseLock();
		},
	};
}
