cmake_minimum_required(VERSION 3.19)
project(switchframe-cef LANGUAGES C CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# CEF_ROOT must be set to the extracted CEF binary distribution
if(NOT DEFINED CEF_ROOT)
  message(FATAL_ERROR "CEF_ROOT must be set to the CEF binary distribution path")
endif()

# Add CEF's cmake directory to module path
set(CMAKE_MODULE_PATH ${CMAKE_MODULE_PATH} "${CEF_ROOT}/cmake")
find_package(CEF REQUIRED)
add_subdirectory(${CEF_ROOT}/libcef_dll ${CMAKE_BINARY_DIR}/libcef_dll_wrapper)

# Sources
set(SOURCES
  src/main.cpp
  src/app.cpp
  src/render_handler.cpp
  src/browser_pool.cpp
  src/control_socket.cpp
  src/frame_socket.cpp
  src/json_parse.cpp
  src/adapter.cpp
)

add_executable(switchframe-cef ${SOURCES})
target_include_directories(switchframe-cef PRIVATE
  ${CEF_ROOT}
  ${CMAKE_CURRENT_SOURCE_DIR}/src
)
# Link against libcef (platform-specific)
if(APPLE)
  set(CEF_LIB "${CEF_ROOT}/Release/Chromium Embedded Framework.framework/Chromium Embedded Framework")
elseif(UNIX)
  set(CEF_LIB "${CEF_ROOT}/Release/libcef.so")
else()
  set(CEF_LIB "${CEF_ROOT}/Release/libcef.lib")
endif()

if(APPLE)
  # On macOS the framework is loaded at runtime via CefScopedLibraryLoader
  # (dlopen). Direct-linking conflicts with the loader and is unnecessary.
  target_link_libraries(switchframe-cef PRIVATE libcef_dll_wrapper)
else()
  target_link_libraries(switchframe-cef PRIVATE libcef_dll_wrapper "${CEF_LIB}")
endif()

if(UNIX AND NOT APPLE)
  # Linux needs pthread and dl
  target_link_libraries(switchframe-cef PRIVATE pthread dl)
  # Set rpath so libcef.so is found at runtime
  set_target_properties(switchframe-cef PROPERTIES
    BUILD_RPATH "${CEF_ROOT}/Release"
    INSTALL_RPATH "$ORIGIN"
  )
endif()

# macOS Helper subprocess binary. Lives at:
#   bin/switchframe-cef.app/Contents/Frameworks/switchframe-cef Helper.app/Contents/MacOS/switchframe-cef Helper
# Built by the bootstrap script (scripts/setup-cef-mac.sh) and copied into
# the bundle layout. The output filename retains the space ("switchframe-cef Helper")
# to match the CEF-on-macOS convention CefSettings auto-discovers.
if(APPLE)
  add_executable(switchframe-cef-helper src/process_helper_mac.cc)
  target_include_directories(switchframe-cef-helper PRIVATE ${CEF_ROOT})
  target_link_libraries(switchframe-cef-helper PRIVATE libcef_dll_wrapper)
  set_target_properties(switchframe-cef-helper PROPERTIES
    OUTPUT_NAME "switchframe-cef Helper"
  )
endif()

# Copy resources to build output
if(IS_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/resources")
  file(COPY resources/ DESTINATION ${CMAKE_BINARY_DIR}/resources)
endif()

# Test targets (no CEF dependency — test protocol, JSON parse, adapter logic only)
set(TEST_COMMON_PROPS CXX_STANDARD 17 CXX_STANDARD_REQUIRED ON)

add_executable(switchframe-cef-protocol-test tests/protocol_test.cpp)
target_include_directories(switchframe-cef-protocol-test PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
set_target_properties(switchframe-cef-protocol-test PROPERTIES ${TEST_COMMON_PROPS})

add_executable(switchframe-cef-adapter-test tests/adapter_test.cpp src/adapter.cpp)
target_include_directories(switchframe-cef-adapter-test PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
set_target_properties(switchframe-cef-adapter-test PROPERTIES ${TEST_COMMON_PROPS})

add_executable(switchframe-cef-json-parse-test tests/json_parse_test.cpp src/json_parse.cpp)
target_include_directories(switchframe-cef-json-parse-test PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
set_target_properties(switchframe-cef-json-parse-test PROPERTIES ${TEST_COMMON_PROPS})

add_executable(switchframe-cef-control-socket-test tests/control_socket_test.cpp src/json_parse.cpp)
target_include_directories(switchframe-cef-control-socket-test PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
set_target_properties(switchframe-cef-control-socket-test PROPERTIES ${TEST_COMMON_PROPS})

add_executable(switchframe-cef-frame-socket-test tests/frame_socket_test.cpp)
target_include_directories(switchframe-cef-frame-socket-test PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
set_target_properties(switchframe-cef-frame-socket-test PROPERTIES ${TEST_COMMON_PROPS})
