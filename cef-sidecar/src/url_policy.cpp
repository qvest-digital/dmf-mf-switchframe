#include "url_policy.h"

#include <climits>
#include <cstdlib>

namespace url_policy {
namespace {

std::string Lower(std::string s) {
    for (char& c : s) {
        if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
    }
    return s;
}

int HexValue(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

// Decodes %XX escapes. Returns false for a malformed escape or a NUL byte,
// which would end the path early in a C string.
bool PercentDecode(const std::string& in, std::string* out) {
    out->clear();
    for (size_t i = 0; i < in.size(); i++) {
        char c = in[i];
        if (c == '%') {
            if (i + 2 >= in.size()) return false;
            int hi = HexValue(in[i + 1]);
            int lo = HexValue(in[i + 2]);
            if (hi < 0 || lo < 0) return false;
            c = static_cast<char>(hi * 16 + lo);
            i += 2;
        }
        if (c == '\0') return false;
        out->push_back(c);
    }
    return true;
}

std::string RealPath(const std::string& path) {
    char buf[PATH_MAX];
    if (realpath(path.c_str(), buf) == nullptr) return "";
    return buf;
}

// |rest| is what follows "scheme:". A host must follow "//".
bool HasHost(const std::string& rest) {
    return rest.size() > 2 && rest.compare(0, 2, "//") == 0 && rest[2] != '/';
}

bool FileAllowed(const std::string& rest, const std::string& root) {
    if (root.empty() || rest.compare(0, 2, "//") != 0) return false;
    size_t path_start = rest.find('/', 2);
    if (path_start == std::string::npos) return false;
    std::string host = Lower(rest.substr(2, path_start - 2));
    if (!host.empty() && host != "localhost") return false;

    std::string path = rest.substr(path_start);
    size_t end = path.find_first_of("?#");
    if (end != std::string::npos) path.resize(end);

    std::string decoded;
    if (!PercentDecode(path, &decoded)) return false;
    std::string target = RealPath(decoded);
    return target.size() > root.size() + 1 &&
           target.compare(0, root.size(), root) == 0 &&
           target[root.size()] == '/';
}

}  // namespace

std::string ResolveRoot(const std::string& dir) {
    if (dir.empty()) return "";
    return RealPath(dir);
}

bool Allowed(const std::string& url, const std::string& root) {
    size_t colon = url.find(':');
    if (colon == std::string::npos || colon == 0) return false;
    std::string scheme = Lower(url.substr(0, colon));
    std::string rest = url.substr(colon + 1);

    if (scheme == "https" || scheme == "wss") return HasHost(rest);
    if (scheme == "file") return FileAllowed(rest, root);
    if (scheme == "data" || scheme == "blob") return true;
    if (scheme == "about") return rest == "blank" || rest == "srcdoc";
    return false;
}

}  // namespace url_policy
