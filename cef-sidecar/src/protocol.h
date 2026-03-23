#pragma once

#include <cstdint>
#include <cstring>
#include <stdexcept>
#include <string>
#include <vector>

namespace protocol {

// Frame header size: 1 (browser_id) + 2 (width) + 2 (height) = 5 bytes.
// Must match Go constant FrameHeaderSize in server/htmlgfx/protocol.go.
constexpr size_t kFrameHeaderSize = 5;

// Maximum frame pixel data size (8K UHD: 7680 * 4320 * 4).
// Must match Go constant maxFramePixels in server/htmlgfx/protocol.go.
constexpr size_t kMaxFramePixels = 7680 * 4320 * 4;

// FrameHeader parsed from the wire.
struct FrameHeader {
    uint8_t browser_id;
    uint16_t width;
    uint16_t height;
};

// Parse a 4-byte big-endian length prefix.
inline uint32_t ParseLengthPrefix(const uint8_t* data) {
    return (static_cast<uint32_t>(data[0]) << 24) |
           (static_cast<uint32_t>(data[1]) << 16) |
           (static_cast<uint32_t>(data[2]) << 8) |
           static_cast<uint32_t>(data[3]);
}

// Parse a frame header from raw bytes (must be at least kFrameHeaderSize bytes).
inline FrameHeader ParseFrameHeader(const uint8_t* data) {
    FrameHeader hdr;
    hdr.browser_id = data[0];
    hdr.width = (static_cast<uint16_t>(data[1]) << 8) | data[2];
    hdr.height = (static_cast<uint16_t>(data[3]) << 8) | data[4];
    return hdr;
}

// Serialize a frame header + pixel data into a length-prefixed binary message.
//
// Wire format (matches Go WriteFrame in server/htmlgfx/protocol.go):
//   [4 bytes: total_length (uint32 big-endian)] -- length of everything after these 4 bytes
//   [1 byte:  browser_id]
//   [2 bytes: width (uint16 big-endian)]
//   [2 bytes: height (uint16 big-endian)]
//   [width * height * 4 bytes: BGRA pixels]
inline std::vector<uint8_t> SerializeFrame(uint8_t browser_id, uint16_t width,
                                            uint16_t height, const void* bgra,
                                            size_t bgra_size) {
    size_t expected = static_cast<size_t>(width) * height * 4;
    if (bgra_size != expected) {
        throw std::invalid_argument("pixel data size mismatch");
    }

    uint32_t total_len = static_cast<uint32_t>(kFrameHeaderSize + bgra_size);
    std::vector<uint8_t> buf(4 + total_len);

    // Length prefix (big-endian).
    buf[0] = (total_len >> 24) & 0xFF;
    buf[1] = (total_len >> 16) & 0xFF;
    buf[2] = (total_len >> 8) & 0xFF;
    buf[3] = total_len & 0xFF;

    // Header.
    buf[4] = browser_id;
    buf[5] = (width >> 8) & 0xFF;
    buf[6] = width & 0xFF;
    buf[7] = (height >> 8) & 0xFF;
    buf[8] = height & 0xFF;

    // Pixels.
    std::memcpy(buf.data() + 9, bgra, bgra_size);

    return buf;
}

// Serialize a JSON "ok" control response as a newline-delimited string.
inline std::string MakeOkResponse(int id) {
    return "{\"id\":" + std::to_string(id) + ",\"status\":\"ok\"}\n";
}

// Serialize a JSON "error" control response as a newline-delimited string.
inline std::string MakeErrorResponse(int id, const std::string& error) {
    return "{\"id\":" + std::to_string(id) +
           ",\"status\":\"error\",\"error\":\"" + error + "\"}\n";
}

// Serialize a JSON event response as a newline-delimited string.
inline std::string MakeEventResponse(int id, const std::string& event,
                                      const std::string& extra_key = "",
                                      const std::string& extra_value = "") {
    std::string resp =
        "{\"id\":" + std::to_string(id) + ",\"event\":\"" + event + "\"";
    if (!extra_key.empty()) {
        resp += ",\"" + extra_key + "\":\"" + extra_value + "\"";
    }
    resp += "}\n";
    return resp;
}

}  // namespace protocol
