package gateway

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
)

func TestGenerationTimingLogsOnlyPhaseMetadata(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	timing := newGenerationTiming("public-model", accountdomain.ProviderBuild)
	timing.markSelection(10 * time.Millisecond)
	timing.markCredential(20 * time.Millisecond)
	timing.markUpstream(30 * time.Millisecond)
	timing.markUpstream(40 * time.Millisecond)
	body := &firstByteReadCloser{ReadCloser: io.NopCloser(strings.NewReader("ok")), mark: timing.markFirstBody}
	if _, err := io.ReadAll(body); err != nil {
		t.Fatal(err)
	}
	timing.finish(logger, "success")
	logged := output.String()
	for _, expected := range []string{"generation_timing", "route=public-model", "provider=grok_build", "selection_wait_ms=10", "credential_wait_ms=20", "upstream_wait_ms=70", "attempts=2", "retries=1"} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("log missing %q: %s", expected, logged)
		}
	}
}

func TestFirstTokenTimerMarksOnce(t *testing.T) {
	timer := newFirstTokenTimer(time.Now().Add(-25 * time.Millisecond))
	if timer.milliseconds() != nil {
		t.Fatal("unmarked timer returned a value")
	}
	timer.mark()
	first := timer.milliseconds()
	if first == nil || *first < 20 {
		t.Fatalf("first token milliseconds = %v", first)
	}
	time.Sleep(time.Millisecond)
	timer.mark()
	second := timer.milliseconds()
	if second == nil || *second != *first {
		t.Fatalf("timer changed after second mark: first=%v second=%v", first, second)
	}
}

func TestFirstTokenTimerMarkAtStampsObservedTime(t *testing.T) {
	started := time.Now().Add(-5 * time.Second)
	timer := newFirstTokenTimer(started)
	timer.markAt(started.Add(1200 * time.Millisecond))
	got := timer.milliseconds()
	if got == nil || *got != 1200 {
		t.Fatalf("markAt milliseconds = %v, want 1200", got)
	}
	// A later forward-time mark must not override the earlier evidence stamp.
	timer.mark()
	if got := timer.milliseconds(); got == nil || *got != 1200 {
		t.Fatalf("mark overrode markAt stamp: %v", got)
	}
	// Zero time never stamps.
	blank := newFirstTokenTimer(started)
	blank.markAt(time.Time{})
	if blank.milliseconds() != nil {
		t.Fatal("zero-time markAt stamped the timer")
	}
	// A timestamp before the request start clamps to zero instead of going negative.
	early := newFirstTokenTimer(started)
	early.markAt(started.Add(-time.Second))
	if got := early.milliseconds(); got == nil || *got != 0 {
		t.Fatalf("pre-start markAt milliseconds = %v, want 0", got)
	}
}
