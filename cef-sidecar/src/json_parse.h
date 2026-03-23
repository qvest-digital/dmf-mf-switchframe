#pragma once

#include <string>

// Forward declaration to avoid circular includes.
struct ControlCommand;

namespace json_parse {

// Parse a JSON line into a ControlCommand.
// Uses simple string scanning for the known fixed set of keys.
// Returns true if at least the "cmd" field was found.
bool ParseControlCommand(const std::string& line, ControlCommand& cmd);

// Internal helpers exposed for testing.
namespace detail {

// Extract a string value for the given key from a JSON line.
// Looks for "key":"value" pattern. Returns empty string if not found.
std::string FindString(const std::string& json, const std::string& key);

// Extract an integer value for the given key from a JSON line.
// Looks for "key":123 pattern. Returns 0 if not found.
int FindInt(const std::string& json, const std::string& key);

}  // namespace detail
}  // namespace json_parse
