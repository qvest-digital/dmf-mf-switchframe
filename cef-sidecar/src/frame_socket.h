#pragma once

#include <atomic>
#include <condition_variable>
#include <cstdint>
#include <map>
#include <mutex>
#include <thread>
#include <vector>

// FrameSocket streams rendered frames to the Go server over a Unix domain
// socket.
//
// Threading model: SendFrame is called on the single CEF UI thread (via
// OnPaint). It MUST NOT block on socket I/O, because the UI thread also runs
// the CEF message loop and executes every control command posted via
// CefPostTask(TID_UI). Blocking there would freeze all browser repaints and
// control commands whenever the reader applies backpressure.
//
// To decouple rendering from I/O, SendFrame only serializes the frame and
// hands it to a single-slot-per-browser queue (the latest frame per browser
// overwrites any stale, not-yet-sent frame). A dedicated writer thread drains
// the queue and performs the blocking write() of complete, length-prefixed
// frames. Coalescing happens only at the queue level: a frame is either sent
// in full or dropped before any of its bytes hit the wire, so the
// length-prefixed stream never desyncs.
class FrameSocket {
public:
    // Takes ownership of |listen_fd|; Close() shuts it down and closes it.
    explicit FrameSocket(int listen_fd);
    ~FrameSocket();

    // Accept a connection (blocking -- call from a thread or before message
    // loop). On success, starts the background writer thread.
    bool Accept();

    // Queue a frame for delivery to the connected client. Non-blocking: copies
    // the frame and returns immediately, overwriting any previously queued but
    // unsent frame for the same browser. Safe to call from the CEF UI thread.
    bool SendFrame(int browser_id, int width, int height, const void* bgra,
                   size_t size);

    void Close();

private:
    // Drains queued frames and writes them to the socket. Runs on writer_.
    void writeLoop();

    // Writes the entire buffer to conn_fd_, looping over partial writes.
    // Returns false on error (and marks the socket disconnected).
    bool writeAll(const std::vector<uint8_t>& buf);

    // Listening socket, owned by this class. Close() retires it (exchange to
    // -1) and close()s it — closing is what actually wakes a parked accept()
    // on macOS, where shutdown() on a listening AF_UNIX socket fails with
    // ENOTCONN and does not unblock accept(). Atomic because the Accept()
    // thread reads it while Close() retires it on another thread.
    std::atomic<int> listen_fd_;
    int conn_fd_ = -1;
    std::atomic<bool> connected_{false};

    // Single-slot-per-browser queue of serialized frames awaiting send.
    // Guarded by queue_mutex_; queue_cv_ signals the writer thread.
    // stop_ is atomic so the writer can poll it mid-batch without the lock; it
    // is still mutated under queue_mutex_ (then notified) for the CV predicate.
    std::mutex queue_mutex_;
    std::condition_variable queue_cv_;
    std::map<int, std::vector<uint8_t>> pending_;  // browser_id -> serialized frame
    std::atomic<bool> stop_{false};

    std::thread writer_;
};
