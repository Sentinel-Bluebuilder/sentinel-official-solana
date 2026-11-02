package progress_test

import (
	"log/slog"
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/progress"
)

func TestTracker_Add_CountsAndStops(t *testing.T) {
	t.Parallel()

	p := progress.New(t.Context(), slog.Default(), "test", 10)
	for range 10 {
		p.Add(1)
	}

	p.Stop()

	if got := p.Count(); got != 10 {
		t.Fatalf("count=%d, want 10", got)
	}

	p.Stop()
}
