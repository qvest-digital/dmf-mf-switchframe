#pragma once

#include <functional>

#include "include/cef_render_handler.h"

// Callback invoked on each rendered frame.
// Parameters: browser_id, width, height, BGRA buffer pointer, buffer size in bytes.
using FrameCallback = std::function<void(int browser_id, int width, int height,
                                         const void* bgra, size_t size)>;

// OffscreenRenderHandler captures CEF's offscreen-rendered BGRA frames and
// forwards them via a callback. Each browser instance gets its own handler
// configured with the target resolution.
class OffscreenRenderHandler : public CefRenderHandler {
public:
    OffscreenRenderHandler(int browser_id, int width, int height,
                           FrameCallback callback);

    // CefRenderHandler interface
    void GetViewRect(CefRefPtr<CefBrowser> browser, CefRect& rect) override;
    void OnPaint(CefRefPtr<CefBrowser> browser, PaintElementType type,
                 const RectList& dirtyRects, const void* buffer,
                 int width, int height) override;

private:
    int browser_id_;
    int width_;
    int height_;
    FrameCallback on_frame_;

    IMPLEMENT_REFCOUNTING(OffscreenRenderHandler);
    DISALLOW_COPY_AND_ASSIGN(OffscreenRenderHandler);
};
