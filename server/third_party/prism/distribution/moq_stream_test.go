package distribution

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	webtransport "github.com/quic-go/webtransport-go"
	"github.com/zsiec/prism/certs"
)

// childCountingCtx counts the contexts derived from it that are still
// attached: the context package registers a child through AfterFunc and
// calls the returned stop once the child is cancelled.
type childCountingCtx struct {
	context.Context
	done     chan struct{}
	attached atomic.Int64
}

func (c *childCountingCtx) Done() <-chan struct{} { return c.done }

func (c *childCountingCtx) AfterFunc(func()) func() bool {
	c.attached.Add(1)
	var once sync.Once
	return func() bool {
		stopped := false
		once.Do(func() {
			c.attached.Add(-1)
			stopped = true
		})
		return stopped
	}
}

// webTransportSession returns both sides of a WebTransport session over
// loopback, the server's first.
func webTransportSession(t *testing.T) (*webtransport.Session, *webtransport.Session) {
	t.Helper()
	cert, err := certs.Generate(time.Hour)
	if err != nil {
		t.Fatalf("certs.Generate: %v", err)
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	sessions := make(chan *webtransport.Session, 1)
	done := make(chan struct{})
	mux := http.NewServeMux()
	h3 := &http3.Server{
		Handler:   mux,
		TLSConfig: http3.ConfigureTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert.TLSCert}}),
	}
	webtransport.ConfigureHTTP3Server(h3)
	srv := &webtransport.Server{H3: h3, CheckOrigin: func(*http.Request) bool { return true }}
	mux.HandleFunc("/moq", func(w http.ResponseWriter, r *http.Request) {
		s, err := srv.Upgrade(w, r)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		sessions <- s
		<-done
	})
	go func() { _ = srv.Serve(udp) }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var d webtransport.Dialer
	d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	url := fmt.Sprintf("https://%s/moq", udp.LocalAddr())
	_, client, err := d.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		close(done)
		_ = client.CloseWithError(0, "")
		_ = d.Close()
		_ = srv.Close()
	})

	select {
	case s := <-sessions:
		return s, client
	case <-ctx.Done():
		t.Fatal("no session")
		return nil, nil
	}
}

// A viewer's subscription opens a stream per group on one context that
// lives as long as the subscription. Each open left a context attached
// to it, which grew the server's memory for as long as the tab was open.
func TestOpenUniStreamLeavesNoContextAttached(t *testing.T) {
	session, client := webTransportSession(t)
	parent := &childCountingCtx{Context: context.Background(), done: make(chan struct{})}

	for i := range 20 {
		stream, err := openUniStream(parent, session)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if _, err := stream.Write([]byte{1}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		_ = stream.Close()

		// The context the stream was opened on is cancelled by now; the
		// stream still delivers.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		got, err := client.AcceptUniStream(ctx)
		if err != nil {
			cancel()
			t.Fatalf("accept %d: %v", i, err)
		}
		b, err := io.ReadAll(got)
		cancel()
		if err != nil || len(b) != 1 {
			t.Fatalf("read %d: %v, %d bytes, want 1", i, err, len(b))
		}
	}
	if n := parent.attached.Load(); n != 0 {
		t.Fatalf("%d contexts still attached after 20 streams, want 0", n)
	}
}
