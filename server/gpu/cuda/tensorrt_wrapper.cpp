// tensorrt_wrapper.cpp — C-linkage wrappers around the TensorRT C++ API.
// Compiled separately and linked into the Go binary via cgo LDFLAGS.
// Build: g++ -O2 -fPIC -std=c++17 -c tensorrt_wrapper.cpp -I/usr/local/cuda/include -I/usr/include/x86_64-linux-gnu

#include <NvInfer.h>
#include <NvOnnxParser.h>
#include <cuda_runtime.h>

#include <fstream>
#include <memory>
#include <string>
#include <vector>
#include <cstring>
#include <cstdio>
#include <cstdarg>

// Thread-local error message buffer.
static thread_local char trt_error_buf[1024] = {0};

static void set_error(const char* fmt, ...) {
    va_list args;
    va_start(args, fmt);
    vsnprintf(trt_error_buf, sizeof(trt_error_buf), fmt, args);
    va_end(args);
}

// Minimal TensorRT logger — writes warnings and errors to stderr.
class TRTLogger : public nvinfer1::ILogger {
public:
    void log(Severity severity, const char* msg) noexcept override {
        if (severity <= Severity::kWARNING) {
            fprintf(stderr, "[TensorRT %s] %s\n", severityStr(severity), msg);
        }
    }
private:
    static const char* severityStr(Severity s) {
        switch (s) {
            case Severity::kINTERNAL_ERROR: return "INTERNAL_ERROR";
            case Severity::kERROR:          return "ERROR";
            case Severity::kWARNING:        return "WARNING";
            case Severity::kINFO:           return "INFO";
            case Severity::kVERBOSE:        return "VERBOSE";
            default:                        return "UNKNOWN";
        }
    }
};

static TRTLogger& getTRTLogger() {
    static TRTLogger logger;
    return logger;
}

// Helper: compute total volume (number of elements) of a tensor shape.
static int64_t volume(const nvinfer1::Dims& dims) {
    int64_t vol = 1;
    for (int i = 0; i < dims.nbDims; ++i) {
        // Treat -1 (dynamic) as 1 for size estimation.
        vol *= (dims.d[i] > 0) ? dims.d[i] : 1;
    }
    return vol;
}

// Stored engine wrapper — holds the runtime and engine together so the
// runtime outlives the engine (required by TensorRT lifecycle).
struct TRTEngineWrapper {
    std::unique_ptr<nvinfer1::IRuntime> runtime;
    std::unique_ptr<nvinfer1::ICudaEngine> engine;

    // Scratch buffer for unused output tensors. u2netp has 7 outputs but we
    // only need the first one. TensorRT requires all outputs to be bound, so
    // we allocate a single scratch buffer large enough for the largest unused
    // output and bind all extra outputs there.
    void* scratchOutputDev = nullptr;
    size_t scratchOutputBytes = 0;
};

extern "C" {

// trt_build_engine builds a TensorRT engine from ONNX and serializes to planPath.
// Returns 0 on success, non-zero on failure.
int trt_build_engine(const char* onnxPath, const char* planPath,
                     int maxBatch, int useFP16, int useINT8) {
    auto builder = std::unique_ptr<nvinfer1::IBuilder>(
        nvinfer1::createInferBuilder(getTRTLogger()));
    if (!builder) {
        set_error("createInferBuilder failed");
        return -1;
    }

    // TensorRT 10+: createNetworkV2(0) — explicit batch is the only mode.
    auto network = std::unique_ptr<nvinfer1::INetworkDefinition>(
        builder->createNetworkV2(0));
    if (!network) {
        set_error("createNetworkV2 failed");
        return -1;
    }

    auto parser = std::unique_ptr<nvonnxparser::IParser>(
        nvonnxparser::createParser(*network, getTRTLogger()));
    if (!parser) {
        set_error("createParser failed");
        return -1;
    }

    if (!parser->parseFromFile(onnxPath,
            static_cast<int>(nvinfer1::ILogger::Severity::kWARNING))) {
        set_error("ONNX parse failed for: %s", onnxPath);
        return -1;
    }

    auto config = std::unique_ptr<nvinfer1::IBuilderConfig>(
        builder->createBuilderConfig());
    if (!config) {
        set_error("createBuilderConfig failed");
        return -1;
    }

    // 256 MB workspace.
    config->setMemoryPoolLimit(nvinfer1::MemoryPoolType::kWORKSPACE, 256ULL << 20);

    if (useFP16 && builder->platformHasFastFp16()) {
        config->setFlag(nvinfer1::BuilderFlag::kFP16);
    }
    if (useINT8 && builder->platformHasFastInt8()) {
        config->setFlag(nvinfer1::BuilderFlag::kINT8);
        // Note: INT8 calibration is not implemented yet.
        // Without a calibrator, INT8 will fall back to FP16/FP32.
    }

    // Set optimization profile for dynamic batch.
    auto profile = builder->createOptimizationProfile();
    if (!profile) {
        set_error("createOptimizationProfile failed");
        return -1;
    }

    // Set min/opt/max for the first input tensor.
    int numInputs = network->getNbInputs();
    for (int i = 0; i < numInputs; i++) {
        auto input = network->getInput(i);
        auto dims = input->getDimensions();

        // Replace dynamic batch dimension (-1) with concrete values.
        nvinfer1::Dims minDims = dims, optDims = dims, maxDims = dims;
        if (dims.nbDims > 0 && dims.d[0] == -1) {
            minDims.d[0] = 1;
            optDims.d[0] = 1;  // optimize for batch=1 (real-time inference)
            maxDims.d[0] = maxBatch;
        }

        profile->setDimensions(input->getName(), nvinfer1::OptProfileSelector::kMIN, minDims);
        profile->setDimensions(input->getName(), nvinfer1::OptProfileSelector::kOPT, optDims);
        profile->setDimensions(input->getName(), nvinfer1::OptProfileSelector::kMAX, maxDims);
    }
    config->addOptimizationProfile(profile);

    // Build the serialized engine.
    auto serialized = std::unique_ptr<nvinfer1::IHostMemory>(
        builder->buildSerializedNetwork(*network, *config));
    if (!serialized || serialized->size() == 0) {
        set_error("buildSerializedNetwork failed");
        return -1;
    }

    // Write plan to file.
    if (planPath && planPath[0] != '\0') {
        std::ofstream out(planPath, std::ios::binary);
        if (!out.is_open()) {
            set_error("cannot open plan path for writing: %s", planPath);
            return -1;
        }
        out.write(static_cast<const char*>(serialized->data()), serialized->size());
        if (!out.good()) {
            set_error("write to plan path failed: %s", planPath);
            return -1;
        }
    }

    return 0;
}

// trt_load_engine deserializes a TensorRT plan file into an engine.
// Returns engine handle (opaque pointer) or NULL on failure.
void* trt_load_engine(const char* planPath) {
    std::ifstream in(planPath, std::ios::binary | std::ios::ate);
    if (!in.is_open()) {
        set_error("cannot open plan file: %s", planPath);
        return nullptr;
    }

    auto size = in.tellg();
    in.seekg(0, std::ios::beg);
    std::vector<char> data(size);
    if (!in.read(data.data(), size)) {
        set_error("cannot read plan file: %s", planPath);
        return nullptr;
    }

    auto wrapper = new TRTEngineWrapper();
    wrapper->runtime.reset(nvinfer1::createInferRuntime(getTRTLogger()));
    if (!wrapper->runtime) {
        set_error("createInferRuntime failed");
        delete wrapper;
        return nullptr;
    }

    wrapper->engine.reset(
        wrapper->runtime->deserializeCudaEngine(data.data(), data.size()));
    if (!wrapper->engine) {
        set_error("deserializeCudaEngine failed for: %s", planPath);
        delete wrapper;
        return nullptr;
    }

    return static_cast<void*>(wrapper);
}

// trt_create_context creates an execution context from an engine.
void* trt_create_context(void* engineHandle) {
    if (!engineHandle) {
        set_error("null engine handle");
        return nullptr;
    }
    auto* wrapper = static_cast<TRTEngineWrapper*>(engineHandle);
    auto ctx = wrapper->engine->createExecutionContext();
    if (!ctx) {
        set_error("createExecutionContext failed");
        return nullptr;
    }
    return static_cast<void*>(ctx);
}

// trt_infer runs async inference.
// inputDevPtr/outputDevPtr are CUDA device pointers.
// engineHandle is the TRTEngineWrapper* (needed for scratch buffer caching).
// stream is a cudaStream_t.
int trt_infer(void* contextHandle, void* inputDevPtr, void* outputDevPtr,
              int batchSize, void* stream) {
    if (!contextHandle) {
        set_error("null context handle");
        return -1;
    }
    auto* ctx = static_cast<nvinfer1::IExecutionContext*>(contextHandle);
    auto* engine = &ctx->getEngine();

    // Set input/output tensor addresses using the modern enqueueV3 API.
    // TensorRT 10 uses named tensors — iterate through I/O tensors.
    //
    // u2netp has 7 output tensors but we only need the first one. TensorRT
    // requires ALL outputs to be bound. We bind the first output to the
    // caller's buffer and all subsequent outputs to a shared scratch buffer.
    int numIO = engine->getNbIOTensors();

    // First pass: find the largest unused output tensor so we can allocate
    // a scratch buffer that's large enough for all of them (they share it).
    int outputIdx = 0;
    size_t maxExtraOutputBytes = 0;
    for (int i = 0; i < numIO; i++) {
        const char* name = engine->getIOTensorName(i);
        auto mode = engine->getTensorIOMode(name);
        if (mode == nvinfer1::TensorIOMode::kOUTPUT) {
            if (outputIdx > 0) {
                auto dims = engine->getTensorShape(name);
                int64_t vol = volume(dims);
                if (vol < 0) vol = -vol;
                size_t bytes = static_cast<size_t>(vol) * sizeof(float);
                if (bytes > maxExtraOutputBytes) {
                    maxExtraOutputBytes = bytes;
                }
            }
            outputIdx++;
        }
    }

    // Lazily allocate scratch for extra outputs. We look up the wrapper
    // through the engine pointer — the wrapper owns the scratch allocation
    // and it persists for the lifetime of the engine.
    // Note: trt_infer receives the context handle, not the engine wrapper.
    // We use a static thread-local scratch pointer for simplicity. Each
    // source has its own CUDA stream and calls from a single goroutine,
    // so thread-local is sufficient.
    static thread_local void* tl_scratch = nullptr;
    static thread_local size_t tl_scratch_bytes = 0;
    if (maxExtraOutputBytes > 0 && maxExtraOutputBytes > tl_scratch_bytes) {
        if (tl_scratch != nullptr) {
            cudaFree(tl_scratch);
        }
        cudaError_t err = cudaMalloc(&tl_scratch, maxExtraOutputBytes);
        if (err != cudaSuccess) {
            set_error("cudaMalloc for scratch output failed: %s", cudaGetErrorString(err));
            tl_scratch = nullptr;
            tl_scratch_bytes = 0;
            return -1;
        }
        tl_scratch_bytes = maxExtraOutputBytes;
    }

    // Second pass: bind all tensors.
    outputIdx = 0;
    for (int i = 0; i < numIO; i++) {
        const char* name = engine->getIOTensorName(i);
        auto mode = engine->getTensorIOMode(name);

        if (mode == nvinfer1::TensorIOMode::kINPUT) {
            // Set dynamic batch dimension if applicable.
            auto dims = engine->getTensorShape(name);
            if (dims.nbDims > 0 && dims.d[0] == -1) {
                dims.d[0] = batchSize;
                ctx->setInputShape(name, dims);
            }
            if (!ctx->setTensorAddress(name, inputDevPtr)) {
                set_error("setTensorAddress failed for input: %s", name);
                return -1;
            }
        } else if (mode == nvinfer1::TensorIOMode::kOUTPUT) {
            void* addr = (outputIdx == 0) ? outputDevPtr : tl_scratch;
            if (!ctx->setTensorAddress(name, addr)) {
                set_error("setTensorAddress failed for output: %s (idx %d)", name, outputIdx);
                return -1;
            }
            outputIdx++;
        }
    }

    cudaStream_t cudaStream = static_cast<cudaStream_t>(stream);
    if (!ctx->enqueueV3(cudaStream)) {
        set_error("enqueueV3 failed");
        return -1;
    }

    return 0;
}

// trt_get_input_size returns the total number of float elements for the first input tensor.
int trt_get_input_size(void* engineHandle) {
    if (!engineHandle) return 0;
    auto* wrapper = static_cast<TRTEngineWrapper*>(engineHandle);
    auto* engine = wrapper->engine.get();

    int numIO = engine->getNbIOTensors();
    for (int i = 0; i < numIO; i++) {
        const char* name = engine->getIOTensorName(i);
        if (engine->getTensorIOMode(name) == nvinfer1::TensorIOMode::kINPUT) {
            return static_cast<int>(volume(engine->getTensorShape(name)));
        }
    }
    return 0;
}

// trt_get_output_size returns the total number of float elements for the first output tensor.
int trt_get_output_size(void* engineHandle) {
    if (!engineHandle) return 0;
    auto* wrapper = static_cast<TRTEngineWrapper*>(engineHandle);
    auto* engine = wrapper->engine.get();

    int numIO = engine->getNbIOTensors();
    for (int i = 0; i < numIO; i++) {
        const char* name = engine->getIOTensorName(i);
        if (engine->getTensorIOMode(name) == nvinfer1::TensorIOMode::kOUTPUT) {
            return static_cast<int>(volume(engine->getTensorShape(name)));
        }
    }
    return 0;
}

// trt_destroy_context releases an execution context.
void trt_destroy_context(void* contextHandle) {
    if (!contextHandle) return;
    auto* ctx = static_cast<nvinfer1::IExecutionContext*>(contextHandle);
    delete ctx;
}

// trt_destroy_engine releases an engine and its runtime.
void trt_destroy_engine(void* engineHandle) {
    if (!engineHandle) return;
    auto* wrapper = static_cast<TRTEngineWrapper*>(engineHandle);
    delete wrapper;
}

// trt_get_last_error returns the last error message (thread-local).
const char* trt_get_last_error(void) {
    return trt_error_buf;
}

// trt_get_num_io returns the number of I/O tensors in the engine.
int trt_get_num_io(void* engineHandle) {
    if (!engineHandle) return 0;
    auto* wrapper = static_cast<TRTEngineWrapper*>(engineHandle);
    return wrapper->engine->getNbIOTensors();
}

// trt_get_tensor_info returns metadata about a tensor by index.
// name_buf receives the tensor name, is_input is 1 for input / 0 for output,
// dtype is the nvinfer1::DataType enum value, ndims/dims describe the shape.
int trt_get_tensor_info(void* engineHandle, int index,
                        char* name_buf, int name_buf_size,
                        int* is_input, int* dtype, int* ndims, int* dims) {
    if (!engineHandle) {
        set_error("null engine handle");
        return -1;
    }
    auto* wrapper = static_cast<TRTEngineWrapper*>(engineHandle);
    auto* engine = wrapper->engine.get();

    int numIO = engine->getNbIOTensors();
    if (index < 0 || index >= numIO) {
        set_error("tensor index %d out of range [0, %d)", index, numIO);
        return -1;
    }

    const char* name = engine->getIOTensorName(index);
    strncpy(name_buf, name, name_buf_size - 1);
    name_buf[name_buf_size - 1] = '\0';

    auto mode = engine->getTensorIOMode(name);
    *is_input = (mode == nvinfer1::TensorIOMode::kINPUT) ? 1 : 0;

    *dtype = static_cast<int>(engine->getTensorDataType(name));

    auto shape = engine->getTensorShape(name);
    *ndims = shape.nbDims;
    for (int d = 0; d < shape.nbDims && d < 8; d++) {
        dims[d] = shape.d[d];
    }

    return 0;
}

// trt_build_engine_v2 builds a TensorRT engine from ONNX with full dynamic
// dimension support. Unlike trt_build_engine which only handles dynamic batch
// (dim 0), this handles ALL dynamic dimensions. maxSeqLen sets the maximum
// for non-batch dynamic dims (e.g. sequence length in Whisper decoder).
int trt_build_engine_v2(const char* onnxPath, const char* planPath,
                        int maxBatch, int maxSeqLen, int useFP16, int useINT8) {
    auto builder = std::unique_ptr<nvinfer1::IBuilder>(
        nvinfer1::createInferBuilder(getTRTLogger()));
    if (!builder) {
        set_error("createInferBuilder failed");
        return -1;
    }

    auto network = std::unique_ptr<nvinfer1::INetworkDefinition>(
        builder->createNetworkV2(0));
    if (!network) {
        set_error("createNetworkV2 failed");
        return -1;
    }

    auto parser = std::unique_ptr<nvonnxparser::IParser>(
        nvonnxparser::createParser(*network, getTRTLogger()));
    if (!parser) {
        set_error("createParser failed");
        return -1;
    }

    if (!parser->parseFromFile(onnxPath,
            static_cast<int>(nvinfer1::ILogger::Severity::kWARNING))) {
        set_error("ONNX parse failed for: %s", onnxPath);
        return -1;
    }

    auto config = std::unique_ptr<nvinfer1::IBuilderConfig>(
        builder->createBuilderConfig());
    if (!config) {
        set_error("createBuilderConfig failed");
        return -1;
    }

    // 512 MB workspace (larger than v1 to accommodate multi-input models).
    config->setMemoryPoolLimit(nvinfer1::MemoryPoolType::kWORKSPACE, 512ULL << 20);

    if (useFP16 && builder->platformHasFastFp16()) {
        config->setFlag(nvinfer1::BuilderFlag::kFP16);
    }
    if (useINT8 && builder->platformHasFastInt8()) {
        config->setFlag(nvinfer1::BuilderFlag::kINT8);
    }

    auto profile = builder->createOptimizationProfile();
    if (!profile) {
        set_error("createOptimizationProfile failed");
        return -1;
    }

    // Set optimization profile for ALL dynamic dimensions, not just batch.
    int numInputs = network->getNbInputs();
    for (int i = 0; i < numInputs; i++) {
        auto input = network->getInput(i);
        auto dims = input->getDimensions();
        const char* inputName = input->getName();

        nvinfer1::Dims minDims = dims, optDims = dims, maxDims = dims;
        bool hasDynamic = false;

        for (int d = 0; d < dims.nbDims; d++) {
            if (dims.d[d] == -1) {
                hasDynamic = true;
                if (d == 0) {
                    // Batch dimension.
                    minDims.d[d] = 1;
                    optDims.d[d] = 1;
                    maxDims.d[d] = maxBatch;
                } else {
                    // Non-batch dynamic dimension (e.g. sequence length).
                    minDims.d[d] = 1;
                    optDims.d[d] = maxSeqLen > 0 ? (maxSeqLen / 2) : 64;
                    maxDims.d[d] = maxSeqLen > 0 ? maxSeqLen : 1500;
                }
            }
        }

        if (hasDynamic) {
            profile->setDimensions(inputName, nvinfer1::OptProfileSelector::kMIN, minDims);
            profile->setDimensions(inputName, nvinfer1::OptProfileSelector::kOPT, optDims);
            profile->setDimensions(inputName, nvinfer1::OptProfileSelector::kMAX, maxDims);
        }
    }
    config->addOptimizationProfile(profile);

    auto serialized = std::unique_ptr<nvinfer1::IHostMemory>(
        builder->buildSerializedNetwork(*network, *config));
    if (!serialized || serialized->size() == 0) {
        set_error("buildSerializedNetwork failed");
        return -1;
    }

    if (planPath && planPath[0] != '\0') {
        std::ofstream out(planPath, std::ios::binary);
        if (!out.is_open()) {
            set_error("cannot open plan path for writing: %s", planPath);
            return -1;
        }
        out.write(static_cast<const char*>(serialized->data()), serialized->size());
        if (!out.good()) {
            set_error("write to plan path failed: %s", planPath);
            return -1;
        }
    }

    return 0;
}

// TensorBinding describes a named tensor with its device pointer and shape.
// Used by trt_infer_multi for per-tensor binding.
typedef struct {
    const char* name;
    void* devPtr;
    int dims[8];
    int ndims;
} TensorBinding;

// trt_infer_multi runs inference with explicit per-tensor bindings.
// Each binding specifies a tensor name, device pointer, and shape.
// Input tensors have their shapes set via setInputShape().
// Unbound output tensors are automatically bound to a scratch buffer.
int trt_infer_multi(void* contextHandle,
                    TensorBinding* bindings, int numBindings,
                    void* stream) {
    if (!contextHandle) {
        set_error("null context handle");
        return -1;
    }
    auto* ctx = static_cast<nvinfer1::IExecutionContext*>(contextHandle);
    auto* engine = &ctx->getEngine();

    // Bind all provided tensors.
    for (int i = 0; i < numBindings; i++) {
        const char* name = bindings[i].name;

        auto mode = engine->getTensorIOMode(name);
        if (mode == nvinfer1::TensorIOMode::kINPUT) {
            nvinfer1::Dims shape;
            shape.nbDims = bindings[i].ndims;
            for (int d = 0; d < bindings[i].ndims && d < 8; d++) {
                shape.d[d] = bindings[i].dims[d];
            }
            if (!ctx->setInputShape(name, shape)) {
                set_error("setInputShape failed for: %s", name);
                return -1;
            }
        }

        if (!ctx->setTensorAddress(name, bindings[i].devPtr)) {
            set_error("setTensorAddress failed for: %s", name);
            return -1;
        }
    }

    // Bind any unbound output tensors to a scratch buffer.
    int numIO = engine->getNbIOTensors();
    static thread_local void* tl_scratch2 = nullptr;
    static thread_local size_t tl_scratch2_bytes = 0;

    for (int i = 0; i < numIO; i++) {
        const char* name = engine->getIOTensorName(i);
        auto mode = engine->getTensorIOMode(name);
        if (mode != nvinfer1::TensorIOMode::kOUTPUT) continue;

        // Check if this output was already bound by the caller.
        bool bound = false;
        for (int b = 0; b < numBindings; b++) {
            if (strcmp(bindings[b].name, name) == 0) {
                bound = true;
                break;
            }
        }
        if (bound) continue;

        auto dims = engine->getTensorShape(name);
        int64_t vol = volume(dims);
        if (vol < 0) vol = -vol;
        size_t bytes = static_cast<size_t>(vol) * sizeof(float);
        if (bytes > tl_scratch2_bytes) {
            if (tl_scratch2) cudaFree(tl_scratch2);
            if (cudaMalloc(&tl_scratch2, bytes) != cudaSuccess) {
                set_error("cudaMalloc scratch failed");
                tl_scratch2 = nullptr;
                tl_scratch2_bytes = 0;
                return -1;
            }
            tl_scratch2_bytes = bytes;
        }
        if (!ctx->setTensorAddress(name, tl_scratch2)) {
            set_error("setTensorAddress scratch failed for: %s", name);
            return -1;
        }
    }

    cudaStream_t cudaStream = static_cast<cudaStream_t>(stream);
    if (!ctx->enqueueV3(cudaStream)) {
        set_error("enqueueV3 failed");
        return -1;
    }

    return 0;
}

} // extern "C"
