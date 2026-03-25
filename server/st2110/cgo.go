//go:build cgo && st2110

package st2110

/*
#cgo pkg-config: mtl
#include <mtl/mtl_api.h>
#include <mtl/st_pipeline_api.h>
#include <mtl/st30_pipeline_api.h>
#include <mtl/st40_pipeline_api.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"
