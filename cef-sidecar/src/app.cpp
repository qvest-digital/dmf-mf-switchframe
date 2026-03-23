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
}

void SwitchframeCefApp::OnContextInitialized() {
    // Browser process context is ready.
    // Browser pool creation will be triggered from here in Task 10.
}
