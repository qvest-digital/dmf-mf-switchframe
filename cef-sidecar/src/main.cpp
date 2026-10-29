#include <iostream>
#include <memory>
#include <string>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <cstring>

#if defined(__APPLE__)
#include <mach-o/dyld.h>  // _NSGetExecutablePath
#include "include/wrapper/cef_library_loader.h"
#endif

#include "include/cef_app.h"
#include "include/cef_command_line.h"
#include "adapter.h"
#include "app.h"
#include "browser_pool.h"
#include "control_socket.h"
#include "frame_socket.h"
#include "protocol.h"
#include "request_policy.h"
#include "url_policy.h"

int main(int argc, char* argv[]) {
#if defined(__APPLE__)
    // CEF on macOS requires the framework be loaded at runtime via dlopen
    // (rather than linked directly) before any CEF API is called. Without
    // this the very first CEF call segfaults inside the framework lookup.
    CefScopedLibraryLoader library_loader;
    if (!library_loader.LoadInMain()) {
        std::cerr << "Failed to load CEF framework via LoadInMain()" << std::endl;
        return 1;
    }
#endif

    CefMainArgs main_args(argc, argv);

    CefRefPtr<SwitchframeCefApp> app(new SwitchframeCefApp());

#if !defined(__APPLE__)
    // Single-binary subprocess handling: on Linux/Windows the same binary
    // serves as main and as renderer/GPU/etc. subprocess. On macOS, CEF
    // spawns subprocesses out of <App>.app/Contents/Frameworks/<App> Helper.app
    // — a separate binary built from process_helper_mac.cc.
    int exit_code = CefExecuteProcess(main_args, app, nullptr);
    if (exit_code >= 0) {
        return exit_code;
    }
#endif

    // Parse command line for socket paths
    std::string control_socket_path;
    std::string frame_socket_path;
    std::string user_data_dir;
    std::string templates_dir;

    for (int i = 1; i < argc; i++) {
        std::string arg(argv[i]);
        if (arg == "--control-socket" && i + 1 < argc) {
            control_socket_path = argv[++i];
        } else if (arg == "--frame-socket" && i + 1 < argc) {
            frame_socket_path = argv[++i];
        } else if (arg == "--user-data-dir" && i + 1 < argc) {
            user_data_dir = argv[++i];
        } else if (arg == "--templates-dir" && i + 1 < argc) {
            templates_dir = argv[++i];
        }
    }

    if (control_socket_path.empty() || frame_socket_path.empty()) {
        std::cerr << "Usage: switchframe-cef --control-socket <path> --frame-socket <path> [--user-data-dir <path>] [--templates-dir <path>]"
                  << std::endl;
        return 1;
    }

    // Without a templates directory no file URL is loaded at all.
    const std::string templates_root = url_policy::ResolveRoot(templates_dir);
    if (templates_root.empty()) {
        std::cerr << "[main] no usable --templates-dir (" << templates_dir
                  << "): file URLs are refused" << std::endl;
    }

    // Default user data dir to a unique temp directory if not specified.
    if (user_data_dir.empty()) {
        user_data_dir = "/tmp/switchframe-cef-data-" + std::to_string(getpid());
    }

    // Resolve the directory containing the binary for resource paths.
    std::string exe_dir;
    {
        char buf[4096] = {};
#if defined(__APPLE__)
        uint32_t size = sizeof(buf);
        _NSGetExecutablePath(buf, &size);
#else
        ssize_t len = readlink("/proc/self/exe", buf, sizeof(buf) - 1);
        if (len > 0) buf[len] = '\0';
#endif
        exe_dir = std::string(buf);
        auto pos = exe_dir.rfind('/');
        if (pos != std::string::npos) exe_dir = exe_dir.substr(0, pos);
    }

    // Configure CEF
    CefSettings settings;
    settings.windowless_rendering_enabled = true;
    settings.no_sandbox = true;
    settings.background_color = CefColorSetARGB(0, 0, 0, 0);
    settings.log_severity = LOGSEVERITY_WARNING;
    CefString(&settings.root_cache_path) = user_data_dir;
    CefString(&settings.cache_path) = user_data_dir + "/cache";

#if defined(__APPLE__)
    CefString(&settings.framework_dir_path) =
        exe_dir + "/../Frameworks/Chromium Embedded Framework.framework";
    // CEF auto-discovers per-type Helpers in Contents/Frameworks/. The
    // bootstrap script creates Helper.app and Helper (GPU/Renderer/Plugin/Alerts).app
    // pointing at the same helper binary.
#else
    // Linux: CEF resources must be alongside the binary.
    CefString(&settings.resources_dir_path) = exe_dir;
    CefString(&settings.locales_dir_path) = exe_dir + "/locales";
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

    // One policy for every browser, installed both as each browser's request
    // handler and as the handler of the context they share, which shares its
    // storage with the global context.
    CefRefPtr<PolicyRequestHandler> policy(
        new PolicyRequestHandler(templates_root));
    CefRefPtr<CefRequestContext> request_context =
        CefRequestContext::CreateContext(CefRequestContext::GetGlobalContext(),
                                         policy);

    // Create browser pool wired to the frame socket.
    auto browser_pool = std::make_shared<BrowserPool>(
        4, [frame_socket](int id, int w, int h, const void* buf, size_t sz) {
            frame_socket->SendFrame(id, w, h, buf, sz);
        },
        policy, request_context);

    // Get adapter.js source for injection on page load.
    const std::string& adapter_js = adapter::GetAdapterJS();

    // Create control socket with command dispatch handler.
    // We use a raw ControlSocket pointer in the lambda since the handler
    // is set after construction via SetHandler.
    std::shared_ptr<ControlSocket> control_socket;
    auto command_handler = [browser_pool, &adapter_js, &control_socket](const ControlCommand& cmd) {
            auto* cs = control_socket.get();

            if (cmd.cmd == "create") {
                int w = cmd.width > 0 ? cmd.width : 1920;
                int h = cmd.height > 0 ? cmd.height : 1080;
                int fps = cmd.fps > 0 ? cmd.fps : 60;
                bool ok = browser_pool->Create(cmd.id, w, h, fps);

                if (ok) {
                    // Wire OnLoadEnd callback to inject adapter.js on every page load
                    // and poll for detected format.
                    auto& ctrl_for_cb = control_socket;
                    int slot_id = cmd.id;  // capture slot ID for callbacks
                    browser_pool->SetLoadEndCallback(cmd.id,
                        [browser_pool, &adapter_js, ctrl_for_cb, slot_id](int /*cef_browser_id*/, int http_status) {
                            (void)http_status;
                            // Inject adapter.js (use slot_id, not CEF browser ID)
                            browser_pool->Execute(slot_id, adapter_js);
                            // After a short delay, read the detected format and report it.
                            std::string format_check =
                                "setTimeout(function() {"
                                "  var fmt = window.__switchframe ? window.__switchframe.format : 'unknown';"
                                "  document.title = 'switchframe-format:' + fmt;"
                                "}, 200);";
                            browser_pool->Execute(slot_id, format_check);
                        });
                    // Wire title change callback to detect format.
                    // adapter.js sets document.title = "switchframe-format:<fmt>"
                    browser_pool->SetTitleChangeCallback(cmd.id,
                        [ctrl_for_cb, slot_id](int /*cef_browser_id*/, const std::string& title) {
                            const std::string fmt_prefix = "switchframe-format:";
                            if (title.compare(0, fmt_prefix.size(), fmt_prefix) == 0) {
                                std::cerr << "[main] Format detected: slot="
                                          << slot_id << " format="
                                          << title.substr(fmt_prefix.size()) << std::endl;
                            }
                            std::string event = protocol::MakeTitleEventResponse(slot_id, title);
                            if (!event.empty() && ctrl_for_cb) {
                                ctrl_for_cb->SendResponse(event);
                            }
                        });
                    browser_pool->SetLoadErrorCallback(cmd.id,
                        [ctrl_for_cb, slot_id](int /*cef_browser_id*/, const std::string& error) {
                            if (ctrl_for_cb) {
                                ctrl_for_cb->SendResponse(protocol::MakeEventResponse(
                                    slot_id, "load_error", "error", error));
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
        };
    control_socket = std::make_shared<ControlSocket>(control_fd, command_handler);

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

    // Cleanup. Stop()/Close() own the listening fds and close them; closing
    // (not just shutting down) the listeners is what unblocks the accept()
    // threads on macOS, so these must run BEFORE the joins below or a sidecar
    // whose Go peer never connected would hang here forever.
    control_socket->Stop();
    frame_socket->Close();

    // Wait for the frame accept thread if it hasn't finished.
    if (frame_accept.joinable()) {
        frame_accept.join();
    }

    unlink(control_socket_path.c_str());
    unlink(frame_socket_path.c_str());

    CefShutdown();
    return 0;
}
