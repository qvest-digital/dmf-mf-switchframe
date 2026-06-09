#include "control_socket.h"

#include <cerrno>
#include <cstring>
#include <iostream>
#include <sstream>
#include <sys/socket.h>
#include <unistd.h>

#include "include/cef_task.h"
#include "include/wrapper/cef_closure_task.h"
#include "include/wrapper/cef_helpers.h"
#include "json_parse.h"

namespace {

// CefTask subclass that invokes a CommandHandler with a captured command.
class CommandTask : public CefTask {
public:
    CommandTask(CommandHandler handler, ControlCommand cmd)
        : handler_(std::move(handler)), cmd_(std::move(cmd)) {}

    void Execute() override { handler_(cmd_); }

private:
    CommandHandler handler_;
    ControlCommand cmd_;
    IMPLEMENT_REFCOUNTING(CommandTask);
};

}  // namespace

ControlSocket::ControlSocket(int listen_fd, CommandHandler handler)
    : listen_fd_(listen_fd), handler_(std::move(handler)) {}

ControlSocket::~ControlSocket() { Stop(); }

void ControlSocket::Start() {
    running_ = true;
    thread_ = std::thread([this]() { readLoop(); });
}

void ControlSocket::SendResponse(const std::string& json_line) {
    std::lock_guard<std::mutex> lock(write_mutex_);
    int fd = conn_fd_.load();
    if (fd < 0 || conn_failed_.load()) {
        return;
    }

    const char* data = json_line.data();
    size_t remaining = json_line.size();

    while (remaining > 0) {
        ssize_t n = write(fd, data, remaining);
        if (n <= 0) {
            if (n < 0 && errno == EINTR) {
                // Interrupted by a signal before anything was written;
                // retry — the stream is still consistent.
                continue;
            }
            std::cerr << "ControlSocket: write failed: " << strerror(errno)
                      << std::endl;
            // The write may have failed partway through the message, leaving
            // a truncated response prefix on the wire. Any later response
            // would concatenate onto it and desynchronize the newline-
            // delimited JSON stream, so mark the connection dead: all further
            // SendResponse calls become silent no-ops (callers tolerate this;
            // the peer is gone or broken anyway). shutdown() — not close() —
            // tears the connection down while keeping the fd valid for the
            // reader thread; Stop() closes it after the join.
            conn_failed_.store(true);
            shutdown(fd, SHUT_RDWR);
            return;
        }
        data += n;
        remaining -= static_cast<size_t>(n);
    }
}

void ControlSocket::Stop() {
    running_ = false;

    // Unblock a reader parked in read() (and any in-flight SendResponse)
    // with shutdown() only: the fd must stay open while other threads may
    // still be inside read()/write() on it, otherwise the kernel can hand
    // the same fd number to an unrelated open() and the parked syscall
    // lands on the wrong descriptor. The close() happens after the join.
    int conn = conn_fd_.load();
    if (conn >= 0) {
        shutdown(conn, SHUT_RDWR);
    }

    // Tear down the listener to unblock a reader parked in accept().
    // shutdown() alone is NOT enough: on macOS, shutdown() on a listening
    // AF_UNIX socket fails with ENOTCONN and does not wake a blocked
    // accept() — only close() does. Keep the shutdown() first for platforms
    // where it does wake accept() (e.g. Linux), then close the fd. running_
    // is already false, so the woken readLoop treats the failure as a clean
    // shutdown rather than logging an error. The usual close-to-wake fd-reuse
    // hazard does not apply: at shutdown the listener is used only by the
    // parked accept() and this class creates no new fds that could recycle
    // the number before readLoop observes running_ == false.
    int lfd = listen_fd_.exchange(-1);
    if (lfd >= 0) {
        shutdown(lfd, SHUT_RDWR);
        close(lfd);
    }

    if (thread_.joinable()) {
        thread_.join();
    }

    // The reader thread is gone. Re-read conn_fd_ (accept() may have
    // completed concurrently with Stop) and close exactly once, under
    // write_mutex_ so a racing SendResponse either sees the fd still open
    // (and finishes before we close it) or sees -1 and returns.
    std::lock_guard<std::mutex> lock(write_mutex_);
    conn = conn_fd_.exchange(-1);
    if (conn >= 0) {
        close(conn);
    }
}

void ControlSocket::readLoop() {
    // Accept one connection from the Go server.
    int fd = -1;
    while (true) {
        int lfd = listen_fd_.load();
        if (lfd < 0) {
            // Stop() already retired the listener: clean shutdown.
            return;
        }
        fd = accept(lfd, nullptr, nullptr);
        if (fd >= 0) {
            break;
        }
        if (errno == EINTR) {
            // Interrupted by a signal; the listener is still fine.
            continue;
        }
        if (running_) {
            std::cerr << "ControlSocket: accept() failed: " << strerror(errno)
                      << std::endl;
        }
        return;
    }
    // Publish for SendResponse/Stop. If Stop() already ran, its post-join
    // cleanup (after this thread exits) closes this fd, so a connection
    // accepted concurrently with Stop is not leaked.
    conn_fd_.store(fd);
    if (!running_) {
        return;
    }

    // Read lines from the connection.
    std::string buffer;
    char chunk[4096];

    while (running_) {
        ssize_t n = read(fd, chunk, sizeof(chunk));
        if (n <= 0) {
            if (n < 0 && errno == EINTR) {
                // Interrupted by a signal — NOT a dead connection. Treating
                // this as fatal would permanently kill the control channel.
                continue;
            }
            if (n < 0 && running_) {
                std::cerr << "ControlSocket: read() failed: "
                          << strerror(errno) << std::endl;
            }
            break;
        }

        buffer.append(chunk, static_cast<size_t>(n));

        // Process complete lines.
        size_t pos;
        while ((pos = buffer.find('\n')) != std::string::npos) {
            std::string line = buffer.substr(0, pos);
            buffer.erase(0, pos + 1);

            if (line.empty()) {
                continue;
            }

            ControlCommand cmd;
            if (!json_parse::ParseControlCommand(line, cmd)) {
                std::cerr << "ControlSocket: failed to parse: " << line
                          << std::endl;
                continue;
            }

            // Post the command to the CEF UI thread.
            CefPostTask(TID_UI,
                        new CommandTask(handler_, std::move(cmd)));
        }
    }
}
