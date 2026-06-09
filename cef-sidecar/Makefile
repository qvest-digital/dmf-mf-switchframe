.PHONY: test clean

test: build/protocol_test build/adapter_test build/json_parse_test build/control_socket_test build/control_socket_lifecycle_test build/frame_socket_test
	./build/protocol_test
	./build/adapter_test
	./build/json_parse_test
	./build/control_socket_test
	./build/control_socket_lifecycle_test
	./build/frame_socket_test

build/protocol_test: tests/protocol_test.cpp src/protocol.h
	@mkdir -p build
	$(CXX) -std=c++17 -Wall -Wextra -o $@ $< -I src

build/adapter_test: tests/adapter_test.cpp src/adapter.h src/adapter.cpp
	@mkdir -p build
	$(CXX) -std=c++17 -Wall -Wextra -o $@ tests/adapter_test.cpp src/adapter.cpp -I src

build/json_parse_test: tests/json_parse_test.cpp src/json_parse.h src/json_parse.cpp src/control_socket.h
	@mkdir -p build
	$(CXX) -std=c++17 -Wall -Wextra -o $@ tests/json_parse_test.cpp src/json_parse.cpp -I src

build/control_socket_test: tests/control_socket_test.cpp src/control_socket.h src/json_parse.h src/json_parse.cpp src/protocol.h
	@mkdir -p build
	$(CXX) -std=c++17 -Wall -Wextra -o $@ tests/control_socket_test.cpp src/json_parse.cpp -I src

# Compiles the real control_socket.cpp against the CEF stubs in
# tests/cef_stubs and runs under ThreadSanitizer to catch shutdown races
# (Stop vs readLoop/SendResponse on conn_fd_).
build/control_socket_lifecycle_test: tests/control_socket_lifecycle_test.cpp src/control_socket.h src/control_socket.cpp src/json_parse.h src/json_parse.cpp tests/cef_stubs/include/cef_task.h
	@mkdir -p build
	$(CXX) -std=c++17 -Wall -Wextra -pthread -g -fsanitize=thread -o $@ tests/control_socket_lifecycle_test.cpp src/control_socket.cpp src/json_parse.cpp -I src -I tests/cef_stubs

build/frame_socket_test: tests/frame_socket_test.cpp src/frame_socket.h src/frame_socket.cpp src/protocol.h
	@mkdir -p build
	$(CXX) -std=c++17 -Wall -Wextra -pthread -o $@ tests/frame_socket_test.cpp src/frame_socket.cpp -I src

clean:
	rm -rf build
