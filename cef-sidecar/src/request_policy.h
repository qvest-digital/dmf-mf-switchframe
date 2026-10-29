#pragma once

#include <string>

#include "include/cef_request_handler.h"
#include "include/cef_resource_request_handler.h"

// Applies url_policy::Allowed to every navigation and every resource load of
// a browser. CEF calls OnBeforeBrowse for each navigation of any frame,
// redirects included, and OnBeforeResourceLoad again for the new URL after a
// resource redirect.
class PolicyRequestHandler : public CefRequestHandler,
                             public CefResourceRequestHandler {
public:
    // |root| is a url_policy::ResolveRoot result.
    explicit PolicyRequestHandler(std::string root);

    // CefRequestHandler
    bool OnBeforeBrowse(CefRefPtr<CefBrowser> browser,
                        CefRefPtr<CefFrame> frame,
                        CefRefPtr<CefRequest> request, bool user_gesture,
                        bool is_redirect) override;
    CefRefPtr<CefResourceRequestHandler> GetResourceRequestHandler(
        CefRefPtr<CefBrowser> browser, CefRefPtr<CefFrame> frame,
        CefRefPtr<CefRequest> request, bool is_navigation, bool is_download,
        const CefString& request_initiator,
        bool& disable_default_handling) override;

    // CefResourceRequestHandler
    ReturnValue OnBeforeResourceLoad(CefRefPtr<CefBrowser> browser,
                                     CefRefPtr<CefFrame> frame,
                                     CefRefPtr<CefRequest> request,
                                     CefRefPtr<CefCallback> callback) override;

private:
    const std::string root_;

    IMPLEMENT_REFCOUNTING(PolicyRequestHandler);
    DISALLOW_COPY_AND_ASSIGN(PolicyRequestHandler);
};
