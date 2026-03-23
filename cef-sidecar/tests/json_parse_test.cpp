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
