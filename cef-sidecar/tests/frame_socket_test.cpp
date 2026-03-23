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
// Helper: create a socket pair via temporary Unix domain socket
// ---------------------------------------------------------------------------

static std::string tmp_socket_path() {
    static int counter = 0;
    return "/tmp/sf_frame_test_" + std::to_string(getpid()) + "_" +
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

// Read exactly n bytes from fd.
static bool read_exact(int fd, void* buf, size_t n) {
    uint8_t* ptr = static_cast<uint8_t*>(buf);
    size_t remaining = n;
    while (remaining > 0) {
        ssize_t got = read(fd, ptr, remaining);
        if (got <= 0) return false;
        ptr += got;
        remaining -= static_cast<size_t>(got);
    }
    return true;
}

// ---------------------------------------------------------------------------
// Frame Socket Tests (socket-level, no CEF)
//
// These test the wire format by writing serialized frames to a socket
// and verifying the receiver can parse them with the protocol module.
// ---------------------------------------------------------------------------

TEST(FrameWire_WriteAndReadSmallFrame) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    // 2x2 BGRA frame (16 bytes).
    uint8_t bgra[16] = {};
    bgra[0] = 0xFF;  // B of pixel 0
    bgra[3] = 0xFF;  // A of pixel 0

    auto serialized =
        protocol::SerializeFrame(1, 2, 2, bgra, sizeof(bgra));

    std::vector<uint8_t> received;

    std::thread reader([&]() {
        int client_fd = connect_to(path);
        ASSERT_TRUE(client_fd >= 0);

        // Read the length prefix first.
        uint8_t len_buf[4];
        ASSERT_TRUE(read_exact(client_fd, len_buf, 4));
        uint32_t total_len = protocol::ParseLengthPrefix(len_buf);

        // Read the rest of the message.
        received.resize(total_len);
        ASSERT_TRUE(read_exact(client_fd, received.data(), total_len));

        close(client_fd);
    });

    int conn_fd = accept(listen_fd, nullptr, nullptr);
    ASSERT_TRUE(conn_fd >= 0);

    // Write the serialized frame.
    ssize_t written =
        write(conn_fd, serialized.data(), serialized.size());
    ASSERT_TRUE(written == static_cast<ssize_t>(serialized.size()));

    close(conn_fd);
    reader.join();

    // Parse header from received data.
    auto hdr = protocol::ParseFrameHeader(received.data());
    ASSERT_EQ(hdr.browser_id, uint8_t(1));
    ASSERT_EQ(hdr.width, uint16_t(2));
    ASSERT_EQ(hdr.height, uint16_t(2));

    // Verify pixel data.
    ASSERT_EQ(received[5], uint8_t(0xFF));  // B of pixel 0
    ASSERT_EQ(received[8], uint8_t(0xFF));  // A of pixel 0

    close(listen_fd);
    unlink(path.c_str());
}

TEST(FrameWire_MultipleFrames) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    // Two frames: 1x1 each.
    uint8_t px1[4] = {0xAA, 0xBB, 0xCC, 0xDD};
    uint8_t px2[4] = {0x11, 0x22, 0x33, 0x44};

    auto frame1 = protocol::SerializeFrame(0, 1, 1, px1, 4);
    auto frame2 = protocol::SerializeFrame(1, 1, 1, px2, 4);

    std::vector<std::vector<uint8_t>> frames_received;

    std::thread reader([&]() {
        int client_fd = connect_to(path);
        ASSERT_TRUE(client_fd >= 0);

        for (int i = 0; i < 2; i++) {
            uint8_t len_buf[4];
            if (!read_exact(client_fd, len_buf, 4)) break;
            uint32_t total_len = protocol::ParseLengthPrefix(len_buf);

            std::vector<uint8_t> msg(total_len);
            if (!read_exact(client_fd, msg.data(), total_len)) break;
            frames_received.push_back(std::move(msg));
        }

        close(client_fd);
    });

    int conn_fd = accept(listen_fd, nullptr, nullptr);
    ASSERT_TRUE(conn_fd >= 0);

    write(conn_fd, frame1.data(), frame1.size());
    write(conn_fd, frame2.data(), frame2.size());

    close(conn_fd);
    reader.join();

    ASSERT_EQ(frames_received.size(), size_t(2));

    // Verify frame 1.
    auto hdr1 = protocol::ParseFrameHeader(frames_received[0].data());
    ASSERT_EQ(hdr1.browser_id, uint8_t(0));
    ASSERT_EQ(frames_received[0][5], uint8_t(0xAA));

    // Verify frame 2.
    auto hdr2 = protocol::ParseFrameHeader(frames_received[1].data());
    ASSERT_EQ(hdr2.browser_id, uint8_t(1));
    ASSERT_EQ(frames_received[1][5], uint8_t(0x11));

    close(listen_fd);
    unlink(path.c_str());
}

TEST(FrameWire_LargerFrame) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    // 64x48 frame.
    size_t pixel_size = 64 * 48 * 4;
    std::vector<uint8_t> bgra(pixel_size, 0x42);

    auto serialized =
        protocol::SerializeFrame(2, 64, 48, bgra.data(), bgra.size());

    std::vector<uint8_t> received;

    std::thread reader([&]() {
        int client_fd = connect_to(path);
        ASSERT_TRUE(client_fd >= 0);

        uint8_t len_buf[4];
        ASSERT_TRUE(read_exact(client_fd, len_buf, 4));
        uint32_t total_len = protocol::ParseLengthPrefix(len_buf);

        received.resize(total_len);
        ASSERT_TRUE(read_exact(client_fd, received.data(), total_len));

        close(client_fd);
    });

    int conn_fd = accept(listen_fd, nullptr, nullptr);
    ASSERT_TRUE(conn_fd >= 0);

    // Write in chunks to test partial write handling.
    size_t offset = 0;
    while (offset < serialized.size()) {
        size_t chunk = std::min(size_t(1024), serialized.size() - offset);
        ssize_t n = write(conn_fd, serialized.data() + offset, chunk);
        ASSERT_TRUE(n > 0);
        offset += static_cast<size_t>(n);
    }

    close(conn_fd);
    reader.join();

    auto hdr = protocol::ParseFrameHeader(received.data());
    ASSERT_EQ(hdr.browser_id, uint8_t(2));
    ASSERT_EQ(hdr.width, uint16_t(64));
    ASSERT_EQ(hdr.height, uint16_t(48));

    // Spot-check some pixel data.
    ASSERT_EQ(received[protocol::kFrameHeaderSize], uint8_t(0x42));
    ASSERT_EQ(received[received.size() - 1], uint8_t(0x42));

    close(listen_fd);
    unlink(path.c_str());
}

TEST(FrameWire_SerializationMatchesExpectedFormat) {
    // Verify the exact wire format matches what Go's ReadFrame expects.
    uint8_t bgra[4] = {0xDE, 0xAD, 0xBE, 0xEF};
    auto buf = protocol::SerializeFrame(3, 1, 1, bgra, 4);

    // Total message: 4 (len prefix) + 5 (header) + 4 (pixels) = 13 bytes.
    ASSERT_EQ(buf.size(), size_t(13));

    // Length prefix: 9 = 5 (header) + 4 (pixels).
    ASSERT_EQ(buf[0], uint8_t(0));
    ASSERT_EQ(buf[1], uint8_t(0));
    ASSERT_EQ(buf[2], uint8_t(0));
    ASSERT_EQ(buf[3], uint8_t(9));

    // Header: browser_id=3, width=1 (0x0001), height=1 (0x0001).
    ASSERT_EQ(buf[4], uint8_t(3));
    ASSERT_EQ(buf[5], uint8_t(0));
    ASSERT_EQ(buf[6], uint8_t(1));
    ASSERT_EQ(buf[7], uint8_t(0));
    ASSERT_EQ(buf[8], uint8_t(1));

    // Pixels: BGRA = 0xDE 0xAD 0xBE 0xEF.
    ASSERT_EQ(buf[9], uint8_t(0xDE));
    ASSERT_EQ(buf[10], uint8_t(0xAD));
    ASSERT_EQ(buf[11], uint8_t(0xBE));
    ASSERT_EQ(buf[12], uint8_t(0xEF));
}

// ---------------------------------------------------------------------------
// Entry Point
// ---------------------------------------------------------------------------

int main() {
    std::cout << "Running frame socket tests:" << std::endl;

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
