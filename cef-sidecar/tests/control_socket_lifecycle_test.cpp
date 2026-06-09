// Lifecycle/threading tests for the real ControlSocket implementation.
//
// Unlike control_socket_test.cpp (which only exercises parse/response logic),
// this binary compiles src/control_socket.cpp against the CEF stubs in
// tests/cef_stubs and is built with ThreadSanitizer (see Makefile). It
// reproduces the Stop()-vs-readLoop() shutdown race: Stop() must not close
// conn_fd_ (or write -1 to it) while the reader thread may still be blocked
// in read() on it, and all cross-thread accesses to conn_fd_ must be
// synchronized. With the pre-fix code, TSan flags the unsynchronized
// conn_fd_ accesses and the close-before-join fd-reuse hazard.

#include <atomic>
#include <chrono>
#include <csignal>
#include <cstring>
#include <functional>
#include <iostream>
#include <pthread.h>
#include <sstream>
#include <string>
#include <sys/socket.h>
#include <sys/un.h>
#include <thread>
#include <unistd.h>
#include <vector>

#include "../src/control_socket.h"

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

#define ASSERT_TRUE(expr)                                                \
    do {                                                                 \
        if (!(expr))                                                     \
            throw std::runtime_error("Assertion failed: " #expr         \
                                     " at line " +                       \
                                     std::to_string(__LINE__));          \
    } while (0)

// ---------------------------------------------------------------------------
// Socket helpers
// ---------------------------------------------------------------------------

static std::string tmp_socket_path() {
    static int counter = 0;
    return "/tmp/sf_ctrl_life_test_" + std::to_string(getpid()) + "_" +
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
// Tests
// ---------------------------------------------------------------------------

// Stop() while the reader thread is actively read()ing commands. Before the
// fix, Stop() closed conn_fd_ and wrote -1 to it without any synchronization
// against the reader's concurrent read of conn_fd_ — a data race (UB) that
// TSan reports — and the close-before-join meant the parked read() could land
// on a recycled fd number. The commands keep flowing during Stop so the
// reader is mid-loop, not parked, maximizing the racing window.
TEST(ControlSocket_StopWhileReaderActive) {
    for (int iter = 0; iter < 20; iter++) {
        std::string path = tmp_socket_path();
        int listen_fd = create_listener(path);
        ASSERT_TRUE(listen_fd >= 0);

        std::atomic<int> commands{0};
        ControlSocket cs(listen_fd,
                         [&](const ControlCommand&) { commands++; });
        cs.Start();

        int client_fd = connect_to(path);
        ASSERT_TRUE(client_fd >= 0);

        // Pump commands so the reader thread is busy in its read loop.
        std::atomic<bool> pump_stop{false};
        std::thread pump([&]() {
            const std::string line = "{\"cmd\":\"play\",\"id\":1}\n";
            while (!pump_stop.load()) {
                if (write(client_fd, line.data(), line.size()) <= 0) {
                    break;
                }
            }
        });

        // Give the reader a moment to start consuming, then Stop()
        // concurrently with the in-flight reads.
        while (commands.load() == 0) {
            std::this_thread::sleep_for(std::chrono::microseconds(50));
        }
        cs.Stop();

        pump_stop.store(true);
        // Unblock a pump writer parked on a full send buffer.
        shutdown(client_fd, SHUT_RDWR);
        pump.join();

        ASSERT_TRUE(commands.load() >= 1);

        // cs.Stop() already closed the listener fd, which ControlSocket owns.
        close(client_fd);
        unlink(path.c_str());
    }
}

// Stop() while the reader thread is parked in read() with no data pending.
// Stop must unblock the reader (shutdown) and join it; it must return
// promptly rather than hanging, and tearing down must not race the reader.
TEST(ControlSocket_StopUnblocksParkedReader) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    std::atomic<int> commands{0};
    ControlSocket cs(listen_fd, [&](const ControlCommand&) { commands++; });
    cs.Start();

    int client_fd = connect_to(path);
    ASSERT_TRUE(client_fd >= 0);

    // Send one command and wait for it so we know the connection is
    // established and the reader is back parked in read().
    const std::string line = "{\"cmd\":\"play\",\"id\":7}\n";
    ASSERT_TRUE(write(client_fd, line.data(), line.size()) ==
                static_cast<ssize_t>(line.size()));
    auto deadline =
        std::chrono::steady_clock::now() + std::chrono::seconds(5);
    while (commands.load() == 0 &&
           std::chrono::steady_clock::now() < deadline) {
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }
    ASSERT_TRUE(commands.load() == 1);

    std::atomic<bool> stopped{false};
    std::thread stopper([&]() {
        cs.Stop();
        stopped.store(true);
    });

    deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5);
    while (!stopped.load() && std::chrono::steady_clock::now() < deadline) {
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }
    ASSERT_TRUE(stopped.load());  // Stop() must not hang
    stopper.join();

    // cs.Stop() already closed the listener fd, which ControlSocket owns.
    close(client_fd);
    unlink(path.c_str());
}

// Regression test for the Darwin shutdown hang: if the Go peer NEVER
// connects, the reader thread sits parked in accept(). shutdown() on a
// listening AF_UNIX socket does not wake accept() on macOS (it fails with
// ENOTCONN) — Stop() must close() the listener so the parked accept()
// returns and the thread join completes. Before the fix, Stop() hung forever
// here.
TEST(ControlSocket_StopReturnsPromptlyWhenNoClientEverConnects) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    // Heap-allocate so that on failure (hang) we can leak the object instead
    // of running a destructor (which calls Stop() again) that would hang the
    // whole test binary.
    auto* cs = new ControlSocket(listen_fd, [](const ControlCommand&) {});
    cs->Start();

    // Give the reader thread time to park in accept(); nothing ever connects.
    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    std::atomic<bool> stopped{false};
    std::thread stopper([cs, &stopped]() {
        cs->Stop();
        stopped.store(true);
    });

    // Watchdog: Stop() (which joins the reader) must return promptly.
    auto deadline =
        std::chrono::steady_clock::now() + std::chrono::seconds(5);
    while (!stopped.load() && std::chrono::steady_clock::now() < deadline) {
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }

    if (!stopped.load()) {
        // Hung (the pre-fix Darwin behavior): detach the stuck thread and
        // intentionally leak cs so the test binary itself can keep running.
        stopper.detach();
        ASSERT_TRUE(stopped.load());  // fail with a clear assertion
    }
    stopper.join();
    delete cs;
    unlink(path.c_str());
}

// EINTR resilience: a signal interrupting the reader's blocked read() must
// not kill the control channel. Before the fix, readLoop treated any
// read() < 0 — including EINTR — as fatal and exited, permanently severing
// the command stream.
//
// EINTR injection: install a no-op SIGUSR1 handler WITHOUT SA_RESTART, block
// SIGUSR1 in the main test thread (the reader thread inherited the default
// unblocked mask when Start() spawned it), then kill() the process — the
// kernel must deliver the signal to the reader thread, interrupting its
// read() with EINTR.
TEST(ControlSocket_ReadLoopSurvivesEINTR) {
    struct sigaction sa = {};
    sa.sa_handler = [](int) {};
    sigemptyset(&sa.sa_mask);
    sa.sa_flags = 0;  // crucially, no SA_RESTART
    ASSERT_TRUE(sigaction(SIGUSR1, &sa, nullptr) == 0);

    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    std::atomic<int> commands{0};
    ControlSocket cs(listen_fd, [&](const ControlCommand&) { commands++; });
    // The reader thread inherits this thread's signal mask (SIGUSR1
    // unblocked) at creation.
    cs.Start();

    int client_fd = connect_to(path);
    ASSERT_TRUE(client_fd >= 0);

    // Establish the connection and wait until the reader has consumed one
    // command, so we know it is back parked in read().
    const std::string line = "{\"cmd\":\"play\",\"id\":1}\n";
    ASSERT_TRUE(write(client_fd, line.data(), line.size()) ==
                static_cast<ssize_t>(line.size()));
    auto deadline =
        std::chrono::steady_clock::now() + std::chrono::seconds(5);
    while (commands.load() == 0 &&
           std::chrono::steady_clock::now() < deadline) {
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }
    ASSERT_TRUE(commands.load() == 1);

    // Block SIGUSR1 in this thread so kill() targets the reader thread.
    sigset_t block_set, old_set;
    sigemptyset(&block_set);
    sigaddset(&block_set, SIGUSR1);
    ASSERT_TRUE(pthread_sigmask(SIG_BLOCK, &block_set, &old_set) == 0);

    // Interrupt the parked read() a few times.
    for (int i = 0; i < 5; i++) {
        kill(getpid(), SIGUSR1);
        std::this_thread::sleep_for(std::chrono::milliseconds(5));
    }

    // The channel must still be alive: a further command must be processed.
    ASSERT_TRUE(write(client_fd, line.data(), line.size()) ==
                static_cast<ssize_t>(line.size()));
    deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5);
    while (commands.load() < 2 &&
           std::chrono::steady_clock::now() < deadline) {
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }
    ASSERT_TRUE(commands.load() == 2);

    pthread_sigmask(SIG_SETMASK, &old_set, nullptr);
    cs.Stop();
    close(client_fd);
    unlink(path.c_str());
}

// SendResponse racing Stop(): a response writer on another thread must never
// touch a closed (potentially recycled) fd. After Stop returns, SendResponse
// must be a safe no-op.
TEST(ControlSocket_SendResponseDuringAndAfterStop) {
    std::string path = tmp_socket_path();
    int listen_fd = create_listener(path);
    ASSERT_TRUE(listen_fd >= 0);

    std::atomic<int> commands{0};
    ControlSocket cs(listen_fd, [&](const ControlCommand&) { commands++; });
    cs.Start();

    int client_fd = connect_to(path);
    ASSERT_TRUE(client_fd >= 0);

    // Establish the connection (reader assigns conn_fd_ after accept).
    const std::string line = "{\"cmd\":\"play\",\"id\":3}\n";
    ASSERT_TRUE(write(client_fd, line.data(), line.size()) > 0);
    while (commands.load() == 0) {
        std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }

    std::atomic<bool> writer_stop{false};
    std::thread responder([&]() {
        while (!writer_stop.load()) {
            cs.SendResponse("{\"id\":3,\"status\":\"ok\"}\n");
        }
    });

    // Drain the client side so responses don't fill the send buffer.
    std::atomic<bool> drain_stop{false};
    std::thread drainer([&]() {
        char buf[4096];
        while (!drain_stop.load()) {
            if (read(client_fd, buf, sizeof(buf)) <= 0) break;
        }
    });

    std::this_thread::sleep_for(std::chrono::milliseconds(20));
    cs.Stop();

    // SendResponse after Stop must be a harmless no-op.
    cs.SendResponse("{\"id\":3,\"status\":\"ok\"}\n");

    writer_stop.store(true);
    responder.join();
    drain_stop.store(true);
    shutdown(client_fd, SHUT_RDWR);
    drainer.join();

    // cs.Stop() already closed the listener fd, which ControlSocket owns.
    close(client_fd);
    unlink(path.c_str());
}

// ---------------------------------------------------------------------------
// Entry Point
// ---------------------------------------------------------------------------

int main() {
    // Writes to sockets torn down by Stop() mid-test must fail with EPIPE,
    // not kill the process.
    signal(SIGPIPE, SIG_IGN);

    std::cout << "Running control socket lifecycle tests:" << std::endl;

    int run = 0;
    int passed = 0;

    for (const auto& tc : test_registry()) {
        run++;
        std::cout << "  " << tc.name << "... " << std::flush;
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
