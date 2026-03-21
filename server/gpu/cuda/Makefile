NVCC := nvcc
ARCH := -arch=sm_89  # Ada Lovelace (L4)
CFLAGS := -O3 -Xcompiler -fPIC -std=c++17
SOURCES := convert.cu blend.cu key.cu scale.cu composite.cu dsk.cu stmap.cu
OBJECTS := $(SOURCES:.cu=.o)
LIBRARY := libswitchframe_cuda.a

.PHONY: all clean

all: $(LIBRARY)

$(LIBRARY): $(OBJECTS)
	$(NVCC) $(ARCH) -lib -o $@ $^

%.o: %.cu common.cuh
	$(NVCC) $(ARCH) $(CFLAGS) -c -o $@ $<

clean:
	rm -f $(OBJECTS) $(LIBRARY)
