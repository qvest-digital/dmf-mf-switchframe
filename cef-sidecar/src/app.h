#pragma once

#include "include/cef_app.h"

// SwitchframeCefApp implements the CefApp interface for process-level callbacks.
class SwitchframeCefApp : public CefApp, public CefBrowserProcessHandler {
public:
    SwitchframeCefApp() = default;

    // CefApp methods
    CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override {
        return this;
    }

    void OnBeforeCommandLineProcessing(
        const CefString& process_type,
        CefRefPtr<CefCommandLine> command_line) override;

    // CefBrowserProcessHandler methods
    void OnContextInitialized() override;

private:
    IMPLEMENT_REFCOUNTING(SwitchframeCefApp);
    DISALLOW_COPY_AND_ASSIGN(SwitchframeCefApp);
};
