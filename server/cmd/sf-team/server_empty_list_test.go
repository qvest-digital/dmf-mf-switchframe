package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmptyListEndpoints_ReturnEmptyArrayNotNull(t *testing.T) {
	cases := []struct {
		name string
		path string
		seed func(*testing.T, db)
	}{
		{"claims", "/api/claims", nil},
		{"messages", "/api/messages?thread=global", nil},
		{"agents", "/api/agents", nil},
		{"items", "/api/items", nil},
		{"touches", "/api/touches?active=true", nil},
		{"progress", "/api/progress/BUG-9999", func(t *testing.T, d db) {
			_ = d.register(context.Background(), "a", "A", "/tmp", 1)
		}},
		{"activity", "/api/items/BUG-EMPTY/activity", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := newTestDB(t)
			if tc.seed != nil {
				tc.seed(t, d)
			}
			srv := newServer(d, serverConfig{ItemsDir: t.TempDir()})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			srv.handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
			}
			body := bytes.TrimSpace(rec.Body.Bytes())
			if bytes.Equal(body, []byte("null")) {
				t.Fatalf("%s returned literal null; must be [] for empty list", tc.path)
			}
			if len(body) == 0 || body[0] != '[' {
				t.Fatalf("%s returned %s; must start with [", tc.path, body)
			}
		})
	}
}
