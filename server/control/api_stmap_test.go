package control

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupSTMapTestAPI(t *testing.T) (*API, *stmap.Registry) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	reg := stmap.NewRegistry()
	api := NewAPI(sw, WithSTMapRegistry(reg))
	return api, reg
}

func TestSTMapAPI_List(t *testing.T) {
	api, _ := setupSTMapTestAPI(t)

	// List empty
	req := httptest.NewRequest("GET", "/api/stmap", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string][]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Empty(t, resp["maps"])

	// Generate a map, then list again
	body := `{"type":"barrel","name":"barrel1","width":320,"height":240}`
	req = httptest.NewRequest("POST", "/api/stmap/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	req = httptest.NewRequest("GET", "/api/stmap", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	err = json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Contains(t, resp["maps"], "barrel1")
}

func TestSTMapAPI_Generate_Static(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	body := `{"type":"barrel","params":{"k1":-0.3},"name":"barrel-cam3","width":320,"height":240}`
	req := httptest.NewRequest("POST", "/api/stmap/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Equal(t, "barrel-cam3", resp["name"])
	require.Equal(t, float64(320), resp["width"])
	require.Equal(t, float64(240), resp["height"])
	require.Equal(t, "static", resp["type"])

	// Verify stored in registry
	m, ok := reg.Get("barrel-cam3")
	require.True(t, ok)
	require.Equal(t, 320, m.Width)
	require.Equal(t, 240, m.Height)
}

func TestSTMapAPI_Generate_Animated(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	body := `{"type":"heat_shimmer","name":"shimmer1","width":320,"height":240,"frame_count":30}`
	req := httptest.NewRequest("POST", "/api/stmap/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Equal(t, "shimmer1", resp["name"])
	require.Equal(t, "animated", resp["type"])
	require.Equal(t, float64(30), resp["frame_count"])

	// Verify stored in registry
	a, ok := reg.GetAnimated("shimmer1")
	require.True(t, ok)
	require.Len(t, a.Frames, 30)
}

func TestSTMapAPI_Generate_UnknownType(t *testing.T) {
	api, _ := setupSTMapTestAPI(t)

	body := `{"type":"nonexistent","name":"foo","width":320,"height":240}`
	req := httptest.NewRequest("POST", "/api/stmap/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSTMapAPI_Generate_DefaultDimensions(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	// Omit width/height — should use pipeline format defaults.
	body := `{"type":"identity","name":"id1"}`
	req := httptest.NewRequest("POST", "/api/stmap/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	m, ok := reg.Get("id1")
	require.True(t, ok)
	// TestSwitcher default format is 1920x1080
	pf := api.switcher.PipelineFormat()
	require.Equal(t, pf.Width, m.Width)
	require.Equal(t, pf.Height, m.Height)
}

func TestSTMapAPI_Generate_AssignSource(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	body := `{"type":"barrel","name":"barrel-src","width":320,"height":240,"assign_source":"camera1"}`
	req := httptest.NewRequest("POST", "/api/stmap/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	name, ok := reg.SourceMap("camera1")
	require.True(t, ok)
	require.Equal(t, "barrel-src", name)
}

func TestSTMapAPI_Generate_AssignProgram(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	body := `{"type":"heat_shimmer","name":"shimmer-prog","width":320,"height":240,"assign_program":true}`
	req := httptest.NewRequest("POST", "/api/stmap/generate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	require.True(t, reg.HasProgramMap())
}

func TestSTMapAPI_AssignSource(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	// Store a map first
	m := stmap.Identity(320, 240)
	m.Name = "id-map"
	require.NoError(t, reg.Store(m))

	body := `{"map":"id-map"}`
	req := httptest.NewRequest("PUT", "/api/stmap/source/camera1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	name, ok := reg.SourceMap("camera1")
	require.True(t, ok)
	require.Equal(t, "id-map", name)
}

func TestSTMapAPI_AssignSource_NotFound(t *testing.T) {
	api, _ := setupSTMapTestAPI(t)

	body := `{"map":"nonexistent"}`
	req := httptest.NewRequest("PUT", "/api/stmap/source/camera1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSTMapAPI_RemoveSource(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	m := stmap.Identity(320, 240)
	m.Name = "id-map"
	require.NoError(t, reg.Store(m))
	require.NoError(t, reg.AssignSource("camera1", "id-map"))

	req := httptest.NewRequest("DELETE", "/api/stmap/source/camera1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	_, ok := reg.SourceMap("camera1")
	require.False(t, ok)
}

func TestSTMapAPI_AssignProgram(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	m := stmap.Identity(320, 240)
	m.Name = "prog-map"
	require.NoError(t, reg.Store(m))

	body := `{"map":"prog-map"}`
	req := httptest.NewRequest("PUT", "/api/stmap/program", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	require.True(t, reg.HasProgramMap())
}

func TestSTMapAPI_AssignProgram_NotFound(t *testing.T) {
	api, _ := setupSTMapTestAPI(t)

	body := `{"map":"nonexistent"}`
	req := httptest.NewRequest("PUT", "/api/stmap/program", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSTMapAPI_RemoveProgram(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	m := stmap.Identity(320, 240)
	m.Name = "prog-map"
	require.NoError(t, reg.Store(m))
	require.NoError(t, reg.AssignProgram("prog-map"))
	require.True(t, reg.HasProgramMap())

	req := httptest.NewRequest("DELETE", "/api/stmap/program", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.False(t, reg.HasProgramMap())
}

func TestSTMapAPI_Delete(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	m := stmap.Identity(320, 240)
	m.Name = "delete-me"
	require.NoError(t, reg.Store(m))

	req := httptest.NewRequest("DELETE", "/api/stmap/delete-me", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	_, ok := reg.Get("delete-me")
	require.False(t, ok)
}

func TestSTMapAPI_Delete_NotFound(t *testing.T) {
	api, _ := setupSTMapTestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/stmap/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSTMapAPI_Get(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	m := stmap.Identity(320, 240)
	m.Name = "get-me"
	require.NoError(t, reg.Store(m))

	req := httptest.NewRequest("GET", "/api/stmap/get-me", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Equal(t, "get-me", resp["name"])
	require.Equal(t, float64(320), resp["width"])
	require.Equal(t, float64(240), resp["height"])
	require.Equal(t, "static", resp["type"])
}

func TestSTMapAPI_Get_NotFound(t *testing.T) {
	api, _ := setupSTMapTestAPI(t)

	req := httptest.NewRequest("GET", "/api/stmap/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSTMapAPI_Upload_PNG(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	// Create a minimal identity map and write it to PNG for upload.
	m := stmap.Identity(4, 4) // small for test
	raw, err := stmap.WriteRaw(m)
	require.NoError(t, err)

	// Upload as raw since that's simpler in tests
	req := httptest.NewRequest("POST", "/api/stmap/upload/test-upload", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	_, ok := reg.Get("test-upload")
	require.True(t, ok)
}

func TestSTMapAPI_Upload_EXR(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	// Build a minimal valid uncompressed EXR with 4x2 float32 R+G channels.
	exrData := buildMinimalEXR(t, 4, 2)

	req := httptest.NewRequest("POST", "/api/stmap/upload/test-exr.exr", strings.NewReader(string(exrData)))
	req.Header.Set("Content-Type", "image/x-exr")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	m, ok := reg.Get("test-exr.exr")
	require.True(t, ok)
	require.Equal(t, 4, m.Width)
	require.Equal(t, 2, m.Height)
}

func TestSTMapAPI_Upload_EXR_InvalidData(t *testing.T) {
	api, _ := setupSTMapTestAPI(t)

	req := httptest.NewRequest("POST", "/api/stmap/upload/bad.exr", strings.NewReader("not valid exr data"))
	req.Header.Set("Content-Type", "image/x-exr")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSTMapAPI_Upload_EXR_MagicDetection(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	// Upload without EXR extension or content-type — should auto-detect by magic bytes.
	exrData := buildMinimalEXR(t, 4, 2)

	req := httptest.NewRequest("POST", "/api/stmap/upload/autodetect", strings.NewReader(string(exrData)))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	m, ok := reg.Get("autodetect")
	require.True(t, ok)
	require.Equal(t, 4, m.Width)
	require.Equal(t, 2, m.Height)
}

// buildMinimalEXR constructs a tiny valid uncompressed EXR file for API tests.
func buildMinimalEXR(t *testing.T, width, height int) []byte {
	t.Helper()

	var buf []byte
	appendLE32 := func(v uint32) { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); buf = append(buf, b...) }
	appendLE64 := func(v uint64) { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); buf = append(buf, b...) }
	appendStr := func(s string) { buf = append(buf, []byte(s)...); buf = append(buf, 0) }
	appendAttr := func(name, typeName string, data []byte) {
		appendStr(name)
		appendStr(typeName)
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uint32(len(data)))
		buf = append(buf, b...)
		buf = append(buf, data...)
	}

	// Magic + version
	appendLE32(0x01312F76)
	appendLE32(2)

	// Channel list: G(float) + R(float) alphabetically
	var chData []byte
	chData = append(chData, []byte("G")...)
	chData = append(chData, 0)
	{
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, 2)
		chData = append(chData, b...)
	} // FLOAT
	chData = append(chData, 0, 0, 0, 0) // pLinear + reserved
	{
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, 1)
		chData = append(chData, b...)
	} // xSampling
	{
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, 1)
		chData = append(chData, b...)
	} // ySampling
	chData = append(chData, []byte("R")...)
	chData = append(chData, 0)
	{
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, 2)
		chData = append(chData, b...)
	}
	chData = append(chData, 0, 0, 0, 0)
	{
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, 1)
		chData = append(chData, b...)
	}
	{
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, 1)
		chData = append(chData, b...)
	}
	chData = append(chData, 0) // terminator
	appendAttr("channels", "chlist", chData)

	appendAttr("compression", "compression", []byte{0})

	dwData := make([]byte, 16)
	binary.LittleEndian.PutUint32(dwData[8:], uint32(width-1))
	binary.LittleEndian.PutUint32(dwData[12:], uint32(height-1))
	appendAttr("dataWindow", "box2i", dwData)
	appendAttr("displayWindow", "box2i", dwData)
	appendAttr("lineOrder", "lineOrder", []byte{0})
	buf = append(buf, 0) // end of header

	// Offset table (one per scanline)
	offsetTableStart := len(buf)
	for y := 0; y < height; y++ {
		appendLE64(0)
	}

	// Scanline blocks
	bytesPerRow := width * 4 * 2 // 2 channels, 4 bytes per float
	for y := 0; y < height; y++ {
		binary.LittleEndian.PutUint64(buf[offsetTableStart+y*8:], uint64(len(buf)))
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uint32(y))
		buf = append(buf, b...)
		binary.LittleEndian.PutUint32(b, uint32(bytesPerRow))
		buf = append(buf, b...)
		// G channel then R channel (alphabetical), all zeros
		buf = append(buf, make([]byte, bytesPerRow)...)
	}

	return buf
}

func TestSTMapAPI_Download(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	m := stmap.Identity(4, 4)
	m.Name = "dl-map"
	require.NoError(t, reg.Store(m))

	req := httptest.NewRequest("GET", "/api/stmap/dl-map/download", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), "dl-map.stmap")

	// Verify the raw data can be read back
	m2, err := stmap.ReadRaw(rec.Body.Bytes(), "roundtrip")
	require.NoError(t, err)
	require.Equal(t, 4, m2.Width)
	require.Equal(t, 4, m2.Height)
}

func TestSTMapAPI_Download_NotFound(t *testing.T) {
	api, _ := setupSTMapTestAPI(t)

	req := httptest.NewRequest("GET", "/api/stmap/nonexistent/download", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSTMapAPI_State(t *testing.T) {
	api, reg := setupSTMapTestAPI(t)

	// Initially empty
	req := httptest.NewRequest("GET", "/api/stmap/state", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state stmap.STMapState
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err)
	require.Empty(t, state.Sources)
	require.Nil(t, state.Program)

	// Add a map and assign to source
	m := stmap.Identity(320, 240)
	m.Name = "state-map"
	require.NoError(t, reg.Store(m))
	require.NoError(t, reg.AssignSource("camera1", "state-map"))

	req = httptest.NewRequest("GET", "/api/stmap/state", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	err = json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err)
	require.Equal(t, "state-map", state.Sources["camera1"])
}

func TestSTMapAPI_Generators(t *testing.T) {
	api, _ := setupSTMapTestAPI(t)

	req := httptest.NewRequest("GET", "/api/stmap/generators", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string][]stmap.GeneratorInfo
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp["generators"])

	// Verify at least barrel and heat_shimmer are present
	names := make([]string, 0, len(resp["generators"]))
	for _, g := range resp["generators"] {
		names = append(names, g.Name)
	}
	require.Contains(t, names, "barrel")
	require.Contains(t, names, "heat_shimmer")
}

func TestSTMapAPI_NilRegistry(t *testing.T) {
	// When no registry is configured, all stmap routes should 404 (not registered).
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw)

	req := httptest.NewRequest("GET", "/api/stmap", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	// Without the registry, routes are not registered, so the mux returns 404 or 405.
	require.NotEqual(t, http.StatusOK, rec.Code)
}
