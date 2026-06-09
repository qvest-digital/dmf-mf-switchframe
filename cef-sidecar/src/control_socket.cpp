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
    if (fd < 0) {
        return;
    }

    const char* data = json_line.data();
    size_t remaining = json_line.size();

    while (remaining > 0) {
        ssize_t n = write(fd, data, remaining);
        if (n <= 0) {
            std::cerr << "ControlSocket: write failed: " << strerror(errno)
                      << std::endl;
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

    // Shut down the listener to unblock accept().
    if (listen_fd_ >= 0) {
        shutdown(listen_fd_, SHUT_RDWR);
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
    int fd = accept(listen_fd_, nullptr, nullptr);
    if (fd < 0) {
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
