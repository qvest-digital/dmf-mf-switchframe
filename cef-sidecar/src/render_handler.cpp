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
    size_t buffer_size = static_cast<size_t>(width) * static_cast<size_t>(height) * 4;
    std::cerr << "[render] OnPaint: browser=" << browser_id_
              << " size=" << width << "x" << height
              << " bytes=" << buffer_size << std::endl;
    on_frame_(browser_id_, width, height, buffer, buffer_size);
}
