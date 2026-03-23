.PHONY: test clean

test: build/protocol_test build/adapter_test build/json_parse_test build/control_socket_test build/frame_socket_test
	./build/protocol_test
	./build/adapter_test
	./build/json_parse_test
	./build/control_socket_test
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

build/frame_socket_test: tests/frame_socket_test.cpp src/frame_socket.h src/protocol.h
	@mkdir -p build
	$(CXX) -std=c++17 -Wall -Wextra -o $@ tests/frame_socket_test.cpp -I src

clean:
	rm -rf build
