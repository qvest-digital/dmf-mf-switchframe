#pragma once

#include <atomic>
#include <functional>
#include <mutex>
#include <string>
#include <thread>

// Command from the Go server, parsed from JSON.
struct ControlCommand {
    std::string cmd;  // "create", "load", "play", "stop", "next", "update",
                      // "custom", "destroy", "shutdown"
    int id = -1;
    int width = 0;
    int height = 0;
    int fps = 0;
    std::string url;
    std::string data;
    std::string action;
    std::string payload;
};

using CommandHandler = std::function<void(const ControlCommand&)>;

class ControlSocket {
public:
    // Takes ownership of |listen_fd|; Stop() shuts it down and closes it.
    ControlSocket(int listen_fd, CommandHandler handler);
    ~ControlSocket();

    // Start accepting a connection and reading commands (spawns thread).
    void Start();

    // Send a response back to the Go server.
    void SendResponse(const std::string& json_line);

    // Stop the reader thread.
    void Stop();

private:
    void readLoop();

    // Listening socket, owned by this class. Stop() retires it (exchange to
    // -1) and close()s it — closing is what actually wakes a parked accept()
    // on macOS, where shutdown() on a listening AF_UNIX socket fails with
    // ENOTCONN and does not unblock accept(). Atomic because the reader
    // thread reads it while Stop() retires it on another thread.
    std::atomic<int> listen_fd_;
    // Shared across the reader thread (accept/read), SendResponse callers,
    // and Stop(); atomic so those accesses are not a data race. The fd is
    // only close()d by Stop() after the reader thread has been joined, so no
    // thread can still be using the descriptor when its number is released
    // for kernel reuse.
    std::atomic<int> conn_fd_{-1};
    // Set by SendResponse when a write fails partway through a message: a
    // truncated response prefix may be left on the wire, and writing anything
    // further would concatenate onto it and desynchronize the
    // newline-delimited JSON stream. Once set, SendResponse silently drops
    // all further responses. The fd itself stays open (the reader thread may
    // still be blocked on it); Stop() closes it after the join.
    std::atomic<bool> conn_failed_{false};
    CommandHandler handler_;
    std::thread thread_;
    std::atomic<bool> running_{false};
    std::mutex write_mutex_;
};
