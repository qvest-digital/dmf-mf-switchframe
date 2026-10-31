# Frame synchronization and timing

> **Where you are:** [docs](../README.md) → concepts → frame-sync-and-timing
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [media-path](media-path.md) · [pipeline](pipeline.md) · [locking-and-concurrency](locking-and-concurrency.md)

**TL;DR** - Every timestamp is media time on TAI. A reference clock ticks once per program frame on the TAI grain grid; on each tick the [sync loop](../../server/switcher/sync_loop.go) takes, per source, the newest frame whose media time is at or before the tick's read point, two frames behind the clock, and the audio of that same media time, and stamps both with the tick's output time. The program is CFR by construction and carries no A/V offset.

## Media time

[`clock/mediatime.go`](../../server/clock/mediatime.go) converts between TAI nanoseconds, flow indices and 90 kHz PTS with the rounding libmxl uses (`mxlTimestampToIndex`, `mxlIndexToTimestamp`). TAI comes from `mxlGetTime` when the binary is built with libmxl and from the system clock otherwise. PTS is on an engine timeline: `clock.PTSBase` (90000) is the TAI of the first conversion in the process.

How a source's frames get their media time:

| Source | PTS |
| --- | --- |
| MXL | the grain index's TAI ([`mxl/reader.go`](../../server/mxl/reader.go)). A skipped or invalid grain costs exactly its own time. An audio read ending at index `i` covers `[i-n, i)` and is stamped with sample `i-n`. |
| SRT, ST 2110 | the stream's own timestamps, placed with [`clock.Anchor`](../../server/clock/mediatime.go): the first one at the TAI it arrives, later ones at their distance to it, so the source keeps its own A/V relation. A jump of more than 2 s from the clock places the source again. |
| everything else (clips, playout, replay, built-ins, decoded relay sources) | the TAI of arrival, set in [`Switcher.IngestRawVideo`](../../server/switcher/switcher.go). |

## The reference clock

[`ReferenceClock`](../../server/clock/reference.go) delivers tick `n` at the TAI start of program grain `n`. A tick carries:

- `VideoPTS`: grain `n`'s PTS.
- `ReadPTS`: grain `n - clock.ReaderDelayGrains` (2). That is the newest media time the tick shows, and the budget for a producer committing late plus transit and pacing.
- `AudioStart` and `AudioSamples`: the sample range `[I(Ra, T(Rv, n)), I(Ra, T(Rv, n+1)))`. At NTSC rates consecutive ticks tile the sample timeline with no rounding drift.
- `AudioPTS` and `ReadSample`: the PTS of `AudioStart` and the sample index of the read point.

A clock that falls behind resumes at the current grain and counts the ticks it skipped, so a tick never carries another grain's time. The switcher owns the clock (`Switcher.Start`). It replaces the clock when the pipeline frame rate changes.

## Frame synchronizer and sync loop

[`FrameSynchronizer`](../../server/switcher/frame_sync.go) keeps a PTS-ordered ring of decoded frames per source. A tick takes the newest frame with PTS at or before `ReadPTS`, less the source's delay (`SetSourceDelay`). It releases the older frames and keeps the newer ones. A frame whose PTS is not after the newest buffered one flushes the ring, as a producer restart does.

[`processTick`](../../server/switcher/sync_loop.go) runs audio first, then video:

- Video shows the program source's due frame. With nothing new it repeats the last frame. After 5 s with nothing (`sourceLossTimeout`), or for a source that never delivered, it shows black. During an FTB hold it shows black. Every frame is stamped with `VideoPTS`. A source slower or faster than the program rate is repeated or dropped there; there is no frame-rate conversion.
- Transitions advance one tick at a time and set the audio crossfade position on every tick.
- Under overload, the loop repeats the last encoded frame for the ticks it is behind.
- The GPU pipeline uses the source manager's cached frame of the exact PTS. [`gpu/source_manager.go`](../../server/gpu/source_manager.go) keeps the last four frames per source. Any other frame (a repeat older than those, or black) is uploaded.

## Program audio

Each tick calls `Mixer.ProduceSamples(tick.ReadSample, tick.AudioSamples)`. Every channel's ring is stamped with the media time of its newest samples on push. `popAt` reads the ring at the read point. A ring more than one period off is moved onto the read point: older audio is dropped, and audio that is not due yet waits behind silence. Each move is counted per source. This is what happens to a source whose audio has no media clock and drifts, such as an SRT stream. The mixed audio is stamped with `tick.AudioPTS`, so a tick's video and audio show the same media time.

The MXL program writer ([`mxl/writer.go`](../../server/mxl/writer.go)) commits video at the clock's grain. It writes audio at the sample indices of the batch's PTS and drops a batch that overlaps what was written.

## Slow motion

The motion-compensated interpolator ([`MCFIState`](../../server/switcher/mcfi_interpolate.go), [`replay/interpolator.go`](../../server/replay/interpolator.go)) serves slow-motion replay; see [subsystems/replay](../subsystems/replay.md).

## Related docs

- **Concepts:** [pipeline](pipeline.md), [media-path](media-path.md), [locking-and-concurrency](locking-and-concurrency.md)
- **Subsystems:** [switcher](../subsystems/switcher.md), [transition](../subsystems/transition.md), [audio](../subsystems/audio.md), [replay](../subsystems/replay.md), [codec](../subsystems/codec.md)
- **Reference:** [metrics](../reference/metrics.md#switcher-metrics), [config-and-flags](../reference/config-and-flags.md)
- **Integration:** [ui-server-contract](../integration/ui-server-contract.md)
