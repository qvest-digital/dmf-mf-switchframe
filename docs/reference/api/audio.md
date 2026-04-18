# API: audio

> **Where you are:** [docs](../../../README.md) → reference → api → audio
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/audio.md](../../subsystems/audio.md) · [api/preset.md](preset.md)

**TL;DR** Nineteen endpoints cover the audio mixer: per-channel level, trim, mute, AFV, phase, balance, 3-band EQ, compressor, HPF, noise gate, and lip-sync delay, plus master bus level and a master brickwall limiter. Per-channel reads return typed responses (EQ bands, compressor stats, HPF state, gate state) separate from the full state broadcast. All mutations are wrapped by the timed command queue so dual-engine deployments keep audio changes frame-aligned.

Most of these endpoints accept a `source` either in the request body (for the historical top-level routes under `/api/audio/<verb>`) or as a path parameter (for the newer, RESTful `/api/audio/{source}/<verb>` routes). Both forms route to the same audio mixer interface.

All mutating endpoints return the full `ControlRoomState` on success. All of them return 501 if the mixer isn't configured (non-cgo build without fdk-aac). Range violations return 400 with a specific error message.

## Endpoints

### `POST /api/audio/level`

**Purpose:** set a channel's fader level in dBFS.
**Auth:** required.
**Handler:** [`control/api_audio.go`](../../../server/control/api_audio.go) → `(*API).handleAudioLevelInner` (`timedHandler`).

**Request body** (`control.audioLevelRequest`): `{ "source": "cam1", "level": -6.0 }`. Level range: `MinLevelDB` to `MaxLevelDB` (roughly -∞ to +12).

**Response 200:** `ControlRoomState`.

**Errors:** 400 (empty source, out-of-range level), 404 (`audio.ErrChannelNotFound`), 501.

**Emits:** state broadcast with updated `AudioChannels[source].Level`.

---

### `POST /api/audio/trim`

**Purpose:** input gain trim (-20 to +20 dB).
**Auth:** required.
**Handler:** [`control/api_audio.go`](../../../server/control/api_audio.go) → `(*API).handleAudioTrimInner`.

**Request body** (`control.audioTrimRequest`): `{ "source": "cam1", "trim": 3.0 }`. Range: `MinTrimDB` to `MaxTrimDB`.

**Errors:** 400 (invalid trim), 404, 501.

---

### `POST /api/audio/mute`

**Purpose:** mute/unmute a channel.
**Auth:** required.
**Handler:** `(*API).handleAudioMuteInner`.

**Request body** (`control.audioMuteRequest`): `{ "source": "cam1", "muted": true }`.

---

### `POST /api/audio/afv`

**Purpose:** toggle Audio-Follows-Video for a channel. When AFV is enabled, a hard cut or transition to this source automatically un-mutes it (and mutes the previous program source if it was AFV).
**Auth:** required.
**Handler:** `(*API).handleAudioAFVInner`.

**Request body** (`control.audioAFVRequest`): `{ "source": "cam1", "afv": true }`.

---

### `POST /api/audio/phase`

**Purpose:** 180° phase-invert a channel.
**Auth:** required.
**Handler:** `(*API).handleAudioPhaseInner`.

**Request body** (`control.audioPhaseRequest`): `{ "source": "cam1", "invert": true }`.

---

### `POST /api/audio/balance`

**Purpose:** stereo balance (-1.0 left, 0 center, +1.0 right).
**Auth:** required.
**Handler:** `(*API).handleAudioBalanceInner`.

**Request body** (`control.audioBalanceRequest`): `{ "source": "cam1", "balance": 0.0 }`. Range -1 to +1.

---

### `POST /api/audio/master`

**Purpose:** master bus fader level.
**Auth:** required.
**Handler:** `(*API).handleAudioMasterInner`.

**Request body** (`control.audioMasterRequest`): `{ "level": -3.0 }`.

---

### `PUT /api/audio/{source}/eq`

**Purpose:** configure one of three EQ bands (`0`=low-shelf, `1`=mid-peak, `2`=high-shelf).
**Auth:** required.
**Handler:** `(*API).handleSetEQInner` (`timedHandlerWithPath`).

**Request body** (`control.eqRequest`): `{ "band": 1, "frequency": 1000, "gain": 3.0, "q": 0.7, "enabled": true }`.

Range checks: frequency `MinEQFreq`-`MaxEQFreq`, gain `MinEQGainDB`-`MaxEQGainDB`, q `MinEQQ`-`MaxEQQ`. Invalid band returns `audio.ErrInvalidBand`.

---

### `GET /api/audio/{source}/eq`

**Purpose:** return the current 3-band EQ settings for a source.
**Auth:** required.
**Handler:** `(*API).handleGetEQ`.

**Response 200:** `[3]audio.EQBandSettings`.

---

### `PUT /api/audio/{source}/compressor`

**Purpose:** configure the per-channel compressor.
**Auth:** required.
**Handler:** `(*API).handleSetCompressorInner`.

**Request body** (`control.compressorRequest`):

```json
{ "threshold": -12, "ratio": 3, "attack": 5, "release": 100, "makeupGain": 3 }
```

Ranges: threshold -40 to 0 dB, ratio 1-20, attack 0.1-100 ms, release 10-1000 ms, makeup 0-24 dB.

---

### `GET /api/audio/{source}/compressor`

**Purpose:** return compressor settings plus current gain reduction.
**Auth:** required.
**Handler:** `(*API).handleGetCompressor`.

**Response 200** (`control.compressorResponse`):

```json
{ "threshold": -12, "ratio": 3, "attack": 5, "release": 100, "makeupGain": 3, "gainReduction": 2.1 }
```

---

### `PUT /api/audio/{source}/hpf`

**Purpose:** enable a high-pass filter on a channel.
**Auth:** required.
**Handler:** `(*API).handleSetHPFInner`.

**Request body** (`control.hpfRequest`): `{ "frequency": 80, "enabled": true }`. Range: `MinHPFFreq` to `MaxHPFFreq`.

---

### `GET /api/audio/{source}/hpf`

**Purpose:** current HPF settings.
**Auth:** required.
**Handler:** `(*API).handleGetHPF`.

**Response 200** (`control.hpfResponse`): `{ "frequency": 80, "enabled": true }`.

---

### `PUT /api/audio/{source}/gate`

**Purpose:** configure a noise gate (expander).
**Auth:** required.
**Handler:** `(*API).handleSetGateInner`.

**Request body** (`control.gateRequest`):

```json
{ "threshold": -40, "range": 20, "attack": 5, "release": 100, "hold": 50, "enabled": true }
```

Ranges are defined in `control.MinGate*` / `MaxGate*` constants.

---

### `GET /api/audio/{source}/gate`

**Purpose:** gate settings plus live gain reduction.
**Auth:** required.
**Handler:** `(*API).handleGetGate`.

**Response 200** (`control.gateResponse`): adds `gainReduction` to the request shape.

---

### `PUT /api/audio/{source}/audio-delay`

**Purpose:** lip-sync delay (0-500 ms) applied independently of the per-source video delay.
**Auth:** required.
**Handler:** `(*API).handleSetAudioDelayInner`.

**Request body** (`control.audioDelayRequest`): `{ "delayMs": 120 }`.

**Related:** [api/source.md#post-apisourceskeydelay](source.md) — the paired video-side delay endpoint.

---

### `PUT /api/audio/limiter`

**Purpose:** configure the master-bus brickwall limiter.
**Auth:** required.
**Handler:** `(*API).handleSetLimiterInner`.

**Request body** (`control.limiterRequest`): `{ "threshold": -3, "ceiling": -1 }`.

---

### `GET /api/audio/limiter`

**Purpose:** current limiter settings plus live gain reduction.
**Auth:** required.
**Handler:** `(*API).handleGetLimiter`.

**Response 200** (`control.limiterResponse`): `{ "threshold": -3, "ceiling": -1, "gainReduction": 0.4 }`.

## Related docs

- **Concepts:** [audio mixer architecture](../../subsystems/audio.md)
- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#audiochannel)
- **Subsystems:** [audio.md](../../subsystems/audio.md) · [control-plane.md](../../subsystems/control-plane.md)
