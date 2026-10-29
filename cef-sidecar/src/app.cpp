#include "app.h"
#include "include/cef_command_line.h"

void SwitchframeCefApp::OnBeforeCommandLineProcessing(
    const CefString& process_type,
    CefRefPtr<CefCommandLine> command_line) {

    // Disable GPU compositing for reliable software rendering in offscreen mode
    command_line->AppendSwitch("disable-gpu");
    command_line->AppendSwitch("disable-gpu-compositing");

    // Enable transparent background support
    command_line->AppendSwitch("enable-begin-frame-scheduling");

    // Disable features that are unnecessary for offscreen rendering
    command_line->AppendSwitch("disable-extensions");
    command_line->AppendSwitch("disable-pdf-extension");

#if defined(__linux__)
    // Windowless rendering needs no display, but Chromium's default Ozone
    // platform on Linux is X11 and aborts at start without $DISPLAY.
    if (!command_line->HasSwitch("ozone-platform")) {
        command_line->AppendSwitchWithValue("ozone-platform", "headless");
    }
#endif

    // Without these Chromium registers with GCM and fetches component
    // updates at start, outbound traffic a graphics renderer has no use for.
    command_line->AppendSwitch("disable-background-networking");
    command_line->AppendSwitch("disable-component-update");
}

void SwitchframeCefApp::OnContextInitialized() {
    // Browser process context is ready.
    // Browser pool creation will be triggered from here in Task 10.
}
