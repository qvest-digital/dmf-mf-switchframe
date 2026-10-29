#include "app.h"
#include "include/cef_command_line.h"

void SwitchframeCefApp::OnBeforeCommandLineProcessing(
    const CefString& process_type,
    CefRefPtr<CefCommandLine> command_line) {

    // A WebSocket handshake reaches neither CEF request handler, so the URL
    // policy cannot refuse ws://. Chromium picks the proxy for ws:// as for
    // http://, so plain http is sent to a proxy at loopback port 1, where
    // none runs, and loopback hosts are taken off the list Chromium never
    // proxies. The policy refuses plain http itself before any connection;
    // https and wss go direct.
    command_line->AppendSwitchWithValue("proxy-server", "http=127.0.0.1:1");
    command_line->AppendSwitchWithValue("proxy-bypass-list", "<-loopback>");

    // Disable GPU compositing for reliable software rendering in offscreen mode
    command_line->AppendSwitch("disable-gpu");
    command_line->AppendSwitch("disable-gpu-compositing");

    // Enable transparent background support
    command_line->AppendSwitch("enable-begin-frame-scheduling");

    // Disable features that are unnecessary for offscreen rendering
    command_line->AppendSwitch("disable-extensions");
    command_line->AppendSwitch("disable-pdf-extension");
}

void SwitchframeCefApp::OnContextInitialized() {
    // Browser process context is ready.
    // Browser pool creation will be triggered from here in Task 10.
}
