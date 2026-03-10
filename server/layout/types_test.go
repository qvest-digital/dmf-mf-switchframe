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
		slot := LayoutSlot{
			SourceKey: "cam1",
			Rect:      image.Rect(100, 100, 420, 280),
		}
		require.NoError(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("odd X origin", func(t *testing.T) {
		slot := LayoutSlot{
			SourceKey: "cam1",
			Rect:      image.Rect(101, 100, 421, 280),
		}
		require.Error(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("odd Y origin", func(t *testing.T) {
		slot := LayoutSlot{
			SourceKey: "cam1",
			Rect:      image.Rect(100, 101, 420, 281),
		}
		require.Error(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("odd width", func(t *testing.T) {
		slot := LayoutSlot{
			SourceKey: "cam1",
			Rect:      image.Rect(100, 100, 421, 280),
		}
		require.Error(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("out of bounds", func(t *testing.T) {
		slot := LayoutSlot{
			SourceKey: "cam1",
			Rect:      image.Rect(1800, 1000, 2000, 1200),
		}
		require.Error(t, ValidateSlot(slot, 1920, 1080))
	})

	t.Run("empty source allowed", func(t *testing.T) {
		slot := LayoutSlot{
			SourceKey: "",
			Rect:      image.Rect(100, 100, 420, 280),
		}
		require.NoError(t, ValidateSlot(slot, 1920, 1080))
	})
}

func TestValidateLayout(t *testing.T) {
	t.Run("valid layout", func(t *testing.T) {
		l := &Layout{
			Name: "test",
			Slots: []LayoutSlot{
				{SourceKey: "cam1", Rect: image.Rect(0, 0, 480, 270), Enabled: true},
			},
		}
		require.NoError(t, ValidateLayout(l, 1920, 1080))
	})

	t.Run("too many slots", func(t *testing.T) {
		l := &Layout{Name: "test", Slots: make([]LayoutSlot, 5)}
		require.Error(t, ValidateLayout(l, 1920, 1080))
	})
}
