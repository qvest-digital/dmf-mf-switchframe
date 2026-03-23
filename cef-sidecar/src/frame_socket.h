#pragma once

#include <atomic>
#include <mutex>

class FrameSocket {
public:
    explicit FrameSocket(int listen_fd);
    ~FrameSocket();

    // Accept a connection (blocking -- call from a thread or before message loop).
    bool Accept();

    // Write a frame to the connected client.
    // Thread-safe (called from CEF render thread via OnPaint).
    bool SendFrame(int browser_id, int width, int height, const void* bgra,
                   size_t size);

    void Close();

private:
    int listen_fd_;
    int conn_fd_ = -1;
    std::mutex write_mutex_;
    std::atomic<bool> connected_{false};
};
