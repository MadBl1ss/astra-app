package legacyfind

import (
	"context"
	"testing"
)

// What the real schtasks prints is read: on the development machine the old
// setup's task exists, elsewhere the list is empty.
func TestRealTaskIsRead(t *testing.T) {
	t.Logf("task folders: %q", taskDirs(context.Background()))
}
