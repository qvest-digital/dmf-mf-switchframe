#include "json_parse.h"
#include "control_socket.h"

#include <cstddef>
#include <cstdint>
#include <cstdlib>
#include <string>

namespace json_parse {
namespace detail {

namespace {

// Skip JSON insignificant whitespace starting at pos.
void SkipWhitespace(const std::string& json, size_t& pos) {
    while (pos < json.size() &&
           (json[pos] == ' ' || json[pos] == '\t' || json[pos] == '\n' ||
            json[pos] == '\r')) {
        pos++;
    }
}

// Append the UTF-8 encoding of Unicode code point |cp| to |out|.
void AppendUtf8(std::string& out, uint32_t cp) {
    if (cp < 0x80) {
        out += static_cast<char>(cp);
    } else if (cp < 0x800) {
        out += static_cast<char>(0xC0 | (cp >> 6));
        out += static_cast<char>(0x80 | (cp & 0x3F));
    } else if (cp < 0x10000) {
        out += static_cast<char>(0xE0 | (cp >> 12));
        out += static_cast<char>(0x80 | ((cp >> 6) & 0x3F));
        out += static_cast<char>(0x80 | (cp & 0x3F));
    } else {
        out += static_cast<char>(0xF0 | (cp >> 18));
        out += static_cast<char>(0x80 | ((cp >> 12) & 0x3F));
        out += static_cast<char>(0x80 | ((cp >> 6) & 0x3F));
        out += static_cast<char>(0x80 | (cp & 0x3F));
    }
}

// Parse exactly four hex digits at json[pos..pos+3] into |out|. Returns false
// if fewer than four characters remain or any character is not a hex digit.
bool ParseHex4(const std::string& json, size_t pos, uint32_t& out) {
    if (pos + 4 > json.size()) {
        return false;
    }
    uint32_t v = 0;
    for (size_t i = 0; i < 4; i++) {
        char c = json[pos + i];
        v <<= 4;
        if (c >= '0' && c <= '9') {
            v |= static_cast<uint32_t>(c - '0');
        } else if (c >= 'a' && c <= 'f') {
            v |= static_cast<uint32_t>(c - 'a' + 10);
        } else if (c >= 'A' && c <= 'F') {
            v |= static_cast<uint32_t>(c - 'A' + 10);
        } else {
            return false;
        }
    }
    out = v;
    return true;
}

// Parse a JSON string starting at the opening quote (json[pos] == '"').
// Advances pos to just past the closing quote and returns the unescaped
// contents. On malformed input (no closing quote) pos is advanced to end and
// whatever was accumulated is returned.
//
// Full RFC 8259 escape decoding is required here: the trusted Go peer builds
// these messages with encoding/json, which HTML-escapes by default ('&' as
// unicode escape 0026, '<' as 003c, '>' as 003e) and escapes control
// characters (newline as backslash-n, etc.). URLs with query parameters and
// JSON/HTML payloads would otherwise be silently corrupted. Malformed escapes
// are kept verbatim (lenient) rather than failing the whole parse.
std::string ParseStringAt(const std::string& json, size_t& pos) {
    std::string result;
    pos++;  // skip opening quote
    while (pos < json.size()) {
        char c = json[pos];
        if (c == '\\' && pos + 1 < json.size()) {
            char esc = json[pos + 1];
            switch (esc) {
                case '"':
                    result += '"';
                    pos += 2;
                    break;
                case '\\':
                    result += '\\';
                    pos += 2;
                    break;
                case '/':
                    result += '/';
                    pos += 2;
                    break;
                case 'b':
                    result += '\b';
                    pos += 2;
                    break;
                case 'f':
                    result += '\f';
                    pos += 2;
                    break;
                case 'n':
                    result += '\n';
                    pos += 2;
                    break;
                case 'r':
                    result += '\r';
                    pos += 2;
                    break;
                case 't':
                    result += '\t';
                    pos += 2;
                    break;
                case 'u': {
                    uint32_t cp = 0;
                    if (!ParseHex4(json, pos + 2, cp)) {
                        // Malformed escape: keep the raw text (lenient).
                        result += '\\';
                        result += 'u';
                        pos += 2;
                        break;
                    }
                    pos += 6;
                    if (cp >= 0xD800 && cp <= 0xDBFF) {
                        // High surrogate: combine with the following low
                        // surrogate to form a supplementary code point.
                        uint32_t lo = 0;
                        if (pos + 1 < json.size() && json[pos] == '\\' &&
                            json[pos + 1] == 'u' &&
                            ParseHex4(json, pos + 2, lo) && lo >= 0xDC00 &&
                            lo <= 0xDFFF) {
                            cp = 0x10000 + ((cp - 0xD800) << 10) +
                                 (lo - 0xDC00);
                            pos += 6;
                        } else {
                            cp = 0xFFFD;  // unpaired high surrogate
                        }
                    } else if (cp >= 0xDC00 && cp <= 0xDFFF) {
                        cp = 0xFFFD;  // unpaired low surrogate
                    }
                    AppendUtf8(result, cp);
                    break;
                }
                default:
                    // Unknown escape: keep both characters (lenient).
                    result += '\\';
                    result += esc;
                    pos += 2;
                    break;
            }
        } else if (c == '"') {
            pos++;  // skip closing quote
            return result;
        } else {
            result += c;
            pos++;
        }
    }
    return result;
}

// Skip over a JSON string value (json[pos] == '"'), advancing pos past the
// closing quote without materializing the contents.
void SkipStringAt(const std::string& json, size_t& pos) {
    pos++;  // skip opening quote
    while (pos < json.size()) {
        char c = json[pos];
        if (c == '\\' && pos + 1 < json.size()) {
            pos += 2;
        } else if (c == '"') {
            pos++;
            return;
        } else {
            pos++;
        }
    }
}

// Skip over any JSON value starting at pos (object, array, string, or bare
// token), advancing pos to just past it. Nested objects/arrays are skipped in
// a balanced fashion so their inner keys are never treated as top-level keys.
void SkipValue(const std::string& json, size_t& pos) {
    SkipWhitespace(json, pos);
    if (pos >= json.size()) {
        return;
    }
    char c = json[pos];
    if (c == '"') {
        SkipStringAt(json, pos);
        return;
    }
    if (c == '{' || c == '[') {
        int depth = 0;
        while (pos < json.size()) {
            char d = json[pos];
            if (d == '"') {
                // Strings inside nested containers may contain braces/brackets;
                // skip them so they don't affect the depth count.
                SkipStringAt(json, pos);
                continue;
            }
            if (d == '{' || d == '[') {
                depth++;
            } else if (d == '}' || d == ']') {
                depth--;
                pos++;
                if (depth == 0) {
                    return;
                }
                continue;
            }
            pos++;
        }
        return;
    }
    // Bare token (number, true, false, null): consume until a structural
    // delimiter.
    while (pos < json.size()) {
        char d = json[pos];
        if (d == ',' || d == '}' || d == ']' || d == ' ' || d == '\t' ||
            d == '\n' || d == '\r') {
            return;
        }
        pos++;
    }
}

// Locate the value for |key| at the top level (depth 1) of the JSON object in
// |json|. On success returns true with |value_pos| pointing at the first
// character of the value (after the colon and any whitespace). Returns false
// if the key is not present as a top-level object key. Keys that only appear
// inside string values or nested objects/arrays are ignored, which is the
// whole point of this structural scan.
bool FindTopLevelKey(const std::string& json, const std::string& key,
                     size_t& value_pos) {
    size_t pos = 0;
    SkipWhitespace(json, pos);
    if (pos >= json.size() || json[pos] != '{') {
        return false;
    }
    pos++;  // enter top-level object

    while (pos < json.size()) {
        SkipWhitespace(json, pos);
        if (pos >= json.size() || json[pos] == '}') {
            return false;
        }
        if (json[pos] == ',') {
            pos++;
            continue;
        }
        if (json[pos] != '"') {
            // Malformed at object scope -- give up rather than guess.
            return false;
        }
        std::string this_key = ParseStringAt(json, pos);
        SkipWhitespace(json, pos);
        if (pos >= json.size() || json[pos] != ':') {
            return false;
        }
        pos++;  // skip colon
        SkipWhitespace(json, pos);
        if (this_key == key) {
            value_pos = pos;
            return true;
        }
        SkipValue(json, pos);
    }
    return false;
}

}  // namespace

std::string FindString(const std::string& json, const std::string& key) {
    size_t pos = 0;
    if (!FindTopLevelKey(json, key, pos)) {
        return "";
    }
    if (pos >= json.size() || json[pos] != '"') {
        // Key exists but its value is not a string.
        return "";
    }
    return ParseStringAt(json, pos);
}

int FindInt(const std::string& json, const std::string& key) {
    size_t pos = 0;
    if (!FindTopLevelKey(json, key, pos)) {
        return 0;
    }
    if (pos >= json.size()) {
        return 0;
    }
    // Parse the number (handles negative values). std::stoi stops at the first
    // non-numeric character, so a trailing comma/brace is fine.
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
