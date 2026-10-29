#include "request_policy.h"

#include <iostream>

#include "url_policy.h"

PolicyRequestHandler::PolicyRequestHandler(std::string root)
    : root_(std::move(root)) {}

bool PolicyRequestHandler::OnBeforeBrowse(CefRefPtr<CefBrowser> /*browser*/,
                                          CefRefPtr<CefFrame> /*frame*/,
                                          CefRefPtr<CefRequest> request,
                                          bool /*user_gesture*/,
                                          bool /*is_redirect*/) {
    const std::string url = request->GetURL().ToString();
    if (url_policy::Allowed(url, root_)) {
        return false;
    }
    std::cerr << "[policy] refused navigation: " << url << std::endl;
    return true;
}

CefRefPtr<CefResourceRequestHandler>
PolicyRequestHandler::GetResourceRequestHandler(
    CefRefPtr<CefBrowser> /*browser*/, CefRefPtr<CefFrame> /*frame*/,
    CefRefPtr<CefRequest> /*request*/, bool /*is_navigation*/,
    bool /*is_download*/, const CefString& /*request_initiator*/,
    bool& /*disable_default_handling*/) {
    return this;
}

CefResourceRequestHandler::ReturnValue
PolicyRequestHandler::OnBeforeResourceLoad(CefRefPtr<CefBrowser> /*browser*/,
                                           CefRefPtr<CefFrame> /*frame*/,
                                           CefRefPtr<CefRequest> request,
                                           CefRefPtr<CefCallback> /*callback*/) {
    const std::string url = request->GetURL().ToString();
    if (url_policy::Allowed(url, root_)) {
        return RV_CONTINUE;
    }
    std::cerr << "[policy] refused load: " << url << std::endl;
    return RV_CANCEL;
}
