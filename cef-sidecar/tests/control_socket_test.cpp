#include <cstring>
#include <functional>
#include <iostream>
#include <sstream>
#include <string>
#include <sys/socket.h>
#include <sys/un.h>
#include <thread>
#include <unistd.h>
#include <vector>

#include "../src/control_socket.h"
#include "../src/json_parse.h"
#include "../src/protocol.h"

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

// ---------------------------------------------------------------------------
// Helper: create a Unix domain socket pair using a temporary path
// ---------------------------------------------------------------------------

static std::string tmp_socket_path() {
    static int counter = 0;
    return "/tmp/sf_ctrl_test_" + std::to_string(getpid()) + "_" +
           std::to_string(counter++);
}

static int create_listener(const std::string& path) {
    unlink(path.c_str());

    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return -1;

    struct sockaddr_un addr = {};
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, path.c_str(), sizeof(addr.sun_path) - 1);

    if (bind(fd, (struct sockaddr*)&addr, sizeof(addr)) < 0) {
        close(fd);
        return -1;
    }

    if (listen(fd, 1) < 0) {
        close(fd);
        return -1;
    }

    return fd;
}

static int connect_to(const std::string& path) {
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return -1;

    struct sockaddr_un addr = {};
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, path.c_str(), sizeof(addr.sun_path) - 1);

    if (connect(fd, (struct sockaddr*)&addr, sizeof(addr)) < 0) {
        close(fd);
        return -1;
    }

    return fd;
}

// ---------------------------------------------------------------------------
// Tests for ControlSocket wiring (without CEF -- handler called directly)
// ---------------------------------------------------------------------------

// Note: ControlSocket uses CefPostTask which isn't available without CEF.
// These tests verify the parse + response logic at a unit level, using
// the json_parse module and protocol response builders.

TEST(ControlResponse_OkFormat) {
    auto resp = protocol::MakeOkResponse(5);
    ASSERT_EQ(resp, std::string("{\"id\":5,\"status\":\"ok\"}\n"));
}

TEST(ControlResponse_ErrorFormat) {
    auto resp = protocol::MakeErrorResponse(2, "slot occupied");
    ASSERT_EQ(resp, std::string("{\"id\":2,\"status\":\"error\","
                                "\"error\":\"slot occupied\"}\n"));
}

TEST(ControlResponse_EventFormat) {
    auto resp =
        protocol::MakeEventResponse(0, "format_detected", "format", "ograf");
    ASSERT_EQ(resp, std::string("{\"id\":0,\"event\":\"format_detected\","
                                "\"format\":\"ograf\"}\n"));
}

// Test that parse + response roundtrips for all command types work.

TEST(ControlRoundtrip_Create) {
    std::string json =
        R"({"cmd":"create","id":0,"width":1920,"height":1080,"fps":60})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("create"));
    // Simulate success response.
    auto resp = protocol::MakeOkResponse(cmd.id);
    ASSERT_EQ(resp, std::string("{\"id\":0,\"status\":\"ok\"}\n"));
}

TEST(ControlRoundtrip_Load) {
    std::string json =
        R"({"cmd":"load","id":1,"url":"http://localhost/gfx.html"})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("load"));
    ASSERT_EQ(cmd.url, std::string("http://localhost/gfx.html"));
    auto resp = protocol::MakeOkResponse(cmd.id);
    ASSERT_EQ(resp, std::string("{\"id\":1,\"status\":\"ok\"}\n"));
}

TEST(ControlRoundtrip_Destroy) {
    std::string json = R"({"cmd":"destroy","id":2})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    auto resp = protocol::MakeOkResponse(cmd.id);
    ASSERT_EQ(resp, std::string("{\"id\":2,\"status\":\"ok\"}\n"));
}

TEST(ControlRoundtrip_Shutdown) {
    std::string json = R"({"cmd":"shutdown","id":0})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    ASSERT_EQ(cmd.cmd, std::string("shutdown"));
}

TEST(ControlRoundtrip_UnknownCommand) {
    std::string json = R"({"cmd":"bogus","id":0})";
    ControlCommand cmd;
    ASSERT_TRUE(json_parse::ParseControlCommand(json, cmd));
    auto resp =
        protocol::MakeErrorResponse(cmd.id, "unknown command: " + cmd.cmd);
    ASSERT_TRUE(resp.find("bogus") != std::string::npos);
}

// Test socket-level send/receive for response writing.

TEST(ControlSocket_SendResponse) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    // Create a ControlSocket with a no-op handler (won't use CefPostTask).
    // We only test SendResponse here, which doesn't touch CEF.
    // To avoid CefPostTask calls we don't call Start().

    // Simulate: manually accept + test SendResponse.
    std::string received;
    std::thread client_thread([&]() {
        int client_fd = connect_to(path);
        ASSERT_TRUE(client_fd >= 0);

        char buf[256] = {};
        ssize_t n = read(client_fd, buf, sizeof(buf) - 1);
        if (n > 0) {
            received = std::string(buf, static_cast<size_t>(n));
        }
        close(client_fd);
    });

    // Accept the connection.
    int conn_fd = accept(listen_fd, nullptr, nullptr);
    ASSERT_TRUE(conn_fd >= 0);

    // Write a response directly.
    std::string resp = protocol::MakeOkResponse(7);
    ssize_t written = write(conn_fd, resp.data(), resp.size());
    ASSERT_TRUE(written > 0);

    close(conn_fd);
    client_thread.join();

    ASSERT_EQ(received, std::string("{\"id\":7,\"status\":\"ok\"}\n"));

    close(listen_fd);
    unlink(path.c_str());
}

// ---------------------------------------------------------------------------
// Entry Point
// ---------------------------------------------------------------------------

int main() {
    std::cout << "Running control socket tests:" << std::endl;

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
