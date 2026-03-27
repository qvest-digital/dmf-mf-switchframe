NVCC := nvcc
ARCH := -arch=sm_89  # Ada Lovelace (L4)
CFLAGS := -O3 -Xcompiler -fPIC -std=c++17
SOURCES := convert.cu blend.cu key.cu scale.cu composite.cu dsk.cu stmap.cu fruc.cu v210.cu preprocess.cu mask_smooth.cu colorgrade.cu blur.cu sharpness.cu neural_key.cu occlusion_fuse.cu ar_composite.cu
OBJECTS := $(SOURCES:.cu=.o)
LIBRARY := libswitchframe_cuda.a

# TensorRT wrapper (C++, compiled with g++ not nvcc)
CXX := g++
CXXFLAGS := -O2 -fPIC -std=c++17 -I/usr/local/cuda/include -I/usr/include/x86_64-linux-gnu
TRT_LIBRARY := libswitchframe_tensorrt.a

# OptiX wrapper (C++, compiled with g++ not nvcc)
# Requires HAS_OPTIX=1 and OPTIX_INCLUDE set to OptiX SDK include dir.
OPTIX_INCLUDE ?= /usr/local/include
OPTIX_CXXFLAGS := $(CXXFLAGS)
OPTIX_LIBRARY := libswitchframe_optix.a

.PHONY: all clean tensorrt optix

all: $(LIBRARY)

tensorrt: $(TRT_LIBRARY)

optix: $(OPTIX_LIBRARY)

$(LIBRARY): $(OBJECTS)
	$(NVCC) $(ARCH) -lib -o $@ $^

%.o: %.cu common.cuh
	$(NVCC) $(ARCH) $(CFLAGS) -c -o $@ $<

$(TRT_LIBRARY): tensorrt_wrapper.o
	ar rcs $@ $^

tensorrt_wrapper.o: tensorrt_wrapper.cpp tensorrt_wrapper.h
	$(CXX) $(CXXFLAGS) -c -o $@ $<

$(OPTIX_LIBRARY): optix_wrapper.o
	ar rcs $@ $^

optix_wrapper.o: optix_wrapper.cpp optix_wrapper.h
ifdef HAS_OPTIX
	$(CXX) $(OPTIX_CXXFLAGS) -DHAS_OPTIX -I$(OPTIX_INCLUDE) -c -o $@ $<
else
	$(CXX) $(OPTIX_CXXFLAGS) -c -o $@ $<
endif

clean:
	rm -f $(OBJECTS) $(LIBRARY) tensorrt_wrapper.o $(TRT_LIBRARY) optix_wrapper.o $(OPTIX_LIBRARY)
