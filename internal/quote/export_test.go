package quote

import (
	"testing"
	"time"
)

// SetNow replaces the clock until the test ends, for tests outside the package.
func SetNow(tb testing.TB, clock func() time.Time) {
	tb.Helper()
	orig := now
	now = clock
	tb.Cleanup(func() { now = orig })
}
