#include "json_parse.h"
#include "control_socket.h"

#include <cstdlib>
#include <string>

namespace json_parse {
namespace detail {

std::string FindString(const std::string& json, const std::string& key) {
    // Search for "key":"value"
    std::string pattern = "\"" + key + "\":\"";
    auto pos = json.find(pattern);
    if (pos == std::string::npos) {
        return "";
    }
    pos += pattern.size();

    // Find the closing quote, handling escaped quotes.
    std::string result;
    for (size_t i = pos; i < json.size(); i++) {
        if (json[i] == '\\' && i + 1 < json.size()) {
            // Escaped character -- include the next char as-is.
            result += json[i + 1];
            i++;
        } else if (json[i] == '"') {
            break;
        } else {
            result += json[i];
        }
    }

    return result;
}

int FindInt(const std::string& json, const std::string& key) {
    // Search for "key":number
    std::string pattern = "\"" + key + "\":";
    auto pos = json.find(pattern);
    if (pos == std::string::npos) {
        return 0;
    }
    pos += pattern.size();

    // Skip whitespace.
    while (pos < json.size() && (json[pos] == ' ' || json[pos] == '\t')) {
        pos++;
    }

    if (pos >= json.size()) {
        return 0;
    }

    // Parse the number (handles negative values).
    try {
        return std::stoi(json.substr(pos));
    } catch (...) {
        return 0;
    }
}

}  // namespace detail

bool ParseControlCommand(const std::string& line, ControlCommand& cmd) {
    cmd.cmd = detail::FindString(line, "cmd");
    if (cmd.cmd.empty()) {
        return false;
    }

    cmd.id = detail::FindInt(line, "id");
    cmd.width = detail::FindInt(line, "width");
    cmd.height = detail::FindInt(line, "height");
    cmd.fps = detail::FindInt(line, "fps");
    cmd.url = detail::FindString(line, "url");
    cmd.data = detail::FindString(line, "data");
    cmd.action = detail::FindString(line, "action");
    cmd.payload = detail::FindString(line, "payload");

    return true;
}

}  // namespace json_parse
