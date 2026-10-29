package graphics

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func addLayers(t *testing.T, c *Compositor, n int) []int {
	t.Helper()
	ids := make([]int, n)
	for i := range ids {
		id, err := c.AddLayer()
		require.NoError(t, err)
		ids[i] = id
	}
	return ids
}

func layerByID(t *testing.T, c *Compositor, id int) LayerState {
	t.Helper()
	for _, l := range c.Status().Layers {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("layer %d not found", id)
	return LayerState{}
}

func TestSetLayerSettings(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	id := addLayers(t, c, 1)[0]

	name := "  Sponsor bug "
	require.NoError(t, c.SetLayerSettings(id, &name, json.RawMessage(`{"template": "network-bug", "fields": {"text": "Hi"}}`)))
	l := layerByID(t, c, id)
	require.Equal(t, "Sponsor bug", l.Name)
	require.JSONEq(t, `{"template":"network-bug","fields":{"text":"Hi"}}`, string(l.Config))

	// Absent fields stay; null clears the config.
	require.NoError(t, c.SetLayerSettings(id, nil, nil))
	require.Equal(t, "Sponsor bug", layerByID(t, c, id).Name)
	require.NoError(t, c.SetLayerSettings(id, nil, json.RawMessage(`null`)))
	require.Empty(t, layerByID(t, c, id).Config)

	long := strings.Repeat("x", 65)
	require.ErrorIs(t, c.SetLayerSettings(id, &long, nil), ErrInvalidLayerName)
	for _, bad := range []string{`[1]`, `"x"`, `{`, `{"a":"` + strings.Repeat("y", 64<<10) + `"}`} {
		require.ErrorIs(t, c.SetLayerSettings(id, nil, json.RawMessage(bad)), ErrInvalidLayerConfig, bad)
	}
	require.ErrorIs(t, c.SetLayerSettings(99, &name, nil), ErrLayerNotFound)
}

func TestSetLayerOrder_RestacksWithoutRenumbering(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	ids := addLayers(t, c, 3) // 0,1,2 stacked bottom to top
	for i, id := range ids {
		n := []string{"bottom", "middle", "top"}[i]
		require.NoError(t, c.SetLayerSettings(id, &n, nil))
	}

	// Move the top layer to the bottom, then the old bottom above the middle.
	require.NoError(t, c.SetLayerOrder([]int{2, 0, 1}))
	st := c.Status()
	var got []int
	for _, l := range st.Layers {
		got = append(got, l.ID)
	}
	require.Equal(t, []int{2, 0, 1}, got)
	for z, l := range st.Layers {
		require.Equal(t, z, l.ZOrder, "z-orders are renumbered 0..n-1")
	}
	// IDs, which playlist events address, keep naming the same layers.
	require.Equal(t, "top", layerByID(t, c, 2).Name)
	require.Equal(t, "bottom", layerByID(t, c, 0).Name)

	for _, bad := range [][]int{{2, 0}, {2, 0, 0}, {2, 0, 7}, {2, 0, 1, 3}} {
		require.ErrorIs(t, c.SetLayerOrder(bad), ErrInvalidLayerOrder, "%v", bad)
	}
}

func TestExportImport_RoundTrip(t *testing.T) {
	src := NewCompositor()
	defer src.Close()
	ids := addLayers(t, src, 3)
	pngData := testPNG(t, 8, 6)
	require.NoError(t, src.SetImage(ids[1], "logo.png", pngData))
	require.NoError(t, src.Off(ids[1]))
	require.NoError(t, src.SetLayerRect(ids[0], image.Rect(100, 800, 1100, 1000)))
	name := "Lower third"
	require.NoError(t, src.SetLayerSettings(ids[0], &name, json.RawMessage(`{"template":"lower-third","fields":{"name":"Ada"}}`)))
	require.NoError(t, src.RemoveLayer(ids[2]))
	ids = append(ids[:2], addLayers(t, src, 1)...) // a gap in the IDs: 0, 1, 3
	require.NoError(t, src.SetLayerOrder([]int{ids[2], ids[0], ids[1]}))

	exported := src.ExportLayers()
	raw, err := json.Marshal(exported)
	require.NoError(t, err)

	dst := NewCompositor()
	defer dst.Close()
	addLayers(t, dst, 2) // replaced by the import
	var doc LayerExport
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NoError(t, dst.ImportLayers(doc))

	again, err := json.Marshal(dst.ExportLayers())
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(again))

	for _, l := range dst.Status().Layers {
		require.False(t, l.Active, "layer %d imported on air", l.ID)
	}
	_, gotPNG, err := dst.GetImage(ids[1])
	require.NoError(t, err)
	require.Equal(t, pngData, gotPNG)

	// A new layer never reuses an imported ID.
	next, err := dst.AddLayer()
	require.NoError(t, err)
	require.Equal(t, 4, next)
}

func TestImport_RefusedWhileOnAir(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	ids := addLayers(t, c, 2)
	require.NoError(t, c.SetImage(ids[0], "a.png", testPNG(t, 4, 4))) // goes on air
	before := c.ExportLayers()

	err := c.ImportLayers(LayerExport{Format: ExportFormat, Version: ExportVersion, Layers: []LayerRecord{{ID: 5}}})
	require.ErrorIs(t, err, ErrLayersOnAir)
	require.Equal(t, before, c.ExportLayers())
}

func TestImport_Validation(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(testPNG(t, 2, 2))
	cases := map[string]LayerExport{
		"format":    {Format: "other", Version: 1},
		"version":   {Format: ExportFormat, Version: 2},
		"dup id":    {Format: ExportFormat, Version: 1, Layers: []LayerRecord{{ID: 1}, {ID: 1}}},
		"neg id":    {Format: ExportFormat, Version: 1, Layers: []LayerRecord{{ID: -1}}},
		"name":      {Format: ExportFormat, Version: 1, Layers: []LayerRecord{{ID: 1, Name: strings.Repeat("n", 65)}}},
		"config":    {Format: ExportFormat, Version: 1, Layers: []LayerRecord{{ID: 1, Config: json.RawMessage(`[]`)}}},
		"rect":      {Format: ExportFormat, Version: 1, Layers: []LayerRecord{{ID: 1, Rect: RectState{Width: -2}}}},
		"base64":    {Format: ExportFormat, Version: 1, Layers: []LayerRecord{{ID: 1, Image: &LayerImage{PNG: "%%"}}}},
		"not png":   {Format: ExportFormat, Version: 1, Layers: []LayerRecord{{ID: 1, Image: &LayerImage{PNG: base64.StdEncoding.EncodeToString([]byte("gif"))}}}},
		"too many":  {Format: ExportFormat, Version: 1, Layers: make([]LayerRecord, DefaultMaxLayers+1)},
		"bad later": {Format: ExportFormat, Version: 1, Layers: []LayerRecord{{ID: 1, Image: &LayerImage{PNG: good}}, {ID: 2, Rect: RectState{X: -1}}}},
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			c := NewCompositor()
			defer c.Close()
			addLayers(t, c, 1)
			before := c.ExportLayers()
			require.ErrorIs(t, c.ImportLayers(doc), ErrInvalidLayerExport)
			require.Equal(t, before, c.ExportLayers(), "a refused import changes nothing")
		})
	}
}
