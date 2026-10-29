#include <sys/stat.h>
#include <unistd.h>

#include <cstdlib>
#include <fstream>
#include <functional>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

#include "../src/url_policy.h"

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
// Fixture: a templates directory with a link out of it, a sibling whose
// name shares its prefix, and a secret beside it.
// ---------------------------------------------------------------------------

struct Fixture {
    std::string base;       // resolved temp directory
    std::string templates;  // base/templates
    std::string root;       // ResolveRoot(templates)
};

static void write_file(const std::string& path) {
    std::ofstream(path) << "x";
}

static const Fixture& fixture() {
    static Fixture f = [] {
        const char* tmp = std::getenv("TMPDIR");
        std::string tmpl = std::string(tmp && *tmp ? tmp : "/tmp") + "/url_policy_test.XXXXXX";
        std::vector<char> buf(tmpl.begin(), tmpl.end());
        buf.push_back('\0');
        if (mkdtemp(buf.data()) == nullptr) throw std::runtime_error("mkdtemp");
        Fixture f;
        f.base = url_policy::ResolveRoot(buf.data());
        f.templates = f.base + "/templates";
        mkdir(f.templates.c_str(), 0755);
        mkdir((f.templates + "/a").c_str(), 0755);
        mkdir((f.templates + "/with space").c_str(), 0755);
        mkdir((f.base + "/templates2").c_str(), 0755);
        write_file(f.templates + "/a/index.html");
        write_file(f.templates + "/with space/index.html");
        write_file(f.base + "/templates2/index.html");
        write_file(f.base + "/secret");
        if (symlink("../secret", (f.templates + "/out").c_str()) != 0 ||
            symlink("templates", (f.base + "/link").c_str()) != 0) {
            throw std::runtime_error("symlink");
        }
        f.root = url_policy::ResolveRoot(f.templates);
        return f;
    }();
    return f;
}

static void remove_fixture() {
    const auto& f = fixture();
    for (const char* p : {"/templates/a/index.html", "/templates/with space/index.html",
                          "/templates2/index.html", "/secret", "/templates/out", "/link"}) {
        unlink((f.base + p).c_str());
    }
    for (const char* p : {"/templates/a", "/templates/with space", "/templates", "/templates2", ""}) {
        rmdir((f.base + p).c_str());
    }
}

static bool allowed(const std::string& url) {
    return url_policy::Allowed(url, fixture().root);
}

// ---------------------------------------------------------------------------
// file
// ---------------------------------------------------------------------------

TEST(File_TemplateBelowRoot) {
    const auto& f = fixture();
    ASSERT_TRUE(allowed("file://" + f.templates + "/a/index.html"));
    ASSERT_TRUE(allowed("file://localhost" + f.templates + "/a/index.html"));
}

// ograf-host takes the graphic's manifest in its query.
TEST(File_QueryAndFragmentAreNotPath) {
    const auto& f = fixture();
    ASSERT_TRUE(allowed("file://" + f.templates +
                        "/a/index.html?manifest=https%3A%2F%2Fg.test%2Fm.json#x"));
}

TEST(File_PercentEncodedPath) {
    const auto& f = fixture();
    ASSERT_TRUE(allowed("file://" + f.templates + "/with%20space/index.html"));
}

// What the review found: a module running in a file:// template page
// navigating a frame to the service account token.
TEST(File_OutsideRootRefused) {
    ASSERT_FALSE(allowed("file:///var/run/secrets/kubernetes.io/serviceaccount/token"));
    ASSERT_FALSE(allowed("file:///etc/passwd"));
    ASSERT_FALSE(allowed("file://" + fixture().base + "/secret"));
}

TEST(File_TraversalRefused) {
    const auto& f = fixture();
    ASSERT_FALSE(allowed("file://" + f.templates + "/a/../../secret"));
    ASSERT_FALSE(allowed("file://" + f.templates + "/a/%2e%2e/%2e%2e/secret"));
}

TEST(File_LinkOutOfRootRefused) {
    ASSERT_FALSE(allowed("file://" + fixture().templates + "/out"));
}

// A root given through a link matches the files below its target.
TEST(File_RootGivenThroughLink) {
    const auto& f = fixture();
    std::string root = url_policy::ResolveRoot(f.base + "/link");
    ASSERT_TRUE(root == f.root);
    ASSERT_TRUE(url_policy::Allowed("file://" + f.base + "/link/a/index.html", root));
}

TEST(File_SiblingSharingThePrefixRefused) {
    ASSERT_FALSE(allowed("file://" + fixture().base + "/templates2/index.html"));
}

TEST(File_RootItselfRefused) {
    const auto& f = fixture();
    ASSERT_FALSE(allowed("file://" + f.templates));
    ASSERT_FALSE(allowed("file://" + f.templates + "/"));
}

TEST(File_MissingFileRefused) {
    ASSERT_FALSE(allowed("file://" + fixture().templates + "/a/missing.html"));
}

TEST(File_OtherHostRefused) {
    ASSERT_FALSE(allowed("file://fileserver" + fixture().templates + "/a/index.html"));
}

TEST(File_MalformedEscapesRefused) {
    const auto& f = fixture();
    ASSERT_FALSE(allowed("file://" + f.templates + "/a/index.html%00.png"));
    ASSERT_FALSE(allowed("file://" + f.templates + "/a/index.html%zz"));
    ASSERT_FALSE(allowed("file://" + f.templates + "/a/index.html%2"));
}

TEST(File_NoRootRefusesEverything) {
    ASSERT_TRUE(url_policy::ResolveRoot("").empty());
    ASSERT_TRUE(url_policy::ResolveRoot("/nonexistent/templates").empty());
    ASSERT_FALSE(url_policy::Allowed("file://" + fixture().templates + "/a/index.html", ""));
}

// ---------------------------------------------------------------------------
// Other schemes
// ---------------------------------------------------------------------------

TEST(Https_AnyHost) {
    ASSERT_TRUE(allowed("https://cdnjs.cloudflare.com/ajax/libs/gsap/3.12.5/gsap.min.js"));
    ASSERT_TRUE(allowed("HTTPS://graphics.test/m.json"));
    ASSERT_TRUE(allowed("wss://data.test/feed"));
}

TEST(Https_WithoutHostRefused) {
    ASSERT_FALSE(allowed("https:///etc/passwd"));
    ASSERT_FALSE(allowed("https:"));
    ASSERT_FALSE(allowed("https:host"));
}

// Plain http reaches cluster-internal services with no certificate naming
// them, such as an engine's own metrics.
TEST(Http_Refused) {
    ASSERT_FALSE(allowed("http://10.0.0.1:9090/metrics"));
    ASSERT_FALSE(allowed("ws://10.0.0.1/feed"));
}

TEST(InlineContent_Allowed) {
    ASSERT_TRUE(allowed("data:text/html,<p>x</p>"));
    ASSERT_TRUE(allowed("blob:https://graphics.test/0b3c6a2e"));
    ASSERT_TRUE(allowed("about:blank"));
    ASSERT_TRUE(allowed("about:srcdoc"));
}

TEST(EverythingElse_Refused) {
    ASSERT_FALSE(allowed("about:version"));
    ASSERT_FALSE(allowed("chrome://gpu"));
    ASSERT_FALSE(allowed("devtools://devtools/bundled/inspector.html"));
    ASSERT_FALSE(allowed("view-source:https://graphics.test/"));
    ASSERT_FALSE(allowed("javascript:alert(1)"));
    ASSERT_FALSE(allowed("ftp://files.test/x"));
    ASSERT_FALSE(allowed(""));
    ASSERT_FALSE(allowed(":"));
    ASSERT_FALSE(allowed("no-scheme"));
}

// ---------------------------------------------------------------------------
// Entry Point
// ---------------------------------------------------------------------------

int main() {
    std::cout << "Running URL policy tests:" << std::endl;

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

    remove_fixture();

    std::cout << std::endl;
    std::cout << passed << "/" << run << " tests passed." << std::endl;
    return (passed == run) ? 0 : 1;
}
