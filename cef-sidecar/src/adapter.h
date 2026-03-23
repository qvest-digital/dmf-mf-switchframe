#pragma once

#include <string>

namespace adapter {

// Returns the adapter.js source code as a string.
// This script is injected into every loaded HTML page to detect template
// format (OGraf / CasparCG) and provide a uniform play/stop/next/update API.
const std::string& GetAdapterJS();

}  // namespace adapter
