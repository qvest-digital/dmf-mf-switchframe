cmake_minimum_required(VERSION 3.19)
project(switchframe-cef LANGUAGES CXX)

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
)

add_executable(switchframe-cef ${SOURCES})
target_include_directories(switchframe-cef PRIVATE
  ${CEF_ROOT}
  ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(switchframe-cef PRIVATE libcef_dll_wrapper ${CEF_BINARY_DIR}/libcef.dylib)

# Copy resources to build output
if(IS_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/resources")
  file(COPY resources/ DESTINATION ${CMAKE_BINARY_DIR}/resources)
endif()

# Test target (no CEF dependency — tests protocol logic only)
add_executable(switchframe-cef-tests tests/protocol_test.cpp)
target_include_directories(switchframe-cef-tests PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
set_target_properties(switchframe-cef-tests PROPERTIES
  CXX_STANDARD 17
  CXX_STANDARD_REQUIRED ON
)
