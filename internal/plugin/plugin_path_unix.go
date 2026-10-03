//go:build cgo && (darwin || linux)

package plugin

/*
#cgo linux LDFLAGS: -ldl
#define _GNU_SOURCE
#include <dlfcn.h>

static const char* tokens_statistic_module_path(void) {
	Dl_info info;
	if (dladdr((void*)&tokens_statistic_module_path, &info) == 0 || info.dli_fname == NULL) {
		return NULL;
	}
	return info.dli_fname;
}
*/
import "C"

func loadedPluginPath() (string, bool) {
	path := C.tokens_statistic_module_path()
	if path == nil {
		return "", false
	}
	return C.GoString(path), true
}
