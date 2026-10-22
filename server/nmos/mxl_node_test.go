package nmos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// Ids must be the same across restarts and engines given the same seed,
// differ between seeds and parts, and be legal IS-04 ids.
func TestDeriveID(t *testing.T) {
	a := DeriveID("p-show/switcher", "receiver", "cam1", "video")
	if a != DeriveID("p-show/switcher", "receiver", "cam1", "video") {
		t.Error("not stable")
	}
	for _, other := range []string{
		DeriveID("p-other/switcher", "receiver", "cam1", "video"),
		DeriveID("p-show/switcher", "receiver", "cam1", "audio"),
		DeriveID("p-show/switcher", "receiver", "cam1v", "ideo"),
	} {
		if other == a {
			t.Errorf("collision: %s", a)
		}
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(a) {
		t.Errorf("%s is not a version 5 UUID", a)
	}
}

func queryServer(t *testing.T, resources map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := resources[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The label shown for a connected input is its source's, as that names what
// is connected rather than which output of a device carries it.
func TestQueryClient_SenderLabel(t *testing.T) {
	const q = "/x-nmos/query/v1.3/"
	srv := queryServer(t, map[string]string{
		q + "senders/s1":   `{"label":"writer-a video","flow_id":"f1"}`,
		q + "flows/f1":     `{"label":"bars flow","source_id":"src1"}`,
		q + "sources/src1": `{"label":"Studio 1 Camera"}`,
		q + "senders/s2":   `{"label":"only a sender","flow_id":null}`,
		q + "senders/s3":   `{"label":"sender","flow_id":"f3"}`,
		q + "flows/f3":     `{"label":"flow three","source_id":"gone"}`,
	})
	c := NewQueryClient(srv.URL + "/")
	for sender, want := range map[string]string{"s1": "Studio 1 Camera", "s2": "only a sender", "s3": "flow three"} {
		got, err := c.SenderLabel(context.Background(), sender)
		if err != nil {
			t.Fatalf("%s: %v", sender, err)
		}
		if got != want {
			t.Errorf("%s: %q, want %q", sender, got, want)
		}
	}
	if _, err := c.SenderLabel(context.Background(), "missing"); err == nil {
		t.Error("unknown sender: no error")
	}
}
