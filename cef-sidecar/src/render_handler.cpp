#include "render_handler.h"
#include <iostream>

OffscreenRenderHandler::OffscreenRenderHandler(int browser_id, int width,
                                               int height,
                                               FrameCallback callback)
    : browser_id_(browser_id),
      width_(width),
      height_(height),
      on_frame_(std::move(callback)) {}

void OffscreenRenderHandler::GetViewRect(CefRefPtr<CefBrowser> /*browser*/,
                                         CefRect& rect) {
    // Report the configured virtual screen size to CEF.
    rect = CefRect(0, 0, width_, height_);
}

void OffscreenRenderHandler::OnPaint(CefRefPtr<CefBrowser> /*browser*/,
                                     PaintElementType type,
                                     const RectList& /*dirtyRects*/,
                                     const void* buffer, int width,
                                     int height) {
    // We only care about the main view, not popups.
    if (type != PET_VIEW) {
        return;
    }

    if (!on_frame_) {
        std::cerr << "[render] OnPaint: no callback for browser " << browser_id_ << std::endl;
        return;
    }

    // BGRA: 4 bytes per pixel.
    // No per-frame logging here: OnPaint runs on the contended CEF UI thread at
    // up to 60fps per browser, and std::cerr is unbuffered (one syscall per
    // write), which would add I/O latency directly on the frame-production hot
    // path. Diagnostics for missing callbacks are handled above (one-shot).
    size_t buffer_size = static_cast<size_t>(width) * static_cast<size_t>(height) * 4;
    on_frame_(browser_id_, width, height, buffer, buffer_size);
}
