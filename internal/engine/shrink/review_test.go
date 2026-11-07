package shrink

import (
	"context"
	"slices"
	"testing"
)

// Ctrl+C before a disk starts (the CLI's loop goes on to the next disk after
// the signal context is cancelled): nothing looks at ctx before the stop, so
// the disk is stopped and compacted anyway.
func TestReviewCancelledBeforeStartStillCompacts(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Run(ctx, w.env, w.disk(), Options{}, nil)
	if slices.Contains(w.fake.CallList(), "compact:Dev") {
		t.Fatalf("a run started after Ctrl+C compacted the disk: err=%q calls=%v", r.Err, w.fake.CallList())
	}
}
