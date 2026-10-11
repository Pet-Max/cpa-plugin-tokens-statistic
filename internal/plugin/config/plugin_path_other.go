//go:build !cgo || (!darwin && !linux)

package config

func loadedPluginPath() (string, bool) {
	return "", false
}
