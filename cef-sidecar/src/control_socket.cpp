#include "control_socket.h"

#include <cerrno>
#include <cstring>
#include <iostream>
#include <sstream>
#include <sys/socket.h>
#include <unistd.h>

#include "include/cef_task.h"
#include "json_parse.h"

ControlSocket::ControlSocket(int listen_fd, CommandHandler handler)
    : listen_fd_(listen_fd), handler_(std::move(handler)) {}

ControlSocket::~ControlSocket() { Stop(); }

void ControlSocket::Start() {
    running_ = true;
    thread_ = std::thread([this]() { readLoop(); });
}

void ControlSocket::SendResponse(const std::string& json_line) {
    std::lock_guard<std::mutex> lock(write_mutex_);
    if (conn_fd_ < 0) {
        return;
    }

    const char* data = json_line.data();
    size_t remaining = json_line.size();

    while (remaining > 0) {
        ssize_t n = write(conn_fd_, data, remaining);
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

    // Close connection to unblock any pending read/accept.
    if (conn_fd_ >= 0) {
        shutdown(conn_fd_, SHUT_RDWR);
        close(conn_fd_);
        conn_fd_ = -1;
    }

    // Close the listener to unblock accept().
    if (listen_fd_ >= 0) {
        shutdown(listen_fd_, SHUT_RDWR);
    }

    if (thread_.joinable()) {
        thread_.join();
    }
}

void ControlSocket::readLoop() {
    // Accept one connection from the Go server.
    conn_fd_ = accept(listen_fd_, nullptr, nullptr);
    if (conn_fd_ < 0) {
        if (running_) {
            std::cerr << "ControlSocket: accept() failed: " << strerror(errno)
                      << std::endl;
        }
        return;
    }

    // Read lines from the connection.
    std::string buffer;
    char chunk[4096];

    while (running_) {
        ssize_t n = read(conn_fd_, chunk, sizeof(chunk));
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
            ControlCommand captured = std::move(cmd);
            CommandHandler handler = handler_;
            CefPostTask(TID_UI,
                        CefCreateClosureTask(base::BindOnce(
                            [](CommandHandler h, ControlCommand c) { h(c); },
                            handler, captured)));
        }
    }
}
