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

    int listen_fd_;
    int conn_fd_ = -1;
    CommandHandler handler_;
    std::thread thread_;
    std::atomic<bool> running_{false};
    std::mutex write_mutex_;
};
