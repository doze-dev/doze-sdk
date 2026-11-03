package engine

import (
	"fmt"
	"runtime"
)

// HostPlatform returns the running host's platform descriptor.
func HostPlatform() (Platform, error) {
	triple, err := targetTriple(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Platform{}, err
	}
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Triple: triple}, nil
}

func targetTriple(goos, goarch string) (string, error) {
	switch goos {
	case "linux":
		switch goarch {
		case "amd64":
			return "x86_64-unknown-linux-gnu", nil
		case "arm64":
			return "aarch64-unknown-linux-gnu", nil
		}
	case "darwin":
		switch goarch {
		case "arm64":
			return "aarch64-apple-darwin", nil
		case "amd64":
			// Policy, not an accident — say so, or this reads as a packaging bug.
			return "", fmt.Errorf("Intel Macs (darwin/amd64) are not supported — doze runs on Apple Silicon Macs and x86-64/arm64 Linux; on an Intel Mac, use a Linux devcontainer or VM")
		}
	}
	return "", fmt.Errorf("unsupported platform %s/%s (doze supports Apple Silicon macOS and x86-64/arm64 Linux)", goos, goarch)
}
