.PHONY: test clean

test: build/protocol_test
	./build/protocol_test

build/protocol_test: tests/protocol_test.cpp src/protocol.h
	@mkdir -p build
	$(CXX) -std=c++17 -Wall -Wextra -o $@ $< -I src

clean:
	rm -rf build
