#pragma once

#include <string>

// What the sidecar may navigate to or load. The browsers are unsandboxed
// Chromium whose pixels go to program, so a URL a page reaches is as good as
// putting what it names on air. Chromium applies this to every navigation,
// redirects and frames included, and to every resource load, so it holds
// whatever a template's own script does.
namespace url_policy {

// ResolveRoot returns |dir| with every symlink resolved, or "" when |dir| is
// empty or cannot be resolved; an empty root allows no file URL at all.
std::string ResolveRoot(const std::string& dir);

// Allowed reports whether |url| may be loaded:
//   https and wss  any host
//   file           only a file strictly below |root| once symlinks are
//                  resolved, so a link cannot point out of it
//   data and blob  content the page already holds, reaching no host or file
//   about          about:blank and about:srcdoc only
// Everything else is refused, plain http and ws included: they reach
// cluster-internal services with no certificate naming them.
bool Allowed(const std::string& url, const std::string& root);

}  // namespace url_policy
