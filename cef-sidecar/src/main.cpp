#include <iostream>
#include <memory>
#include <string>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <cstring>

#include "include/cef_app.h"
#include "include/cef_command_line.h"
#include "adapter.h"
#include "app.h"
#include "browser_pool.h"
#include "control_socket.h"
#include "frame_socket.h"
#include "protocol.h"

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

    // Create frame socket and accept connection (blocks until Go connects).
    auto frame_socket = std::make_shared<FrameSocket>(frame_fd);

    // Create browser pool wired to the frame socket.
    auto browser_pool = std::make_shared<BrowserPool>(
        4, [frame_socket](int id, int w, int h, const void* buf, size_t sz) {
            frame_socket->SendFrame(id, w, h, buf, sz);
        });

    // Get adapter.js source for injection on page load.
    const std::string& adapter_js = adapter::GetAdapterJS();

    // Create control socket with command dispatch handler.
    auto control_socket = std::make_shared<ControlSocket>(
        control_fd,
        [browser_pool, &adapter_js,
         ctrl = std::weak_ptr<ControlSocket>()](const ControlCommand& cmd) {
            auto cs = ctrl.lock();

            if (cmd.cmd == "create") {
                int w = cmd.width > 0 ? cmd.width : 1920;
                int h = cmd.height > 0 ? cmd.height : 1080;
                int fps = cmd.fps > 0 ? cmd.fps : 60;
                bool ok = browser_pool->Create(cmd.id, w, h, fps);

                if (ok) {
                    // Wire OnLoadEnd callback to inject adapter.js on every page load.
                    browser_pool->SetLoadEndCallback(cmd.id,
                        [browser_pool, &adapter_js, cs](int browser_id, int http_status) {
                            (void)http_status;
                            browser_pool->Execute(browser_id, adapter_js);
                            // Report format detection event after adapter has a
                            // chance to run. The adapter.js sets up a load listener
                            // that calls __switchframe.detect() asynchronously.
                        });
                    browser_pool->SetLoadErrorCallback(cmd.id,
                        [cs](int browser_id, const std::string& error) {
                            if (cs) {
                                cs->SendResponse(protocol::MakeEventResponse(
                                    browser_id, "load_error", "error", error));
                            }
                        });
                    if (cs) {
                        cs->SendResponse(protocol::MakeOkResponse(cmd.id));
                    }
                } else {
                    if (cs) {
                        cs->SendResponse(protocol::MakeErrorResponse(
                            cmd.id, "create failed"));
                    }
                }
            } else if (cmd.cmd == "load") {
                bool ok = browser_pool->LoadURL(cmd.id, cmd.url);
                if (cs) {
                    if (ok) {
                        cs->SendResponse(protocol::MakeOkResponse(cmd.id));
                    } else {
                        cs->SendResponse(protocol::MakeErrorResponse(
                            cmd.id, "load failed"));
                    }
                }
            } else if (cmd.cmd == "play") {
                bool ok = browser_pool->Execute(
                    cmd.id, "window.__switchframe && window.__switchframe.play()");
                if (cs) {
                    cs->SendResponse(ok ? protocol::MakeOkResponse(cmd.id)
                                        : protocol::MakeErrorResponse(
                                              cmd.id, "play failed"));
                }
            } else if (cmd.cmd == "stop") {
                bool ok = browser_pool->Execute(
                    cmd.id, "window.__switchframe && window.__switchframe.stop()");
                if (cs) {
                    cs->SendResponse(ok ? protocol::MakeOkResponse(cmd.id)
                                        : protocol::MakeErrorResponse(
                                              cmd.id, "stop failed"));
                }
            } else if (cmd.cmd == "next") {
                bool ok = browser_pool->Execute(
                    cmd.id, "window.__switchframe && window.__switchframe.next()");
                if (cs) {
                    cs->SendResponse(ok ? protocol::MakeOkResponse(cmd.id)
                                        : protocol::MakeErrorResponse(
                                              cmd.id, "next failed"));
                }
            } else if (cmd.cmd == "update") {
                std::string js = "window.__switchframe && "
                                 "window.__switchframe.update('" +
                                 protocol::EscapeForJS(cmd.data) + "')";
                bool ok = browser_pool->Execute(cmd.id, js);
                if (cs) {
                    cs->SendResponse(ok ? protocol::MakeOkResponse(cmd.id)
                                        : protocol::MakeErrorResponse(
                                              cmd.id, "update failed"));
                }
            } else if (cmd.cmd == "custom") {
                std::string js =
                    "window.__switchframe && "
                    "window.__switchframe.custom('" +
                    protocol::EscapeForJS(cmd.action) + "', '" +
                    protocol::EscapeForJS(cmd.payload) + "')";
                bool ok = browser_pool->Execute(cmd.id, js);
                if (cs) {
                    cs->SendResponse(ok ? protocol::MakeOkResponse(cmd.id)
                                        : protocol::MakeErrorResponse(
                                              cmd.id, "custom failed"));
                }
            } else if (cmd.cmd == "destroy") {
                browser_pool->Destroy(cmd.id);
                if (cs) {
                    cs->SendResponse(protocol::MakeOkResponse(cmd.id));
                }
            } else if (cmd.cmd == "shutdown") {
                if (cs) {
                    cs->SendResponse(protocol::MakeOkResponse(cmd.id));
                }
                CefQuitMessageLoop();
            } else {
                if (cs) {
                    cs->SendResponse(protocol::MakeErrorResponse(
                        cmd.id, "unknown command: " + cmd.cmd));
                }
            }
        });

    // Signal ready to the Go server (before accepting connections so the Go
    // server knows the socket files exist and can connect).
    std::cout << "READY" << std::endl;
    std::cout.flush();

    // Accept frame socket connection in a background thread (blocks until Go
    // connects).
    std::thread frame_accept([&frame_socket]() { frame_socket->Accept(); });

    // Start the control socket reader (spawns its own thread with accept).
    control_socket->Start();

    // Run CEF message loop (blocks until CefQuitMessageLoop is called)
    CefRunMessageLoop();

    // Cleanup
    control_socket->Stop();
    frame_socket->Close();

    // Wait for the frame accept thread if it hasn't finished.
    if (frame_accept.joinable()) {
        frame_accept.join();
    }

    close(control_fd);
    close(frame_fd);
    unlink(control_socket_path.c_str());
    unlink(frame_socket_path.c_str());

    CefShutdown();
    return 0;
}
