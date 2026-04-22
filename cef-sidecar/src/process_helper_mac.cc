// Entry point for the macOS CEF Helper process.
//
// CEF on macOS spawns renderer/GPU/utility subprocesses by exec'ing a
// separate Helper binary that lives at:
//   <App>.app/Contents/Frameworks/<App> Helper.app/Contents/MacOS/<App> Helper
//
// The helper must dynamically load the CEF framework via LoadInHelper before
// invoking any CEF API. After that it just hands off to CefExecuteProcess.
//
// This file is built only on macOS; it is not part of the Linux/Windows path.

#include <iostream>

#include "include/cef_app.h"
#include "include/wrapper/cef_library_loader.h"

int main(int argc, char* argv[]) {
    CefScopedLibraryLoader library_loader;
    if (!library_loader.LoadInHelper()) {
        std::cerr << "helper: LoadInHelper failed" << std::endl;
        return 1;
    }

    CefMainArgs main_args(argc, argv);
    return CefExecuteProcess(main_args, nullptr, nullptr);
}
