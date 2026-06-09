#include "frame_socket.h"

#include <cerrno>
#include <cstring>
#include <iostream>
#include <sys/socket.h>
#include <unistd.h>
#include <utility>

#include "protocol.h"

FrameSocket::FrameSocket(int listen_fd) : listen_fd_(listen_fd) {}

FrameSocket::~FrameSocket() { Close(); }

bool FrameSocket::Accept() {
    std::cerr << "[frame-socket] Waiting for connection..." << std::endl;
    int fd = accept(listen_fd_, nullptr, nullptr);
    if (fd < 0) {
        std::cerr << "[frame-socket] accept() failed: " << strerror(errno)
                  << std::endl;
        return false;
    }

    // Publish the connection and start the background writer under
    // queue_mutex_ so this cannot race Close(): Accept() runs on a dedicated
    // thread while Close() may run concurrently on the main thread. If Close
    // already ran (stop_ set), drop the connection instead of re-arming
    // stop_ = false and starting a writer thread that Close has already
    // joined-past — that would resurrect the socket after shutdown and leave
    // an unjoined writer running. The writer_ thread object is written only
    // here, under the same mutex Close acquires before reading it, which
    // orders the accesses.
    {
        std::lock_guard<std::mutex> lock(queue_mutex_);
        if (stop_) {
            close(fd);
            std::cerr << "[frame-socket] Connection accepted after Close; "
                         "dropping" << std::endl;
            return false;
        }
        conn_fd_ = fd;
        connected_ = true;
        // All socket writes happen on the writer thread so the CEF UI thread
        // never blocks on I/O.
        writer_ = std::thread([this]() { writeLoop(); });
    }
    std::cerr << "[frame-socket] Connected (fd=" << fd << ")" << std::endl;
    return true;
}

bool FrameSocket::SendFrame(int browser_id, int width, int height,
                             const void* bgra, size_t size) {
    if (!connected_) {
        std::cerr << "[frame-socket] SendFrame: not connected" << std::endl;
        return false;
    }

    // Serialize on the calling (CEF UI) thread. This copies the pixel buffer,
    // which is required because CEF only guarantees |bgra| is valid for the
    // duration of the OnPaint call.
    std::vector<uint8_t> buf;
    try {
        buf = protocol::SerializeFrame(static_cast<uint8_t>(browser_id),
                                       static_cast<uint16_t>(width),
                                       static_cast<uint16_t>(height), bgra,
                                       size);
    } catch (const std::exception& e) {
        std::cerr << "FrameSocket: serialize failed: " << e.what()
                  << std::endl;
        return false;
    }

    // Hand off to the writer thread. Keep only the latest frame per browser:
    // overwrite any previously queued but unsent frame for this browser so
    // backpressure on the reader drops stale frames instead of blocking or
    // unbounded buffering.
    {
        std::lock_guard<std::mutex> lock(queue_mutex_);
        pending_[browser_id] = std::move(buf);
    }
    queue_cv_.notify_one();
    return true;
}

bool FrameSocket::writeAll(const std::vector<uint8_t>& buf) {
    const uint8_t* data = buf.data();
    size_t remaining = buf.size();

    while (remaining > 0) {
        ssize_t n = write(conn_fd_, data, remaining);
        if (n <= 0) {
            if (n < 0 && (errno == EINTR)) {
                continue;
            }
            std::cerr << "FrameSocket: write failed: " << strerror(errno)
                      << std::endl;
            connected_ = false;
            return false;
        }
        data += n;
        remaining -= static_cast<size_t>(n);
    }
    return true;
}

void FrameSocket::writeLoop() {
    while (true) {
        std::map<int, std::vector<uint8_t>> batch;
        {
            std::unique_lock<std::mutex> lock(queue_mutex_);
            queue_cv_.wait(lock,
                           [this]() { return stop_ || !pending_.empty(); });
            if (stop_ && pending_.empty()) {
                return;
            }
            // Take ownership of everything queued so far; the map is left empty
            // for producers to refill. Writing each entry below blocks only
            // this thread, not the CEF UI thread.
            batch.swap(pending_);
        }

        for (auto& entry : batch) {
            if (stop_) {
                return;
            }
            if (!writeAll(entry.second)) {
                // Connection is broken; nothing more we can do here. Drain
                // until Close() tears us down.
                return;
            }
        }
    }
}

void FrameSocket::Close() {
    connected_ = false;

    // Signal the writer to stop and wake it if it is waiting. Setting stop_
    // under queue_mutex_ also blocks a concurrent Accept() from publishing a
    // new connection/writer from this point on, and reading conn_fd_ under
    // the same mutex orders it against Accept()'s write.
    int conn;
    {
        std::lock_guard<std::mutex> lock(queue_mutex_);
        stop_ = true;
        pending_.clear();
        conn = conn_fd_;
    }
    queue_cv_.notify_all();

    // Unblock the Accept() thread if it is still parked in accept() on the
    // listener (e.g. the Go server connected the control socket but never the
    // frame socket during a crash/restart race). Without this, join() on the
    // accept thread would hang forever and the sidecar would never exit.
    // Mirrors ControlSocket::Stop(); the listener fd itself is owned and closed
    // by main after the join.
    if (listen_fd_ >= 0) {
        shutdown(listen_fd_, SHUT_RDWR);
    }

    // Unblock a writer that is parked inside write() on a full send buffer.
    // shutdown() (not close()) keeps the fd valid while the writer may still
    // be using it; the close happens after the join below.
    if (conn >= 0) {
        shutdown(conn, SHUT_RDWR);
    }

    if (writer_.joinable()) {
        writer_.join();
    }

    {
        std::lock_guard<std::mutex> lock(queue_mutex_);
        conn = conn_fd_;
        conn_fd_ = -1;
    }
    if (conn >= 0) {
        close(conn);
    }
}
