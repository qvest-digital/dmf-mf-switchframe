package main

import (
	"context"
	"fmt"
	"image"

	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
)

// snapshotProviderAdapter bridges the App to the control.SnapshotProvider interface.
type snapshotProviderAdapter struct {
	app *App
}

func (sp *snapshotProviderAdapter) CaptureFullSnapshot() internal.FullStateSnapshot {
	return sp.app.captureFullSnapshot()
}

func (sp *snapshotProviderAdapter) ApplyTarget() internal.SnapshotApplyTarget {
	return sp.app
}

// Verify snapshotProviderAdapter satisfies the interface at compile time.
var _ control.SnapshotProvider = (*snapshotProviderAdapter)(nil)

// Verify App satisfies SnapshotApplyTarget at compile time.
var _ internal.SnapshotApplyTarget = (*App)(nil)

// --- SnapshotApplyTarget implementation ---

// Cut switches the program source.
func (a *App) Cut(ctx context.Context, source string) error {
	return a.sw.Cut(ctx, source)
}

// SetPreview sets the preview source.
func (a *App) SetPreview(ctx context.Context, source string) error {
	return a.sw.SetPreview(ctx, source)
}

// SetTransitionConfig stores the transition type, duration, and easing.
// These values are used by the next transition start. Since the switcher does
// not have a persistent "default transition" setter (the type/duration are only
// active during a transition), this is a best-effort no-op that logs the config
// for reconciliation but does not trigger a transition.
func (a *App) SetTransitionConfig(transitionType string, durationMs int, easing string) error {
	// The transition type/duration/easing are parameters passed to StartTransition
	// rather than stored defaults. During snapshot reconciliation, capturing
	// the "last used" transition type is informational — the real state is
	// whether an active transition is running (which is transient).
	return nil
}

// SetFTB sets or clears the Fade to Black state. If the desired state matches
// the current state, this is a no-op. If they differ, FadeToBlack is toggled.
func (a *App) SetFTB(active bool) error {
	state := a.sw.State()
	if state.FTBActive == active {
		return nil // already in desired state
	}
	if active {
		// Need to enter FTB — toggle it on.
		return a.sw.FadeToBlack(context.Background())
	}
	// Need to exit FTB — toggle it off (FadeToBlack toggles when in FTB state).
	return a.sw.FadeToBlack(context.Background())
}

// --- Audio per-channel ---

func (a *App) SetLevel(sourceKey string, levelDB float64) error {
	return a.mixer.SetLevel(sourceKey, levelDB)
}

func (a *App) SetTrim(sourceKey string, trimDB float64) error {
	return a.mixer.SetTrim(sourceKey, trimDB)
}

func (a *App) SetMuted(sourceKey string, muted bool) error {
	return a.mixer.SetMuted(sourceKey, muted)
}

func (a *App) SetAFV(sourceKey string, afv bool) error {
	return a.mixer.SetAFV(sourceKey, afv)
}

func (a *App) SetPhaseInvert(sourceKey string, invert bool) error {
	return a.mixer.SetPhaseInvert(sourceKey, invert)
}

func (a *App) SetBalance(sourceKey string, balance float64) error {
	return a.mixer.SetBalance(sourceKey, balance)
}

func (a *App) SetHPF(sourceKey string, frequency float64, enabled bool) error {
	return a.mixer.SetHPF(sourceKey, frequency, enabled)
}

func (a *App) SetEQ(sourceKey string, band int, frequency, gain, q float64, enabled bool) error {
	return a.mixer.SetEQ(sourceKey, band, frequency, gain, q, enabled)
}

func (a *App) SetCompressor(sourceKey string, threshold, ratio, attack, release, makeupGain float64) error {
	return a.mixer.SetCompressor(sourceKey, threshold, ratio, attack, release, makeupGain)
}

func (a *App) SetGate(sourceKey string, threshold, rangeDB, attack, release, hold float64, enabled bool) error {
	return a.mixer.SetGate(sourceKey, threshold, rangeDB, attack, release, hold, enabled)
}

func (a *App) SetAudioDelay(sourceKey string, delayMs int) error {
	return a.mixer.SetAudioDelay(sourceKey, delayMs)
}

// --- Audio master ---

func (a *App) SetMasterLevel(levelDB float64) error {
	return a.mixer.SetMasterLevel(levelDB)
}

func (a *App) SetLimiterThreshold(thresholdDB float64) error {
	return a.mixer.SetLimiterThreshold(thresholdDB)
}

func (a *App) SetLimiterCeiling(ceilingDB float64) error {
	return a.mixer.SetLimiterCeiling(ceilingDB)
}

// --- Graphics ---

func (a *App) SetGraphicsLayerActive(id int, active bool) error {
	if a.compositor == nil {
		return nil
	}
	if active {
		return a.compositor.On(id)
	}
	return a.compositor.Off(id)
}

func (a *App) SetGraphicsLayerZOrder(id int, zOrder int) error {
	if a.compositor == nil {
		return nil
	}
	return a.compositor.SetLayerZOrder(id, zOrder)
}

func (a *App) SetGraphicsLayerRect(id int, x, y, width, height int) error {
	if a.compositor == nil {
		return nil
	}
	return a.compositor.SetLayerRect(id, image.Rect(x, y, x+width, y+height))
}

// --- DVE Layout ---

func (a *App) SetDVELayout(layout internal.LayoutSnapshot) error {
	if a.dveCompositor == nil {
		return nil
	}

	l := &dve.Layout{
		Name:  layout.ActivePreset,
		Slots: make([]dve.Slot, len(layout.Slots)),
	}

	for i, ss := range layout.Slots {
		slot := dve.Slot{
			SourceKey: ss.SourceKey,
			Enabled:   ss.Enabled,
			ZOrder:    ss.ZOrder,
			Transform: dve.Transform{
				PositionX:    ss.PositionX,
				PositionY:    ss.PositionY,
				Width:        ss.Width,
				Height:       ss.Height,
				RotationDeg:  ss.RotationDeg,
				AnchorX:      ss.AnchorX,
				AnchorY:      ss.AnchorY,
				PerspectiveX: ss.PerspectiveX,
				PerspectiveY: ss.PerspectiveY,
				CropTop:      ss.CropTop,
				CropBottom:   ss.CropBottom,
				CropLeft:     ss.CropLeft,
				CropRight:    ss.CropRight,
				CropSoftness: ss.CropSoftness,
				Opacity:      ss.Opacity,
				ScaleMode:    ss.ScaleMode,
				CropAnchor:   ss.CropAnchor,
				DefocusRadius: ss.DefocusRadius,
				MirrorH:      ss.MirrorH,
				MirrorV:      ss.MirrorV,
				MosaicSize:   ss.MosaicSize,
				Saturation:   ss.Saturation,
				Brightness:   ss.Brightness,
				Contrast:     ss.Contrast,
			},
		}

		if ss.Border != nil {
			slot.Transform.Border = dve.BorderConfig{
				Width:        ss.Border.Width,
				ColorY:       ss.Border.ColorY,
				ColorCb:      ss.Border.ColorCb,
				ColorCr:      ss.Border.ColorCr,
				CornerRadius: ss.Border.CornerRadius,
				Softness:     ss.Border.Softness,
				InnerWidth:   ss.Border.InnerWidth,
			}
		}

		if ss.Shadow != nil {
			slot.Transform.Shadow = dve.ShadowConfig{
				Enabled: ss.Shadow.Enabled,
				OffsetX: ss.Shadow.OffsetX,
				OffsetY: ss.Shadow.OffsetY,
				Blur:    ss.Shadow.Blur,
				Opacity: ss.Shadow.Opacity,
				ColorY:  ss.Shadow.ColorY,
			}
		}

		if ss.Mode3D != "" {
			slot.Transform.Mode3D = ss.Mode3D
			slot.Transform.Transform3D = &dve.Transform3D{
				RotateX: ss.RotateX,
				RotateY: ss.RotateY,
				RotateZ: ss.RotateZ,
			}
		}

		l.Slots[i] = slot
	}

	a.dveCompositor.SetLayout(l)
	return nil
}

// --- Source config ---

func (a *App) SetSourceLabel(ctx context.Context, sourceKey, label string) error {
	return a.sw.SetLabel(ctx, sourceKey, label)
}

func (a *App) SetSourcePosition(sourceKey string, position int) error {
	return a.sw.SetSourcePosition(sourceKey, position)
}

// --- Upstream keys ---

func (a *App) SetUpstreamKey(sourceKey string, config internal.UpstreamKeySnapshot) error {
	if a.keyProcessor == nil {
		return nil
	}
	a.keyProcessor.SetKey(sourceKey, graphics.KeyConfig{
		Type:           graphics.KeyType(config.Type),
		Enabled:        config.Enabled,
		KeyColorY:      config.KeyColorY,
		KeyColorCb:     config.KeyColorCb,
		KeyColorCr:     config.KeyColorCr,
		Similarity:     config.Similarity,
		Smoothness:     config.Smoothness,
		SpillSuppress:  config.SpillSuppress,
		SpillReplaceCb: config.SpillReplaceCb,
		SpillReplaceCr: config.SpillReplaceCr,
		LowClip:        config.LowClip,
		HighClip:       config.HighClip,
		Softness:       config.Softness,
		FillSource:     config.FillSource,
	})
	return nil
}

// --- Captions ---

func (a *App) SetCaptionMode(mode string) error {
	if a.captionMgr == nil {
		return nil
	}
	m, ok := caption.ParseMode(mode)
	if !ok {
		return fmt.Errorf("unknown caption mode %q", mode)
	}
	a.captionMgr.SetMode(m)
	return nil
}

// --- Color grading ---

func (a *App) SetColorGrade(lookName string, intensity float32) error {
	if a.colorGradeCtrl == nil {
		return nil
	}
	return a.colorGradeCtrl.SetColorGrade(lookName, intensity)
}

func (a *App) DisableColorGrade() error {
	if a.colorGradeCtrl == nil {
		return nil
	}
	a.colorGradeCtrl.DisableColorGrade()
	return nil
}

// --- ST maps ---

func (a *App) AssignSTMapSource(sourceKey, mapName string) error {
	if a.stmapRegistry == nil {
		return nil
	}
	return a.stmapRegistry.AssignSource(sourceKey, mapName)
}

func (a *App) AssignSTMapProgram(mapName string) error {
	if a.stmapRegistry == nil {
		return nil
	}
	return a.stmapRegistry.AssignProgram(mapName)
}
