package layout

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvenAlign(t *testing.T) {
	require.Equal(t, 0, EvenAlign(0))
	require.Equal(t, 0, EvenAlign(1))
	require.Equal(t, 2, EvenAlign(2))
	require.Equal(t, 2, EvenAlign(3))
	require.Equal(t, 100, EvenAlign(100))
	require.Equal(t, 100, EvenAlign(101))
}

func TestValidateSlot(t *testing.T) {
	t.Run("valid slot", func(t *testing.T) {
		slot := Slot{
			SourceKey: "cam1",
			Rect:      image.Rect(100, 100, 420, 280),
		}
		require.NoError(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("odd X origin", func(t *testing.T) {
		slot := Slot{
			SourceKey: "cam1",
			Rect:      image.Rect(101, 100, 421, 280),
		}
		require.Error(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("odd Y origin", func(t *testing.T) {
		slot := Slot{
			SourceKey: "cam1",
			Rect:      image.Rect(100, 101, 420, 281),
		}
		require.Error(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("odd width", func(t *testing.T) {
		slot := Slot{
			SourceKey: "cam1",
			Rect:      image.Rect(100, 100, 421, 280),
		}
		require.Error(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("out of bounds", func(t *testing.T) {
		slot := Slot{
			SourceKey: "cam1",
			Rect:      image.Rect(1800, 1000, 2000, 1200),
		}
		require.Error(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("empty source allowed", func(t *testing.T) {
		slot := Slot{
			SourceKey: "",
			Rect:      image.Rect(100, 100, 420, 280),
		}
		require.NoError(t, ValidateSlot(slot, 1920, 1080))
	})
}

func TestEffectiveScaleMode(t *testing.T) {
	t.Run("default is stretch", func(t *testing.T) {
		s := Slot{}
		require.Equal(t, ScaleModeStretch, s.EffectiveScaleMode())
	})
	t.Run("fill", func(t *testing.T) {
		s := Slot{ScaleMode: ScaleModeFill}
		require.Equal(t, ScaleModeFill, s.EffectiveScaleMode())
	})
	t.Run("explicit stretch", func(t *testing.T) {
		s := Slot{ScaleMode: ScaleModeStretch}
		require.Equal(t, ScaleModeStretch, s.EffectiveScaleMode())
	})
	t.Run("unknown treated as stretch", func(t *testing.T) {
		s := Slot{ScaleMode: "unknown"}
		require.Equal(t, ScaleModeStretch, s.EffectiveScaleMode())
	})
}

func TestEffectiveCropAnchor(t *testing.T) {
	t.Run("default is center", func(t *testing.T) {
		s := Slot{}
		ax, ay := s.EffectiveCropAnchor()
		require.Equal(t, 0.5, ax)
		require.Equal(t, 0.5, ay)
	})
	t.Run("custom values", func(t *testing.T) {
		s := Slot{CropAnchor: [2]float64{0.0, 1.0}}
		ax, ay := s.EffectiveCropAnchor()
		require.Equal(t, 0.0, ax)
		require.Equal(t, 1.0, ay)
	})
	t.Run("clamped to 0-1", func(t *testing.T) {
		s := Slot{CropAnchor: [2]float64{-0.5, 2.0}}
		ax, ay := s.EffectiveCropAnchor()
		require.Equal(t, 0.0, ax)
		require.Equal(t, 1.0, ay)
	})
	t.Run("top-left in fill mode", func(t *testing.T) {
		s := Slot{ScaleMode: ScaleModeFill, CropAnchor: [2]float64{0.0, 0.0}}
		ax, ay := s.EffectiveCropAnchor()
		require.Equal(t, 0.0, ax, "explicit [0,0] in fill mode should be top-left, not center")
		require.Equal(t, 0.0, ay)
	})
	t.Run("zero anchor without fill defaults to center", func(t *testing.T) {
		s := Slot{CropAnchor: [2]float64{0.0, 0.0}}
		ax, ay := s.EffectiveCropAnchor()
		require.Equal(t, 0.5, ax, "zero anchor in stretch mode should default to center")
		require.Equal(t, 0.5, ay)
	})
}

func TestValidateSlot_ScaleMode(t *testing.T) {
	base := Slot{SourceKey: "cam1", Rect: image.Rect(0, 0, 480, 270)}

	t.Run("valid fill", func(t *testing.T) {
		s := base
		s.ScaleMode = ScaleModeFill
		require.NoError(t, ValidateSlot(s, 1920, 1080))
	})
	t.Run("valid stretch", func(t *testing.T) {
		s := base
		s.ScaleMode = ScaleModeStretch
		require.NoError(t, ValidateSlot(s, 1920, 1080))
	})
	t.Run("empty is valid", func(t *testing.T) {
		require.NoError(t, ValidateSlot(base, 1920, 1080))
	})
	t.Run("invalid mode", func(t *testing.T) {
		s := base
		s.ScaleMode = "invalid"
		require.Error(t, ValidateSlot(s, 1920, 1080))
	})
	t.Run("cropAnchor out of range", func(t *testing.T) {
		s := base
		s.CropAnchor = [2]float64{0.5, 1.5}
		require.Error(t, ValidateSlot(s, 1920, 1080))
	})
}

func TestValidateLayout(t *testing.T) {
	t.Run("valid layout", func(t *testing.T) {
		l := &Layout{
			Name: "test",
			Slots: []Slot{
				{SourceKey: "cam1", Rect: image.Rect(0, 0, 480, 270), Enabled: true},
			},
		}
		require.NoError(t, ValidateLayout(l, 1920, 1080))
	})

	t.Run("too many slots", func(t *testing.T) {
		l := &Layout{Name: "test", Slots: make([]Slot, 5)}
		require.Error(t, ValidateLayout(l, 1920, 1080))
	})
}
