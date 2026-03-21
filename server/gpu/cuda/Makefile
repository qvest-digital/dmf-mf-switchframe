NVCC := nvcc
ARCH := -arch=sm_89  # Ada Lovelace (L4)
CFLAGS := -O3 -Xcompiler -fPIC -std=c++17
SOURCES := convert.cu blend.cu key.cu scale.cu composite.cu dsk.cu stmap.cu fruc.cu v210.cu preprocess.cu mask_smooth.cu
OBJECTS := $(SOURCES:.cu=.o)
LIBRARY := libswitchframe_cuda.a

# TensorRT wrapper (C++, compiled with g++ not nvcc)
CXX := g++
CXXFLAGS := -O2 -fPIC -std=c++17 -I/usr/local/cuda/include -I/usr/include/x86_64-linux-gnu
TRT_LIBRARY := libswitchframe_tensorrt.a

.PHONY: all clean tensorrt

all: $(LIBRARY)

tensorrt: $(TRT_LIBRARY)

$(LIBRARY): $(OBJECTS)
	$(NVCC) $(ARCH) -lib -o $@ $^

%.o: %.cu common.cuh
	$(NVCC) $(ARCH) $(CFLAGS) -c -o $@ $<

$(TRT_LIBRARY): tensorrt_wrapper.o
	ar rcs $@ $^

tensorrt_wrapper.o: tensorrt_wrapper.cpp
	$(CXX) $(CXXFLAGS) -c -o $@ $<

clean:
	rm -f $(OBJECTS) $(LIBRARY) tensorrt_wrapper.o $(TRT_LIBRARY)
