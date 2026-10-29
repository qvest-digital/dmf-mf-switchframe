#pragma once

#include <string>

#include "include/cef_request_context_handler.h"
#include "include/cef_request_handler.h"
#include "include/cef_resource_request_handler.h"

// Applies url_policy::Allowed to every navigation and every resource load of
// a browser. CEF calls OnBeforeBrowse for each navigation of any frame,
// redirects included, and OnBeforeResourceLoad again for the new URL after a
// resource redirect.
//
// CEF asks a browser's client for its CefRequestHandler only for a request
// that has a browser; a request from a service worker has none and goes to
// the CefRequestContextHandler of the browser's request context instead. The
// handler is installed in both places, so the policy does not depend on
// which one CEF consults. The one request neither sees is a service worker's
// own script, which has to share the https origin of the page registering it
// and cannot be redirected, so it stays inside the policy.
class PolicyRequestHandler : public CefRequestHandler,
                             public CefRequestContextHandler,
                             public CefResourceRequestHandler {
public:
    // |root| is a url_policy::ResolveRoot result.
    explicit PolicyRequestHandler(std::string root);

    // CefRequestHandler
    bool OnBeforeBrowse(CefRefPtr<CefBrowser> browser,
                        CefRefPtr<CefFrame> frame,
                        CefRefPtr<CefRequest> request, bool user_gesture,
                        bool is_redirect) override;

    // CefRequestHandler and CefRequestContextHandler, which declare it with
    // the same signature; |browser| and |frame| are null from the latter.
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
