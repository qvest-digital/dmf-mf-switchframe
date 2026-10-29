package control

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// uiSource is the UI tree the CORS lists are checked against.
const uiSource = "../../ui/src"

func TestCORSMiddleware_PreflightReturns204(t *testing.T) {
	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware(nil)(inner)

	req := httptest.NewRequest(http.MethodOptions, "/api/switch/cut", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rec.Code)
	}
	if innerCalled {
		t.Error("inner handler should not be called for preflight OPTIONS request")
	}

	// Check all CORS headers are set.
	assertCORSHeaders(t, rec, "*")
}

func TestCORSMiddleware_NormalRequestSetsCORSHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	handler := CORSMiddleware(nil)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/switch/cut", nil)
	req.Header.Set("Origin", "http://localhost:5173")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != `{"status":"ok"}` {
		t.Errorf("expected body %q, got %q", `{"status":"ok"}`, rec.Body.String())
	}

	assertCORSHeaders(t, rec, "*")
}

func TestCORSMiddleware_NoOriginStillWorks(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware(nil)(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/switch/state", nil)
	// No Origin header set.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// CORS headers should still be present even without an Origin header.
	assertCORSHeaders(t, rec, "*")
}

func TestCORSMiddleware_AllowedOrigins_MatchingOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware([]string{"http://localhost:5173", "https://app.example.com"})(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/switch/state", nil)
	req.Header.Set("Origin", "http://localhost:5173")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "http://localhost:5173" {
		t.Errorf("expected Access-Control-Allow-Origin %q, got %q", "http://localhost:5173", got)
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Errorf("expected Vary: Origin header when allowedOrigins is set")
	}
}

func TestCORSMiddleware_AllowedOrigins_NonMatchingOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware([]string{"https://app.example.com"})(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/switch/state", nil)
	req.Header.Set("Origin", "http://evil.example.com")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "" {
		t.Errorf("expected no Access-Control-Allow-Origin header, got %q", got)
	}
}

func TestCORSMiddleware_AllowedOrigins_PreflightMatch(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware([]string{"http://localhost:5173"})(inner)

	req := httptest.NewRequest(http.MethodOptions, "/api/switch/cut", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rec.Code)
	}

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "http://localhost:5173" {
		t.Errorf("expected Access-Control-Allow-Origin %q, got %q", "http://localhost:5173", got)
	}
}

// assertCORSHeaders checks that all expected CORS headers are set on the response.
func assertCORSHeaders(t *testing.T, rec *httptest.ResponseRecorder, expectedOrigin string) {
	t.Helper()

	expectations := map[string]string{
		"Access-Control-Allow-Origin":  expectedOrigin,
		"Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		"Access-Control-Allow-Headers": "Content-Type, Authorization",
		"Access-Control-Max-Age":       "86400",
	}

	for header, expected := range expectations {
		got := rec.Header().Get(header)
		if got != expected {
			t.Errorf("header %s: expected %q, got %q", header, expected, got)
		}
	}
}

// A UI served by one engine calls the other engine cross-origin, so every
// method the API registers a route for has to pass the preflight there.
func TestCORSMiddleware_AllowsEveryRegisteredRouteMethod(t *testing.T) {
	allowed := map[string]bool{}
	for _, m := range strings.Split(corsAllowMethods, ",") {
		allowed[strings.TrimSpace(m)] = true
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	route := regexp.MustCompile(`Handle(?:Func)?\("([A-Z]+) /api/`)
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range route.FindAllSubmatch(src, -1) {
			method := string(m[1])
			seen[method] = true
			if !allowed[method] {
				t.Errorf("%s registers a %s route, but Access-Control-Allow-Methods is %q", f, method, corsAllowMethods)
			}
		}
	}
	if !seen[http.MethodPatch] {
		t.Fatalf("no PATCH route found; the route scan no longer matches how routes are registered")
	}
}

func TestCORSMiddleware_PreflightAllowsPatch(t *testing.T) {
	handler := CORSMiddleware(nil)(http.NotFoundHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/graphics/0", nil)
	req.Header.Set("Origin", "https://engine-a.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPatch)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rec.Code)
	}
	methods := map[string]bool{}
	for _, m := range strings.Split(rec.Header().Get("Access-Control-Allow-Methods"), ",") {
		methods[strings.TrimSpace(m)] = true
	}
	if !methods[http.MethodPatch] {
		t.Errorf("Access-Control-Allow-Methods %q does not allow PATCH", rec.Header().Get("Access-Control-Allow-Methods"))
	}
}

// Every method and header the UI sets on a request has to be in the
// preflight answer, or the call fails at the other engine only, which in a
// dual-engine session reads as one engine diverging.
func TestCORSMiddleware_AllowsEveryUIRequest(t *testing.T) {
	allowedMethods := map[string]bool{}
	for _, m := range strings.Split(corsAllowMethods, ",") {
		allowedMethods[strings.TrimSpace(m)] = true
	}
	allowedHeaders := map[string]bool{}
	for _, h := range strings.Split(corsAllowHeaders, ",") {
		allowedHeaders[strings.ToLower(strings.TrimSpace(h))] = true
	}

	methodRe := regexp.MustCompile(`method:\s*['"]([A-Z]+)['"]|\.open\(['"]([A-Z]+)['"]`)
	headersObjRe := regexp.MustCompile(`headers:\s*\{([^}]*)\}`)
	headerKeyRe := regexp.MustCompile(`['"]([A-Za-z-]+)['"]\s*:`)
	headerSetRe := regexp.MustCompile(`headers\[['"]([A-Za-z-]+)['"]\]|setRequestHeader\(['"]([A-Za-z-]+)['"]`)

	methods := map[string]string{}
	headers := map[string]string{}
	err := filepath.WalkDir(uiSource, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(path, ".test.ts") || !(strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".svelte")) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range methodRe.FindAllStringSubmatch(string(src), -1) {
			methods[m[1]+m[2]] = path
		}
		for _, obj := range headersObjRe.FindAllStringSubmatch(string(src), -1) {
			for _, k := range headerKeyRe.FindAllStringSubmatch(obj[1], -1) {
				headers[k[1]] = path
			}
		}
		for _, m := range headerSetRe.FindAllStringSubmatch(string(src), -1) {
			headers[m[1]+m[2]] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if methods[http.MethodPatch] == "" || headers["Authorization"] == "" {
		t.Fatalf("UI scan found methods %v and headers %v; it no longer matches how the UI builds requests", methods, headers)
	}
	for m, path := range methods {
		if !allowedMethods[m] {
			t.Errorf("%s sends %s, but Access-Control-Allow-Methods is %q", path, m, corsAllowMethods)
		}
	}
	for h, path := range headers {
		if !allowedHeaders[strings.ToLower(h)] {
			t.Errorf("%s sets header %s, but Access-Control-Allow-Headers is %q", path, h, corsAllowHeaders)
		}
	}
}
