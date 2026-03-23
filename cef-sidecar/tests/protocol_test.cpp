#include <cstring>
#include <functional>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

#include "../src/protocol.h"

// ---------------------------------------------------------------------------
// Minimal test framework (no external dependencies)
// ---------------------------------------------------------------------------

struct TestCase {
    std::string name;
    std::function<void()> fn;
};

static std::vector<TestCase>& test_registry() {
    static std::vector<TestCase> tests;
    return tests;
}

#define TEST(name)                                                       \
    static void test_##name();                                           \
    static bool registered_##name = [] {                                 \
        test_registry().push_back({#name, test_##name});                 \
        return true;                                                     \
    }();                                                                 \
    static void test_##name()

#define ASSERT_EQ(a, b)                                                  \
    do {                                                                 \
        auto _a = (a);                                                   \
        auto _b = (b);                                                   \
        if (_a != _b) {                                                  \
            std::ostringstream ss;                                       \
            ss << "Expected " << _b << " but got " << _a                \
               << " at line " << __LINE__;                               \
            throw std::runtime_error(ss.str());                          \
        }                                                                \
    } while (0)

#define ASSERT_THROW(expr, exc_type)                                     \
    do {                                                                 \
        bool caught = false;                                             \
        try {                                                            \
            expr;                                                        \
        } catch (const exc_type&) {                                      \
            caught = true;                                               \
        }                                                                \
        if (!caught)                                                     \
            throw std::runtime_error("Expected exception at line " +     \
                                     std::to_string(__LINE__));          \
    } while (0)

// ---------------------------------------------------------------------------
// Frame Serialization Tests
// ---------------------------------------------------------------------------

TEST(SerializeFrame_BasicRoundTrip) {
    uint8_t bgra[16] = {};  // 2x2 frame, 4 bytes per pixel
    bgra[0] = 0xFF;         // B
    bgra[1] = 0x00;         // G
    bgra[2] = 0x00;         // R
    bgra[3] = 0xFF;         // A (opaque blue)

    auto buf = protocol::SerializeFrame(3, 2, 2, bgra, sizeof(bgra));

    // Total size: 4 (length) + 5 (header) + 16 (pixels) = 25.
    ASSERT_EQ(buf.size(), size_t(25));

    // Parse length prefix.
    uint32_t total_len = protocol::ParseLengthPrefix(buf.data());
    ASSERT_EQ(total_len, uint32_t(21));  // 5 + 16

    // Parse header.
    auto hdr = protocol::ParseFrameHeader(buf.data() + 4);
    ASSERT_EQ(hdr.browser_id, uint8_t(3));
    ASSERT_EQ(hdr.width, uint16_t(2));
    ASSERT_EQ(hdr.height, uint16_t(2));

    // Verify first pixel.
    ASSERT_EQ(buf[9], uint8_t(0xFF));   // B
    ASSERT_EQ(buf[10], uint8_t(0x00));  // G
    ASSERT_EQ(buf[11], uint8_t(0x00));  // R
    ASSERT_EQ(buf[12], uint8_t(0xFF));  // A
}

TEST(SerializeFrame_LargerDimensions) {
    // 640x480 frame.
    std::vector<uint8_t> bgra(640 * 480 * 4, 0xAA);
    auto buf =
        protocol::SerializeFrame(0, 640, 480, bgra.data(), bgra.size());

    uint32_t total_len = protocol::ParseLengthPrefix(buf.data());
    ASSERT_EQ(total_len,
              uint32_t(protocol::kFrameHeaderSize + bgra.size()));

    auto hdr = protocol::ParseFrameHeader(buf.data() + 4);
    ASSERT_EQ(hdr.width, uint16_t(640));
    ASSERT_EQ(hdr.height, uint16_t(480));
}

TEST(SerializeFrame_SizeMismatch) {
    uint8_t bgra[8] = {};  // 8 bytes but 2x2 needs 16
    ASSERT_THROW(protocol::SerializeFrame(0, 2, 2, bgra, sizeof(bgra)),
                 std::invalid_argument);
}

TEST(SerializeFrame_MaxBrowserID) {
    uint8_t bgra[4] = {};  // 1x1
    auto buf = protocol::SerializeFrame(255, 1, 1, bgra, sizeof(bgra));
    auto hdr = protocol::ParseFrameHeader(buf.data() + 4);
    ASSERT_EQ(hdr.browser_id, uint8_t(255));
}

TEST(SerializeFrame_BigEndianWidthHeight) {
    // Width 0x0400 = 1024, Height 0x0300 = 768.
    std::vector<uint8_t> bgra(1024 * 768 * 4, 0);
    auto buf =
        protocol::SerializeFrame(1, 1024, 768, bgra.data(), bgra.size());

    // Check big-endian encoding manually.
    ASSERT_EQ(buf[5], uint8_t(0x04));  // width high byte
    ASSERT_EQ(buf[6], uint8_t(0x00));  // width low byte
    ASSERT_EQ(buf[7], uint8_t(0x03));  // height high byte
    ASSERT_EQ(buf[8], uint8_t(0x00));  // height low byte
}

TEST(SerializeFrame_1x1Pixel) {
    // Minimal valid frame: 1x1.
    uint8_t bgra[4] = {0x10, 0x20, 0x30, 0x40};
    auto buf = protocol::SerializeFrame(0, 1, 1, bgra, sizeof(bgra));

    ASSERT_EQ(buf.size(), size_t(4 + 5 + 4));  // 13

    uint32_t total_len = protocol::ParseLengthPrefix(buf.data());
    ASSERT_EQ(total_len, uint32_t(9));  // 5 + 4

    // Pixel data.
    ASSERT_EQ(buf[9], uint8_t(0x10));
    ASSERT_EQ(buf[10], uint8_t(0x20));
    ASSERT_EQ(buf[11], uint8_t(0x30));
    ASSERT_EQ(buf[12], uint8_t(0x40));
}

// ---------------------------------------------------------------------------
// Parse Frame Header Tests
// ---------------------------------------------------------------------------

TEST(ParseFrameHeader_AllFields) {
    // browser=7, width=1920 (0x0780), height=1080 (0x0438).
    uint8_t data[5] = {7, 0x07, 0x80, 0x04, 0x38};
    auto hdr = protocol::ParseFrameHeader(data);
    ASSERT_EQ(hdr.browser_id, uint8_t(7));
    ASSERT_EQ(hdr.width, uint16_t(1920));
    ASSERT_EQ(hdr.height, uint16_t(1080));
}

TEST(ParseFrameHeader_ZeroDimensions) {
    uint8_t data[5] = {0, 0, 0, 0, 0};
    auto hdr = protocol::ParseFrameHeader(data);
    ASSERT_EQ(hdr.browser_id, uint8_t(0));
    ASSERT_EQ(hdr.width, uint16_t(0));
    ASSERT_EQ(hdr.height, uint16_t(0));
}

TEST(ParseFrameHeader_MaxValues) {
    // browser=255, width=65535, height=65535.
    uint8_t data[5] = {0xFF, 0xFF, 0xFF, 0xFF, 0xFF};
    auto hdr = protocol::ParseFrameHeader(data);
    ASSERT_EQ(hdr.browser_id, uint8_t(255));
    ASSERT_EQ(hdr.width, uint16_t(65535));
    ASSERT_EQ(hdr.height, uint16_t(65535));
}

// ---------------------------------------------------------------------------
// Parse Length Prefix Tests
// ---------------------------------------------------------------------------

TEST(ParseLengthPrefix_SmallValue) {
    // 0x00000015 = 21.
    uint8_t data[4] = {0x00, 0x00, 0x00, 0x15};
    ASSERT_EQ(protocol::ParseLengthPrefix(data), uint32_t(21));
}

TEST(ParseLengthPrefix_LargeValue) {
    // 0x007E9005 = 8294405.
    uint8_t data[4] = {0x00, 0x7E, 0x90, 0x05};
    ASSERT_EQ(protocol::ParseLengthPrefix(data), uint32_t(0x007E9005));
}

TEST(ParseLengthPrefix_MaxValue) {
    uint8_t data[4] = {0xFF, 0xFF, 0xFF, 0xFF};
    ASSERT_EQ(protocol::ParseLengthPrefix(data), uint32_t(0xFFFFFFFF));
}

TEST(ParseLengthPrefix_Zero) {
    uint8_t data[4] = {0x00, 0x00, 0x00, 0x00};
    ASSERT_EQ(protocol::ParseLengthPrefix(data), uint32_t(0));
}

// ---------------------------------------------------------------------------
// Control Response Builder Tests
// ---------------------------------------------------------------------------

TEST(MakeOkResponse_Format) {
    auto resp = protocol::MakeOkResponse(0);
    ASSERT_EQ(resp, std::string("{\"id\":0,\"status\":\"ok\"}\n"));
}

TEST(MakeOkResponse_NonZeroID) {
    auto resp = protocol::MakeOkResponse(3);
    ASSERT_EQ(resp, std::string("{\"id\":3,\"status\":\"ok\"}\n"));
}

TEST(MakeOkResponse_LargeID) {
    auto resp = protocol::MakeOkResponse(99999);
    ASSERT_EQ(resp, std::string("{\"id\":99999,\"status\":\"ok\"}\n"));
}

TEST(MakeErrorResponse_Format) {
    auto resp = protocol::MakeErrorResponse(1, "not found");
    ASSERT_EQ(
        resp,
        std::string(
            "{\"id\":1,\"status\":\"error\",\"error\":\"not found\"}\n"));
}

TEST(MakeEventResponse_WithExtraField) {
    auto resp =
        protocol::MakeEventResponse(0, "format_detected", "format", "ograf");
    ASSERT_EQ(resp,
              std::string("{\"id\":0,\"event\":\"format_detected\","
                          "\"format\":\"ograf\"}\n"));
}

TEST(MakeEventResponse_NoExtraField) {
    auto resp = protocol::MakeEventResponse(2, "stopped");
    ASSERT_EQ(resp, std::string("{\"id\":2,\"event\":\"stopped\"}\n"));
}

// ---------------------------------------------------------------------------
// Cross-Compatibility with Go Protocol (server/htmlgfx/protocol.go)
// ---------------------------------------------------------------------------

TEST(FrameFormat_MatchesGoProtocol) {
    // Verify the C++ serialization matches what Go ReadFrame expects:
    //   [4 bytes total_len BE][1 byte browser_id][2 bytes width BE]
    //   [2 bytes height BE][pixels]
    uint8_t bgra[32] = {};  // 4x2
    for (int i = 0; i < 32; i += 4) {
        bgra[i] = 0xFF;      // B
        bgra[i + 1] = 0x80;  // G
        bgra[i + 2] = 0x40;  // R
        bgra[i + 3] = 0xFF;  // A
    }

    auto buf = protocol::SerializeFrame(2, 4, 2, bgra, sizeof(bgra));

    // Go expects: total_length = header_size + pixel_size = 5 + 32 = 37.
    uint32_t total_len = protocol::ParseLengthPrefix(buf.data());
    ASSERT_EQ(total_len, uint32_t(37));

    // Message total size = 4 + 37 = 41.
    ASSERT_EQ(buf.size(), size_t(41));

    // Go reads browser_id at offset 4.
    ASSERT_EQ(buf[4], uint8_t(2));
    // Go reads width at offset 5-6 (big-endian).
    ASSERT_EQ(buf[5], uint8_t(0));
    ASSERT_EQ(buf[6], uint8_t(4));
    // Go reads height at offset 7-8 (big-endian).
    ASSERT_EQ(buf[7], uint8_t(0));
    ASSERT_EQ(buf[8], uint8_t(2));
    // Pixels start at offset 9.
    ASSERT_EQ(buf[9], uint8_t(0xFF));
}

TEST(FrameFormat_GoRoundTrip1080p) {
    // Verify a 1920x1080 frame has the correct wire format envelope.
    size_t pixel_size = 1920 * 1080 * 4;
    uint32_t expected_total_len =
        static_cast<uint32_t>(protocol::kFrameHeaderSize + pixel_size);

    std::vector<uint8_t> bgra(pixel_size, 0);
    auto buf =
        protocol::SerializeFrame(0, 1920, 1080, bgra.data(), bgra.size());

    uint32_t total_len = protocol::ParseLengthPrefix(buf.data());
    ASSERT_EQ(total_len, expected_total_len);

    auto hdr = protocol::ParseFrameHeader(buf.data() + 4);
    ASSERT_EQ(hdr.width, uint16_t(1920));
    ASSERT_EQ(hdr.height, uint16_t(1080));
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

TEST(FrameHeaderSize_Constant) {
    ASSERT_EQ(protocol::kFrameHeaderSize, size_t(5));
}

TEST(MaxFramePixels_Constant) {
    // 7680 * 4320 * 4 = 132,710,400.
    ASSERT_EQ(protocol::kMaxFramePixels, size_t(7680 * 4320 * 4));
}

TEST(MaxFramePixels_Fits1080p) {
    size_t frame_1080p = 1920 * 1080 * 4;
    if (frame_1080p >= protocol::kMaxFramePixels) {
        throw std::runtime_error("1080p frame exceeds max");
    }
}

TEST(MaxFramePixels_Fits4K) {
    size_t frame_4k = 3840 * 2160 * 4;
    if (frame_4k >= protocol::kMaxFramePixels) {
        throw std::runtime_error("4K frame exceeds max");
    }
}

// ---------------------------------------------------------------------------
// EscapeForJS Tests
// ---------------------------------------------------------------------------

TEST(EscapeForJS_NoEscape) {
    ASSERT_EQ(protocol::EscapeForJS("hello world"), std::string("hello world"));
}

TEST(EscapeForJS_SingleQuote) {
    ASSERT_EQ(protocol::EscapeForJS("O'Brien"), std::string("O\\'Brien"));
}

TEST(EscapeForJS_Backslash) {
    ASSERT_EQ(protocol::EscapeForJS("path\\to"), std::string("path\\\\to"));
}

TEST(EscapeForJS_Newline) {
    ASSERT_EQ(protocol::EscapeForJS("line1\nline2"), std::string("line1\\nline2"));
}

TEST(EscapeForJS_Tab) {
    ASSERT_EQ(protocol::EscapeForJS("a\tb"), std::string("a\\tb"));
}

TEST(EscapeForJS_CarriageReturn) {
    ASSERT_EQ(protocol::EscapeForJS("a\rb"), std::string("a\\rb"));
}

TEST(EscapeForJS_ScriptTag) {
    ASSERT_EQ(protocol::EscapeForJS("</script>"), std::string("<\\/script>"));
}

TEST(EscapeForJS_ComplexString) {
    // JSON with quotes: {"name":"O'Brien","title":"Sr. Engineer"}
    std::string input = R"({"name":"O'Brien","title":"Sr. Engineer"})";
    std::string escaped = protocol::EscapeForJS(input);
    // Single quotes escaped, double quotes pass through (they're safe in single-quoted JS strings)
    ASSERT_EQ(escaped, std::string(R"({"name":"O\'Brien","title":"Sr. Engineer"})"));
}

TEST(EscapeForJS_EmptyString) {
    ASSERT_EQ(protocol::EscapeForJS(""), std::string(""));
}

// ---------------------------------------------------------------------------
// EscapeForJSON Tests
// ---------------------------------------------------------------------------

TEST(EscapeForJSON_NoEscape) {
    ASSERT_EQ(protocol::EscapeForJSON("hello"), std::string("hello"));
}

TEST(EscapeForJSON_DoubleQuote) {
    ASSERT_EQ(protocol::EscapeForJSON("say \"hi\""), std::string("say \\\"hi\\\""));
}

TEST(EscapeForJSON_Backslash) {
    ASSERT_EQ(protocol::EscapeForJSON("a\\b"), std::string("a\\\\b"));
}

TEST(EscapeForJSON_Newline) {
    ASSERT_EQ(protocol::EscapeForJSON("a\nb"), std::string("a\\nb"));
}

TEST(EscapeForJSON_MakeErrorResponseEscapes) {
    auto resp = protocol::MakeErrorResponse(1, "can't load \"foo\"");
    // The error message should have double quotes escaped
    ASSERT_EQ(resp, std::string("{\"id\":1,\"status\":\"error\",\"error\":\"can't load \\\"foo\\\"\"}\n"));
}

// ---------------------------------------------------------------------------
// Entry Point
// ---------------------------------------------------------------------------

int main() {
    std::cout << "Running CEF sidecar protocol tests:" << std::endl;

    int run = 0;
    int passed = 0;

    for (const auto& tc : test_registry()) {
        run++;
        std::cout << "  " << tc.name << "... ";
        try {
            tc.fn();
            passed++;
            std::cout << "PASS" << std::endl;
        } catch (const std::exception& e) {
            std::cout << "FAIL: " << e.what() << std::endl;
        }
    }

    std::cout << std::endl;
    std::cout << passed << "/" << run << " tests passed." << std::endl;
    return (passed == run) ? 0 : 1;
}
