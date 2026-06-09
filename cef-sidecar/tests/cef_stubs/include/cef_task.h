// Minimal CEF API stub for unit-testing code that posts tasks via
// CefPostTask without linking the real CEF framework. Posted tasks are
// executed inline on the calling thread, which is sufficient (and the most
// adversarial scheduling) for lifecycle/threading tests of ControlSocket.
#pragma once

#include <atomic>

typedef enum {
    TID_UI,
} CefThreadId;

class CefTask;

template <typename T>
class CefRefPtr {
public:
    CefRefPtr() : ptr_(nullptr) {}
    CefRefPtr(T* p) : ptr_(p) {  // NOLINT(google-explicit-constructor)
        if (ptr_) ptr_->AddRef();
    }
    CefRefPtr(const CefRefPtr& other) : ptr_(other.ptr_) {
        if (ptr_) ptr_->AddRef();
    }
    CefRefPtr& operator=(const CefRefPtr& other) {
        if (this != &other) {
            T* old = ptr_;
            ptr_ = other.ptr_;
            if (ptr_) ptr_->AddRef();
            if (old) old->Release();
        }
        return *this;
    }
    ~CefRefPtr() {
        if (ptr_) ptr_->Release();
    }
    T* get() const { return ptr_; }
    T* operator->() const { return ptr_; }

private:
    T* ptr_;
};

class CefTask {
public:
    virtual ~CefTask() = default;
    virtual void Execute() = 0;
    virtual void AddRef() const = 0;
    virtual bool Release() const = 0;
};

#define IMPLEMENT_REFCOUNTING(ClassName)                                     \
public:                                                                      \
    void AddRef() const override { ref_count_.fetch_add(1); }                \
    bool Release() const override {                                          \
        if (ref_count_.fetch_sub(1) == 1) {                                  \
            delete this;                                                     \
            return true;                                                     \
        }                                                                    \
        return false;                                                        \
    }                                                                        \
                                                                             \
private:                                                                     \
    mutable std::atomic<int> ref_count_ { 0 }

// Test stub: run the task synchronously on the posting thread.
inline bool CefPostTask(CefThreadId /*thread_id*/, CefRefPtr<CefTask> task) {
    if (task.get()) {
        task->Execute();
    }
    return true;
}
