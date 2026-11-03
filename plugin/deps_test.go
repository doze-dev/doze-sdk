package plugin

import (
	"os/exec"
	"strings"
	"testing"
)

// The author kit never downloads or caches engine binaries: the host does
// (fetch.go). A kit package that imports binaries would put that back in
// every module.
func TestTheKitDoesNotFetch(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../plugin", "../enginetest", "../modtool", "../console", "../engine").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "github.com/doze-dev/doze-sdk/binaries" {
			t.Fatal("a kit package imports binaries")
		}
	}
}
