//go:build cgo && mxl

package mxl

/*
#cgo pkg-config: libmxl
#include <mxl/mxl.h>
#include <mxl/flow.h>
#include <mxl/flowinfo.h>
#include <mxl/time.h>
#include <mxl/rational.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"
