#include "browser_pool.h"

#include <iostream>

#include "include/cef_browser.h"

// ---------- BrowserClient ----------

BrowserClient::BrowserClient(CefRefPtr<OffscreenRenderHandler> handler)
    : handler_(handler) {}

void BrowserClient::OnAfterCreated(CefRefPtr<CefBrowser> browser) {
    std::cerr << "[browser] OnAfterCreated: id=" << browser->GetIdentifier() << std::endl;
}

void BrowserClient::OnBeforeClose(CefRefPtr<CefBrowser> browser) {
    std::cerr << "[browser] OnBeforeClose: id=" << browser->GetIdentifier() << std::endl;
}

void BrowserClient::OnLoadEnd(CefRefPtr<CefBrowser> browser,
                              CefRefPtr<CefFrame> frame, int httpStatusCode) {
    std::cerr << "[browser] OnLoadEnd: id=" << browser->GetIdentifier()
              << " url=" << frame->GetURL().ToString()
              << " status=" << httpStatusCode
              << " main=" << frame->IsMain() << std::endl;

    if (!frame->IsMain()) {
        return;
    }

    if (on_load_end_) {
        std::cerr << "[browser] Firing on_load_end callback" << std::endl;
        on_load_end_(browser->GetIdentifier(), httpStatusCode);
    } else {
        std::cerr << "[browser] No on_load_end callback set!" << std::endl;
    }
}

void BrowserClient::OnLoadError(CefRefPtr<CefBrowser> browser,
                                CefRefPtr<CefFrame> frame,
                                ErrorCode errorCode,
                                const CefString& errorText,
                                const CefString& failingUrl) {
    std::cerr << "[browser] OnLoadError: id=" << browser->GetIdentifier()
              << " error=" << errorCode
              << " text=" << errorText.ToString()
              << " url=" << failingUrl.ToString() << std::endl;

    if (!frame->IsMain()) {
        return;
    }

    if (on_load_error_) {
        std::string msg = "Load error " + std::to_string(errorCode) + ": " +
                          errorText.ToString() + " (" + failingUrl.ToString() +
                          ")";
        on_load_error_(browser->GetIdentifier(), msg);
    }
}

void BrowserClient::OnTitleChange(CefRefPtr<CefBrowser> browser,
                                   const CefString& title) {
    std::string t = title.ToString();
    std::cerr << "[browser] OnTitleChange: id=" << browser->GetIdentifier()
              << " title=" << t << std::endl;
    if (on_title_change_) {
        on_title_change_(browser->GetIdentifier(), t);
    }
}

// ---------- BrowserPool ----------

BrowserPool::BrowserPool(int max_browsers, FrameCallback on_frame)
    : max_browsers_(max_browsers),
      slots_(static_cast<size_t>(max_browsers)),
      on_frame_(std::move(on_frame)) {}

BrowserPool::~BrowserPool() {
    // Close all active browsers.
    std::lock_guard<std::mutex> lock(mutex_);
    for (auto& slot : slots_) {
        if (slot.active && slot.browser) {
            slot.browser->GetHost()->CloseBrowser(true);
            slot.active = false;
        }
    }
}

bool BrowserPool::Create(int id, int width, int height, int fps) {
    std::lock_guard<std::mutex> lock(mutex_);

    if (id < 0 || id >= max_browsers_) {
        std::cerr << "BrowserPool::Create: invalid slot " << id << std::endl;
        return false;
    }

    BrowserSlot& slot = slots_[static_cast<size_t>(id)];
    if (slot.active) {
        std::cerr << "BrowserPool::Create: slot " << id << " already active"
                  << std::endl;
        return false;
    }

    // Create the render handler for this slot.
    CefRefPtr<OffscreenRenderHandler> handler(
        new OffscreenRenderHandler(id, width, height, on_frame_));

    // Create the client that wires up render + lifecycle + load handlers.
    CefRefPtr<BrowserClient> client(new BrowserClient(handler));

    // Configure the browser window (offscreen).
    CefWindowInfo window_info;
    window_info.SetAsWindowless(0);

    CefBrowserSettings browser_settings;
    browser_settings.windowless_frame_rate = fps;
    // Transparent background (ARGB = 0x00000000).
    browser_settings.background_color = CefColorSetARGB(0, 0, 0, 0);

    // CreateBrowserSync must be called on the CEF UI thread.
    CefRefPtr<CefBrowser> browser = CefBrowserHost::CreateBrowserSync(
        window_info, client, "about:blank", browser_settings, nullptr, nullptr);

    if (!browser) {
        std::cerr << "BrowserPool::Create: CreateBrowserSync failed for slot "
                  << id << std::endl;
        return false;
    }

    slot.browser = browser;
    slot.handler = handler;
    slot.client = client;
    slot.active = true;

    std::cerr << "[pool] Created browser slot " << id
              << " (" << width << "x" << height << " @" << fps << "fps)"
              << " cef_id=" << browser->GetIdentifier() << std::endl;

    return true;
}

bool BrowserPool::LoadURL(int id, const std::string& url) {
    std::lock_guard<std::mutex> lock(mutex_);

    if (id < 0 || id >= max_browsers_) {
        return false;
    }

    BrowserSlot& slot = slots_[static_cast<size_t>(id)];
    if (!slot.active || !slot.browser) {
        return false;
    }

    CefRefPtr<CefFrame> frame = slot.browser->GetMainFrame();
    if (!frame) {
        return false;
    }

    std::cerr << "[pool] LoadURL: slot=" << id << " url=" << url << std::endl;
    frame->LoadURL(url);
    return true;
}

bool BrowserPool::Execute(int id, const std::string& js) {
    std::lock_guard<std::mutex> lock(mutex_);

    if (id < 0 || id >= max_browsers_) {
        return false;
    }

    BrowserSlot& slot = slots_[static_cast<size_t>(id)];
    if (!slot.active || !slot.browser) {
        return false;
    }

    CefRefPtr<CefFrame> frame = slot.browser->GetMainFrame();
    if (!frame) {
        return false;
    }

    std::cerr << "[pool] Execute: slot=" << id << " js_len=" << js.size() << std::endl;
    frame->ExecuteJavaScript(js, "", 0);
    return true;
}

void BrowserPool::Destroy(int id) {
    std::lock_guard<std::mutex> lock(mutex_);

    if (id < 0 || id >= max_browsers_) {
        return;
    }

    BrowserSlot& slot = slots_[static_cast<size_t>(id)];
    if (!slot.active) {
        return;
    }

    if (slot.browser) {
        slot.browser->GetHost()->CloseBrowser(true);
    }

    slot.browser = nullptr;
    slot.handler = nullptr;
    slot.client = nullptr;
    slot.active = false;
}

int BrowserPool::GetID(CefRefPtr<CefBrowser> browser) const {
    std::lock_guard<std::mutex> lock(mutex_);

    int target = browser->GetIdentifier();
    for (int i = 0; i < max_browsers_; i++) {
        const BrowserSlot& slot = slots_[static_cast<size_t>(i)];
        if (slot.active && slot.browser &&
            slot.browser->GetIdentifier() == target) {
            return i;
        }
    }

    return -1;
}

void BrowserPool::SetLoadEndCallback(int id, BrowserClient::LoadEndCallback cb) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (id < 0 || id >= max_browsers_) return;
    BrowserSlot& slot = slots_[static_cast<size_t>(id)];
    if (slot.active && slot.client) {
        slot.client->SetLoadEndCallback(std::move(cb));
    }
}

void BrowserPool::SetLoadErrorCallback(int id, BrowserClient::LoadErrorCallback cb) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (id < 0 || id >= max_browsers_) return;
    BrowserSlot& slot = slots_[static_cast<size_t>(id)];
    if (slot.active && slot.client) {
        slot.client->SetLoadErrorCallback(std::move(cb));
    }
}

void BrowserPool::SetTitleChangeCallback(int id, BrowserClient::TitleChangeCallback cb) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (id < 0 || id >= max_browsers_) return;
    BrowserSlot& slot = slots_[static_cast<size_t>(id)];
    if (slot.active && slot.client) {
        slot.client->SetTitleChangeCallback(std::move(cb));
    }
}
