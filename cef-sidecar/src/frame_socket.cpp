#include "frame_socket.h"

#include <cerrno>
#include <cstring>
#include <iostream>
#include <sys/socket.h>
#include <unistd.h>

#include "protocol.h"

FrameSocket::FrameSocket(int listen_fd) : listen_fd_(listen_fd) {}

FrameSocket::~FrameSocket() { Close(); }

bool FrameSocket::Accept() {
    conn_fd_ = accept(listen_fd_, nullptr, nullptr);
    if (conn_fd_ < 0) {
        std::cerr << "FrameSocket: accept() failed: " << strerror(errno)
                  << std::endl;
        return false;
    }
    connected_ = true;
    return true;
}

bool FrameSocket::SendFrame(int browser_id, int width, int height,
                             const void* bgra, size_t size) {
    if (!connected_) {
        return false;
    }

    std::lock_guard<std::mutex> lock(write_mutex_);
    if (conn_fd_ < 0) {
        return false;
    }

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

    const uint8_t* data = buf.data();
    size_t remaining = buf.size();

    while (remaining > 0) {
        ssize_t n = write(conn_fd_, data, remaining);
        if (n <= 0) {
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

void FrameSocket::Close() {
    connected_ = false;
    if (conn_fd_ >= 0) {
        close(conn_fd_);
        conn_fd_ = -1;
    }
}
