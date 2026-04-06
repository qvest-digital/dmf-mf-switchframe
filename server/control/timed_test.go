package control

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/cmdqueue"
)

func TestTimedHandler_NoTimestamp_ExecutesImmediately(t *testing.T) {
	called := false
	inner := func(w http.ResponseWriter, r *http.Request, body []byte) {
		called = true
		w.WriteHeader(http.StatusOK)
	}

	q := cmdqueue.New(func() int64 { return 1_000_000_000 })
	h := timedHandler(q, "cut", inner)

	body := `{"source":"camera1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/switch/cut", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.True(t, called, "inner handler should be called when no execute_at is present")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTimedHandler_WithTimestamp_Enqueues(t *testing.T) {
	called := false
	inner := func(w http.ResponseWriter, r *http.Request, body []byte) {
		called = true
		w.WriteHeader(http.StatusOK)
	}

	// Clock returns a time such that execute_at is in the future (not stale).
	now := int64(1_000_000_000)
	future := now + 500_000 // 500ms in the future
	q := cmdqueue.New(func() int64 { return now })
	h := timedHandler(q, "cut", inner)

	body := fmt.Sprintf(`{"source":"camera1","execute_at":%d,"command_seq":1}`, future)
	req := httptest.NewRequest(http.MethodPost, "/api/switch/cut", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.False(t, called, "inner handler should NOT be called when execute_at is present")
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, 1, q.Pending(), "command should be enqueued")
}

func TestTimedHandler_NilQueue_ExecutesImmediately(t *testing.T) {
	called := false
	inner := func(w http.ResponseWriter, r *http.Request, body []byte) {
		called = true
		w.WriteHeader(http.StatusOK)
	}

	// Even with execute_at present, nil queue means execute immediately.
	h := timedHandler(nil, "cut", inner)

	body := `{"source":"camera1","execute_at":9999999999,"command_seq":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/switch/cut", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.True(t, called, "inner handler should be called when queue is nil")
	require.Equal(t, http.StatusOK, rec.Code)
}
