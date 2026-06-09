#include <functional>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

#include "../src/control_socket.h"
#include "../src/json_parse.h"

// ---------------------------------------------------------------------------
// Minimal test framework
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

#define ASSERT_TRUE(expr)                                                \
    do {                                                                 \
        if (!(expr))                                                     \
            throw std::runtime_error("Assertion failed: " #expr         \
                                     " at line " +                       \
                                     std::to_string(__LINE__));          \
    } while (0)

#define ASSERT_FALSE(expr)                                               \
    do {                                                                 \
        if (expr)                                                        \
            throw std::runtime_error("Assertion failed: NOT " #expr     \
                                     " at line " +                       \
                                     std::to_string(__LINE__));          \
    } while (0)

// ---------------------------------------------------------------------------
// FindString Tests
// ---------------------------------------------------------------------------

TEST(FindString_SimpleValue) {
    std::string json = R"({"cmd":"create","url":"http://example.com"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "cmd"),
              std::string("create"));
}

TEST(FindString_URLValue) {
    std::string json = R"({"cmd":"load","url":"http://example.com/page"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "url"),
              std::string("http://example.com/page"));
}

TEST(FindString_MissingKey) {
    std::string json = R"({"cmd":"create"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "missing"), std::string(""));
}

TEST(FindString_EmptyValue) {
    std::string json = R"({"cmd":""})";
    ASSERT_EQ(json_parse::detail::FindString(json, "cmd"), std::string(""));
}

TEST(FindString_EscapedQuote) {
    std::string json = R"({"data":"hello\"world"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("hello\"world"));
}

// The Go peer builds messages with encoding/json, which HTML-escapes by
// default: '&' -> \u0026, '<' -> \u003c, '>' -> \u003e. URLs with query
// parameters must round-trip. (Raw string literals below: the backslash
// escapes are part of the JSON text, not C++ escapes.)
TEST(FindString_UnicodeEscapeAmpersandInURL) {
    std::string json = R"({"url":"http://h/p?a=1\u0026b=2"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "url"),
              std::string("http://h/p?a=1&b=2"));
}

TEST(FindString_UnicodeEscapeAngleBrackets) {
    std::string json = R"({"data":"\u003cb\u003ehi\u003c/b\u003e"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("<b>hi</b>"));
}

TEST(FindString_ControlEscapes) {
    std::string json = R"({"data":"line1\nline2\ttabbed\r\b\f"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("line1\nline2\ttabbed\r\b\f"));
}

TEST(FindString_EscapedSlash) {
    std::string json = R"({"url":"http:\/\/example.com\/page"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "url"),
              std::string("http://example.com/page"));
}

TEST(FindString_UnicodeEscapeTwoByteUTF8) {
    // U+00E9 (e with acute) -> 0xC3 0xA9 in UTF-8.
    std::string json = R"({"data":"caf\u00e9"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("caf\xc3\xa9"));
}

TEST(FindString_UnicodeEscapeThreeByteUTF8) {
    // U+20AC (euro sign) -> 0xE2 0x82 0xAC in UTF-8.
    std::string json = R"({"data":"price: \u20ac5"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("price: \xe2\x82\xac" "5"));
}

TEST(FindString_UnicodeEscapeSurrogatePair) {
    // U+1F600 (grinning face) as a UTF-16 surrogate pair -> 4-byte UTF-8.
    std::string json = R"({"data":"\ud83d\ude00"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("\xf0\x9f\x98\x80"));
}

TEST(FindString_UnicodeEscapeUppercaseHex) {
    std::string json = R"({"data":"\u003C\u003E"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("<>"));
}

TEST(FindString_MalformedUnicodeEscapeIsLenient) {
    // Bad hex digits: keep the raw text rather than corrupting/failing, and
    // keep scanning so the closing quote is still found.
    std::string json = R"({"data":"a\uZZZZb","cmd":"x"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("a\\uZZZZb"));
    ASSERT_EQ(json_parse::detail::FindString(json, "cmd"), std::string("x"));
}

TEST(FindString_TruncatedUnicodeEscapeIsLenient) {
    std::string json = "{\"data\":\"a\\u00";
    // No closing quote: ParseStringAt returns what it accumulated.
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("a\\u00"));
}

TEST(FindString_LoneSurrogateEmitsReplacementChar) {
    // A high surrogate not followed by a low surrogate cannot be encoded;
    // emit U+FFFD (replacement character) like Go's encoding/json does.
    std::string json = R"({"data":"x\ud83dy"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "data"),
              std::string("x\xef\xbf\xbdy"));
}

TEST(ParseCommand_LoadURLWithQueryParams) {
    // End-to-end: Go's WriteControlCommand (json.Marshal) emits \u0026 for
    // '&' in a load URL. The parsed URL must contain the real ampersand.
    std::string json =
        R"({"cmd":"load","id":1,"url":"http://localhost:8080/g.html?a=1\u0026b=2"})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.url,
              std::string("http://localhost:8080/g.html?a=1&b=2"));
}

TEST(ParseCommand_UpdateDataWithHTMLAndNewlines) {
    std::string json =
        R"({"cmd":"update","id":0,"data":"{\"html\":\"\u003cb\u003eBreaking\u003c/b\u003e \u0026 more\\nnews\"}"})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.data,
              // The escaped-backslash + n in the wire text decodes to a literal
              // backslash-n inside the nested JSON document, as Go would emit
              // for a marshaled inner JSON string.
              std::string("{\"html\":\"<b>Breaking</b> & more\\nnews\"}"));
}

TEST(FindString_MultipleFields) {
    std::string json = R"({"cmd":"custom","action":"fadeIn","payload":"test"})";
    ASSERT_EQ(json_parse::detail::FindString(json, "action"),
              std::string("fadeIn"));
    ASSERT_EQ(json_parse::detail::FindString(json, "payload"),
              std::string("test"));
}

// ---------------------------------------------------------------------------
// FindInt Tests
// ---------------------------------------------------------------------------

TEST(FindInt_SimpleValue) {
    std::string json = R"({"id":42})";
    ASSERT_EQ(json_parse::detail::FindInt(json, "id"), 42);
}

TEST(FindInt_Zero) {
    std::string json = R"({"id":0})";
    ASSERT_EQ(json_parse::detail::FindInt(json, "id"), 0);
}

TEST(FindInt_MissingKey) {
    std::string json = R"({"cmd":"create"})";
    ASSERT_EQ(json_parse::detail::FindInt(json, "id"), 0);
}

TEST(FindInt_NegativeValue) {
    std::string json = R"({"id":-1})";
    ASSERT_EQ(json_parse::detail::FindInt(json, "id"), -1);
}

TEST(FindInt_WithWhitespace) {
    std::string json = R"({"id": 7})";
    ASSERT_EQ(json_parse::detail::FindInt(json, "id"), 7);
}

TEST(FindInt_MultipleInts) {
    std::string json = R"({"id":3,"width":1920,"height":1080,"fps":60})";
    ASSERT_EQ(json_parse::detail::FindInt(json, "id"), 3);
    ASSERT_EQ(json_parse::detail::FindInt(json, "width"), 1920);
    ASSERT_EQ(json_parse::detail::FindInt(json, "height"), 1080);
    ASSERT_EQ(json_parse::detail::FindInt(json, "fps"), 60);
}

TEST(FindInt_IntFollowedByComma) {
    std::string json = R"({"id":5,"cmd":"test"})";
    ASSERT_EQ(json_parse::detail::FindInt(json, "id"), 5);
}

TEST(FindInt_IntFollowedByBrace) {
    std::string json = R"({"id":5})";
    ASSERT_EQ(json_parse::detail::FindInt(json, "id"), 5);
}

// ---------------------------------------------------------------------------
// ParseControlCommand Tests
// ---------------------------------------------------------------------------

TEST(ParseCommand_Create) {
    std::string json =
        R"({"cmd":"create","id":0,"width":1920,"height":1080,"fps":60})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("create"));
    ASSERT_EQ(cmd.id, 0);
    ASSERT_EQ(cmd.width, 1920);
    ASSERT_EQ(cmd.height, 1080);
    ASSERT_EQ(cmd.fps, 60);
}

TEST(ParseCommand_Load) {
    std::string json =
        R"({"cmd":"load","id":1,"url":"http://localhost:8080/graphic.html"})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("load"));
    ASSERT_EQ(cmd.id, 1);
    ASSERT_EQ(cmd.url,
              std::string("http://localhost:8080/graphic.html"));
}

TEST(ParseCommand_Play) {
    std::string json = R"({"cmd":"play","id":2})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("play"));
    ASSERT_EQ(cmd.id, 2);
}

TEST(ParseCommand_Update) {
    std::string json = R"({"cmd":"update","id":0,"data":"some data here"})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("update"));
    ASSERT_EQ(cmd.data, std::string("some data here"));
}

TEST(ParseCommand_Custom) {
    std::string json =
        R"({"cmd":"custom","id":1,"action":"fadeIn","payload":"test"})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("custom"));
    ASSERT_EQ(cmd.action, std::string("fadeIn"));
    ASSERT_EQ(cmd.payload, std::string("test"));
}

TEST(ParseCommand_Destroy) {
    std::string json = R"({"cmd":"destroy","id":3})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("destroy"));
    ASSERT_EQ(cmd.id, 3);
}

TEST(ParseCommand_Shutdown) {
    std::string json = R"({"cmd":"shutdown","id":0})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("shutdown"));
}

TEST(ParseCommand_EmptyLine) {
    ControlCommand cmd;
    ASSERT_FALSE(json_parse::ParseControlCommand("", cmd));
}

TEST(ParseCommand_Garbage) {
    ControlCommand cmd;
    ASSERT_FALSE(json_parse::ParseControlCommand("not json at all", cmd));
}

TEST(ParseCommand_MissingCmd) {
    std::string json = R"({"id":0,"width":100})";
    ControlCommand cmd;
    ASSERT_FALSE(json_parse::ParseControlCommand(json, cmd));
}

TEST(ParseCommand_DefaultValues) {
    // Only cmd is present -- other fields should be defaults.
    std::string json = R"({"cmd":"stop"})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.id, 0);
    ASSERT_EQ(cmd.width, 0);
    ASSERT_EQ(cmd.height, 0);
    ASSERT_EQ(cmd.fps, 0);
    ASSERT_EQ(cmd.url, std::string(""));
    ASSERT_EQ(cmd.data, std::string(""));
    ASSERT_EQ(cmd.action, std::string(""));
    ASSERT_EQ(cmd.payload, std::string(""));
}

// ---------------------------------------------------------------------------
// Entry Point
// ---------------------------------------------------------------------------

int main() {
    std::cout << "Running JSON parse tests:" << std::endl;

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
