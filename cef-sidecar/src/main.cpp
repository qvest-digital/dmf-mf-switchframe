#include <iostream>
#include <string>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <cstring>

#include "include/cef_app.h"
#include "include/cef_command_line.h"
#include "app.h"
#include "browser_pool.h"

int main(int argc, char* argv[]) {
    // CEF subprocess support (required for multi-process architecture)
    CefMainArgs main_args(argc, argv);

    CefRefPtr<SwitchframeCefApp> app(new SwitchframeCefApp());

    // Check if this is a subprocess (renderer, gpu, etc.)
    int exit_code = CefExecuteProcess(main_args, app, nullptr);
    if (exit_code >= 0) {
        return exit_code;  // Subprocess handled
    }

    // Parse command line for socket paths
    std::string control_socket_path;
    std::string frame_socket_path;

    for (int i = 1; i < argc; i++) {
        std::string arg(argv[i]);
        if (arg == "--control-socket" && i + 1 < argc) {
            control_socket_path = argv[++i];
        } else if (arg == "--frame-socket" && i + 1 < argc) {
            frame_socket_path = argv[++i];
        }
    }

    if (control_socket_path.empty() || frame_socket_path.empty()) {
        std::cerr << "Usage: switchframe-cef --control-socket <path> --frame-socket <path>"
                  << std::endl;
        return 1;
    }

    // Configure CEF
    CefSettings settings;
    settings.windowless_rendering_enabled = true;
    settings.no_sandbox = true;
    settings.background_color = CefColorSetARGB(0, 0, 0, 0);
    settings.log_severity = LOGSEVERITY_WARNING;

#if defined(__APPLE__)
    // macOS requires the framework path
    CefString(&settings.framework_dir_path) =
        CefString(argv[0]).ToString() +
        "/../Frameworks/Chromium Embedded Framework.framework";
#endif

    if (!CefInitialize(main_args, settings, app, nullptr)) {
        std::cerr << "CEF initialization failed" << std::endl;
        return 1;
    }

    // Create Unix domain socket listeners
    // (actual accept + read/write loops added in Tasks 10-11)
    auto create_listener = [](const std::string& path) -> int {
        unlink(path.c_str());  // Remove stale socket

        int fd = socket(AF_UNIX, SOCK_STREAM, 0);
        if (fd < 0) {
            std::cerr << "socket() failed: " << strerror(errno) << std::endl;
            return -1;
        }

        struct sockaddr_un addr = {};
        addr.sun_family = AF_UNIX;
        strncpy(addr.sun_path, path.c_str(), sizeof(addr.sun_path) - 1);

        if (bind(fd, (struct sockaddr*)&addr, sizeof(addr)) < 0) {
            std::cerr << "bind() failed for " << path << ": " << strerror(errno)
                      << std::endl;
            close(fd);
            return -1;
        }

        if (listen(fd, 1) < 0) {
            std::cerr << "listen() failed: " << strerror(errno) << std::endl;
            close(fd);
            return -1;
        }

        return fd;
    };

    int control_fd = create_listener(control_socket_path);
    int frame_fd = create_listener(frame_socket_path);

    if (control_fd < 0 || frame_fd < 0) {
        CefShutdown();
        return 1;
    }

    // Create browser pool with stub frame callback.
    // The frame socket writer will be wired here in Task 11.
    auto browser_pool = std::make_shared<BrowserPool>(
        4, [](int id, int w, int h, const void* /*buf*/, size_t sz) {
            std::cerr << "frame: browser=" << id << " " << w << "x" << h
                      << " size=" << sz << std::endl;
        });

    // Signal ready to the Go server
    std::cout << "READY" << std::endl;
    std::cout.flush();

    // Run CEF message loop (blocks until CefQuitMessageLoop is called)
    CefRunMessageLoop();

    // Cleanup
    close(control_fd);
    close(frame_fd);
    unlink(control_socket_path.c_str());
    unlink(frame_socket_path.c_str());

    CefShutdown();
    return 0;
}
