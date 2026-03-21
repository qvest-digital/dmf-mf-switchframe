XCRUN := xcrun
METAL := $(XCRUN) -sdk macosx metal
METALLIB := $(XCRUN) -sdk macosx metallib

SOURCES := convert.metal blend.metal scale.metal key.metal \
           composite.metal dsk.metal stmap.metal fruc.metal v210.metal
AIRFILES := $(SOURCES:.metal=.air)
LIBRARY := switchframe_gpu.metallib

all: $(LIBRARY)

%.air: %.metal common.h
	$(METAL) -c -o $@ $<

$(LIBRARY): $(AIRFILES)
	$(METALLIB) -o $@ $^

clean:
	rm -f $(AIRFILES) $(LIBRARY)

.PHONY: all clean
