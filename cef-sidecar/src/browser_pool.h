#pragma once

#include <functional>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

#include "include/cef_browser.h"
#include "include/cef_client.h"
#include "include/cef_life_span_handler.h"
#include "include/cef_load_handler.h"

#include "render_handler.h"

// BrowserClient ties a CefBrowser to its OffscreenRenderHandler and provides
// lifecycle/load event callbacks for the control protocol.
class BrowserClient : public CefClient,
                      public CefLifeSpanHandler,
                      public CefLoadHandler {
public:
    using LoadEndCallback = std::function<void(int browser_id, int http_status)>;
    using LoadErrorCallback =
        std::function<void(int browser_id, const std::string& error)>;

    explicit BrowserClient(CefRefPtr<OffscreenRenderHandler> handler);

    // CefClient overrides
    CefRefPtr<CefRenderHandler> GetRenderHandler() override { return handler_; }
    CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
    CefRefPtr<CefLoadHandler> GetLoadHandler() override { return this; }

    // CefLifeSpanHandler overrides
    void OnAfterCreated(CefRefPtr<CefBrowser> browser) override;
    void OnBeforeClose(CefRefPtr<CefBrowser> browser) override;

    // CefLoadHandler overrides
    void OnLoadEnd(CefRefPtr<CefBrowser> browser, CefRefPtr<CefFrame> frame,
                   int httpStatusCode) override;
    void OnLoadError(CefRefPtr<CefBrowser> browser, CefRefPtr<CefFrame> frame,
                     ErrorCode errorCode, const CefString& errorText,
                     const CefString& failingUrl) override;

    void SetLoadEndCallback(LoadEndCallback cb) { on_load_end_ = std::move(cb); }
    void SetLoadErrorCallback(LoadErrorCallback cb) {
        on_load_error_ = std::move(cb);
    }

private:
    CefRefPtr<OffscreenRenderHandler> handler_;
    LoadEndCallback on_load_end_;
    LoadErrorCallback on_load_error_;

    IMPLEMENT_REFCOUNTING(BrowserClient);
    DISALLOW_COPY_AND_ASSIGN(BrowserClient);
};

// BrowserPool manages a fixed number of offscreen CefBrowser slots.
// Each slot can be independently created, navigated, scripted, and destroyed.
class BrowserPool {
public:
    // max_browsers: maximum simultaneous browser instances (slot IDs 0..N-1).
    // on_frame: called on the CEF UI thread when a browser paints a frame.
    BrowserPool(int max_browsers, FrameCallback on_frame);
    ~BrowserPool();

    // Create a browser in the given slot. Returns false if the slot is invalid,
    // already occupied, or creation fails.
    // Must be called on the CEF UI thread (CefCurrentlyOn(TID_UI)).
    bool Create(int id, int width, int height, int fps);

    // Navigate the browser in slot |id| to |url|.
    bool LoadURL(int id, const std::string& url);

    // Execute JavaScript in the browser's main frame.
    bool Execute(int id, const std::string& js);

    // Close and destroy the browser in slot |id|.
    void Destroy(int id);

    // Reverse-lookup: find the pool slot ID for a CefBrowser (by CEF identifier).
    // Returns -1 if not found.
    int GetID(CefRefPtr<CefBrowser> browser) const;

    // Set per-slot load-end and load-error callbacks (forwarded to BrowserClient).
    void SetLoadEndCallback(int id, BrowserClient::LoadEndCallback cb);
    void SetLoadErrorCallback(int id, BrowserClient::LoadErrorCallback cb);

private:
    struct BrowserSlot {
        CefRefPtr<CefBrowser> browser;
        CefRefPtr<OffscreenRenderHandler> handler;
        CefRefPtr<BrowserClient> client;
        bool active = false;
    };

    int max_browsers_;
    std::vector<BrowserSlot> slots_;
    FrameCallback on_frame_;
    mutable std::mutex mutex_;
};
