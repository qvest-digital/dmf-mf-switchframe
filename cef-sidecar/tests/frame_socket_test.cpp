#include <atomic>
#include <chrono>
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

#include "../src/frame_socket.h"
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

// Read one length-prefixed frame message (header + pixels) from fd.
static bool read_frame_msg(int fd, std::vector<uint8_t>& out) {
    uint8_t len_buf[4];
    if (!read_exact(fd, len_buf, 4)) return false;
    uint32_t total_len = protocol::ParseLengthPrefix(len_buf);
    out.resize(total_len);
    return read_exact(fd, out.data(), total_len);
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
// FrameSocket class tests (exercise the live writer-thread path)
//
// These instantiate the real FrameSocket so we cover SendFrame -> background
// writer -> socket, including the non-blocking-under-backpressure and
// per-browser coalescing behavior that decouples the CEF UI thread from I/O.
// ---------------------------------------------------------------------------

TEST(FrameSocket_DeliversFrameThroughWriterThread) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    FrameSocket fs(listen_fd);

    // Accept runs in its own thread (blocks until the client connects).
    std::thread accept_thread([&]() { ASSERT_TRUE(fs.Accept()); });

    int client_fd = connect_to(path);
    ASSERT_TRUE(client_fd >= 0);
    accept_thread.join();

    // 2x2 BGRA frame.
    uint8_t bgra[16] = {};
    bgra[0] = 0x7E;  // B of pixel 0
    bgra[3] = 0xFF;  // A of pixel 0
    ASSERT_TRUE(fs.SendFrame(1, 2, 2, bgra, sizeof(bgra)));

    std::vector<uint8_t> msg;
    ASSERT_TRUE(read_frame_msg(client_fd, msg));

    auto hdr = protocol::ParseFrameHeader(msg.data());
    ASSERT_EQ(hdr.browser_id, uint8_t(1));
    ASSERT_EQ(hdr.width, uint16_t(2));
    ASSERT_EQ(hdr.height, uint16_t(2));
    ASSERT_EQ(msg[protocol::kFrameHeaderSize], uint8_t(0x7E));  // B of pixel 0

    fs.Close();
    close(client_fd);
    close(listen_fd);
    unlink(path.c_str());
}

// Regression test for the freeze bug: OnPaint -> SendFrame must not block the
// caller (the CEF UI thread) when the reader stops draining and the socket
// send buffer fills. Before the fix, SendFrame did a blocking write() loop and
// would hang here; with the writer-thread hand-off it returns promptly.
TEST(FrameSocket_SendFrameDoesNotBlockUnderBackpressure) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    FrameSocket fs(listen_fd);
    std::thread accept_thread([&]() { ASSERT_TRUE(fs.Accept()); });

    int client_fd = connect_to(path);
    ASSERT_TRUE(client_fd >= 0);
    accept_thread.join();

    // The client never reads, so the socket send buffer fills quickly.
    // Each frame is ~4MB (1024x1024 BGRA); a handful overflows any send buffer.
    const int kFrames = 16;
    const int kW = 1024, kH = 1024;
    std::vector<uint8_t> bgra(static_cast<size_t>(kW) * kH * 4, 0x33);

    std::atomic<bool> burst_done{false};

    std::thread sender([&]() {
        for (int i = 0; i < kFrames; i++) {
            // Vary browser_id so we also keep multiple per-browser slots.
            ASSERT_TRUE(fs.SendFrame(i % 4, kW, kH, bgra.data(), bgra.size()));
        }
        burst_done.store(true);
    });

    // Watchdog: the entire burst of SendFrame calls must complete quickly
    // because none of them may block on the full socket. Poll for up to 5s.
    auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5);
    while (!burst_done.load() &&
           std::chrono::steady_clock::now() < deadline) {
        std::this_thread::sleep_for(std::chrono::milliseconds(5));
    }
    bool finished = burst_done.load();

    // Drain the reader so the writer thread can make progress and the sender
    // (already finished if the fix is in place) can be joined cleanly.
    std::thread drainer([&]() {
        uint8_t buf[65536];
        while (read(client_fd, buf, sizeof(buf)) > 0) {
        }
    });

    sender.join();
    ASSERT_TRUE(finished);  // would be false (hung) before the fix

    // Tear down: closing the socket unblocks the drainer's read().
    fs.Close();
    close(client_fd);
    drainer.join();
    close(listen_fd);
    unlink(path.c_str());
}

// While the reader is stalled, newer frames for the same browser must replace
// older queued ones (single-slot-per-browser coalescing), so the reader sees
// the latest frame rather than a backlog of stale ones.
TEST(FrameSocket_CoalescesStaleFramesPerBrowser) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    FrameSocket fs(listen_fd);
    std::thread accept_thread([&]() { ASSERT_TRUE(fs.Accept()); });

    int client_fd = connect_to(path);
    ASSERT_TRUE(client_fd >= 0);
    accept_thread.join();

    // Make the socket non-blocking on the reader side so we can detect "no
    // more frames" without hanging.
    // (We instead just count frames received within a bounded read window.)

    // Queue many frames for browser 0 in a tight loop while nothing is reading.
    // Each frame carries a distinct marker byte so we can identify which
    // survived. The writer thread will deliver at most a small number; the key
    // property is that the LAST queued frame's marker is delivered.
    const int kW = 1, kH = 1;
    const int kFrames = 200;
    uint8_t last_marker = 0;
    for (int i = 0; i < kFrames; i++) {
        uint8_t px[4] = {static_cast<uint8_t>(i & 0xFF), 0x00, 0x00, 0xFF};
        last_marker = px[0];
        ASSERT_TRUE(fs.SendFrame(0, kW, kH, px, sizeof(px)));
    }

    // Now drain everything the writer manages to deliver, with a bounded
    // window, and remember the last frame's marker byte (offset
    // kFrameHeaderSize in the message body = first pixel's B channel).
    std::vector<uint8_t> msg;
    int received = 0;
    uint8_t seen_last = 0;
    // Read until the writer drains the single outstanding slot. Give it a few
    // attempts; with coalescing only a few distinct frames arrive.
    auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(3);
    // Mark the end of the live stream by closing the writer after a short
    // grace period from a helper thread, which makes read_frame_msg return
    // false once the buffered data is consumed.
    std::thread closer([&]() {
        std::this_thread::sleep_for(std::chrono::milliseconds(300));
        fs.Close();
    });
    while (std::chrono::steady_clock::now() < deadline) {
        if (!read_frame_msg(client_fd, msg)) break;
        received++;
        seen_last = msg[protocol::kFrameHeaderSize];
    }
    closer.join();

    // We must have received at least one frame, strictly fewer than we queued
    // (proving coalescing dropped stale frames), and the most recent frame we
    // saw must be the last one we queued (latest-wins).
    ASSERT_TRUE(received >= 1);
    ASSERT_TRUE(received < kFrames);
    ASSERT_EQ(seen_last, last_marker);

    close(client_fd);
    close(listen_fd);
    unlink(path.c_str());
}

// Regression test for the Accept/Close shutdown race: main.cpp runs Accept()
// on a background thread and calls Close() from the main thread while Accept
// may still be parked in accept(). If a connection arrives as (or after)
// Close runs, Accept must NOT re-arm stop_ = false and start a writer thread
// that Close has already joined-past — that resurrects the socket after
// shutdown and races the writer_ std::thread object itself.
//
// Deterministic interleaving: the client connection is queued in the listener
// backlog BEFORE Close() runs, so the subsequent Accept() call returns a real
// connection even though Close has fully completed.
TEST(FrameSocket_AcceptAfterCloseDoesNotResurrect) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    FrameSocket fs(listen_fd);

    // Connection pending in the backlog; accept() will succeed immediately.
    int client_fd = connect_to(path);
    ASSERT_TRUE(client_fd >= 0);

    fs.Close();

    // Accept must observe the completed Close and refuse to start the writer
    // or clear the stop flag. Before the fix this returned true.
    ASSERT_TRUE(!fs.Accept());

    // And the socket must not consider itself connected afterwards.
    uint8_t px[4] = {0, 0, 0, 0xFF};
    ASSERT_TRUE(!fs.SendFrame(0, 1, 1, px, sizeof(px)));

    close(client_fd);
    close(listen_fd);
    unlink(path.c_str());
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
