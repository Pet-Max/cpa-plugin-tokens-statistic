//go:build cgo

#include <stddef.h>
#include <stdint.h>

typedef struct {
    void* ptr;
    size_t len;
} cliproxy_buffer;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
    uint32_t abi_version;
    void* host_ctx;
    cliproxy_host_call_fn call;
    cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

int cliproxy_plugin_call_bridge(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    return cliproxyPluginCall((char*)method, (uint8_t*)request, request_len, response);
}

int cliproxy_host_call_bridge(cliproxy_host_api* host, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    if (host == NULL || host->call == NULL) {
        return 1;
    }
    return host->call(host->host_ctx, method, request, request_len, response);
}

void cliproxy_host_free_bridge(cliproxy_host_api* host, void* ptr, size_t len) {
    if (host != NULL && host->free_buffer != NULL && ptr != NULL) {
        host->free_buffer(ptr, len);
    }
}
