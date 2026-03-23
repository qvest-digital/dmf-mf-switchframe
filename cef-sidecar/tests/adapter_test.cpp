#include <functional>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

#include "../src/adapter.h"

// ---------------------------------------------------------------------------
// Minimal test framework (same as protocol_test.cpp)
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

#define ASSERT_FALSE(expr)                                               \
    do {                                                                 \
        if (expr)                                                        \
            throw std::runtime_error("Assertion failed: NOT " #expr     \
                                     " at line " +                       \
                                     std::to_string(__LINE__));          \
    } while (0)

// ---------------------------------------------------------------------------
// Helper: check if a string contains a substring
// ---------------------------------------------------------------------------

static bool contains(const std::string& haystack, const std::string& needle) {
    return haystack.find(needle) != std::string::npos;
}

// ---------------------------------------------------------------------------
// Adapter.js Content Tests
// ---------------------------------------------------------------------------

TEST(AdapterJS_NotEmpty) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_FALSE(js.empty());
}

TEST(AdapterJS_ContainsSwitchframeGlobal) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "window.__switchframe"));
}

TEST(AdapterJS_ContainsDetectFunction) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "detect: function()"));
}

TEST(AdapterJS_ContainsPlayFunction) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "play: function()"));
}

TEST(AdapterJS_ContainsStopFunction) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "stop: function()"));
}

TEST(AdapterJS_ContainsNextFunction) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "next: function()"));
}

TEST(AdapterJS_ContainsUpdateFunction) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "update: function(data)"));
}

TEST(AdapterJS_ContainsCustomFunction) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "custom: function(action, payload)"));
}

TEST(AdapterJS_ContainsReportFormatFunction) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "reportFormat: function(fmt)"));
}

TEST(AdapterJS_ContainsOGrafDetection) {
    const std::string& js = adapter::GetAdapterJS();
    // OGraf detection: link[rel="ograf-manifest"]
    ASSERT_TRUE(contains(js, "ograf-manifest"));
    // OGraf API: playAction
    ASSERT_TRUE(contains(js, "playAction"));
    // OGraf API: stopAction
    ASSERT_TRUE(contains(js, "stopAction"));
    // OGraf API: updateAction
    ASSERT_TRUE(contains(js, "updateAction"));
    // OGraf API: customAction
    ASSERT_TRUE(contains(js, "customAction"));
}

TEST(AdapterJS_ContainsCasparCGDetection) {
    const std::string& js = adapter::GetAdapterJS();
    // CasparCG detection: window.play
    ASSERT_TRUE(contains(js, "typeof window.play === 'function'"));
    // CasparCG API: window.stop
    ASSERT_TRUE(contains(js, "window.stop"));
    // CasparCG API: window.next
    ASSERT_TRUE(contains(js, "window.next"));
    // CasparCG API: window.update
    ASSERT_TRUE(contains(js, "window.update"));
}

TEST(AdapterJS_ContainsNativeBinding) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "__switchframe_native"));
    ASSERT_TRUE(contains(js, "reportFormat"));
}

TEST(AdapterJS_WrappedInIIFE) {
    const std::string& js = adapter::GetAdapterJS();
    // Starts with IIFE pattern
    ASSERT_TRUE(contains(js, "(function()"));
    // Ends with IIFE invocation
    ASSERT_TRUE(contains(js, "})();"));
}

TEST(AdapterJS_HasUseStrict) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "'use strict'"));
}

TEST(AdapterJS_ContainsFormatField) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "format: 'unknown'"));
}

TEST(AdapterJS_ContainsLoadEventListener) {
    const std::string& js = adapter::GetAdapterJS();
    ASSERT_TRUE(contains(js, "document.readyState"));
    ASSERT_TRUE(contains(js, "addEventListener('load'"));
}

TEST(AdapterJS_ReturnsSameInstance) {
    // Verify GetAdapterJS() returns the same reference each time.
    const std::string* ptr1 = &adapter::GetAdapterJS();
    const std::string* ptr2 = &adapter::GetAdapterJS();
    ASSERT_TRUE(ptr1 == ptr2);
}

// ---------------------------------------------------------------------------
// Entry Point
// ---------------------------------------------------------------------------

int main() {
    std::cout << "Running adapter.js tests:" << std::endl;

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
