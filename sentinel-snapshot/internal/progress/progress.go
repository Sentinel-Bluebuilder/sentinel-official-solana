package progress

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type Tracker struct {
	log      *slog.Logger
	stage    string
	total    int64
	count    atomic.Int64
	start    time.Time
	stop     chan struct{}
	stopOnce sync.Once
}

func (p *Tracker) Add(n int64) { p.count.Add(n) }

func (p *Tracker) Count() int64 { return p.count.Load() }

func (p *Tracker) Stop() {
	p.stopOnce.Do(func() {
		close(p.stop)
		p.emit("stage complete")
	})
}

func (p *Tracker) emit(msg string) {
	done := p.count.Load()
	elapsed := time.Since(p.start).Seconds()

	rate := 0.0
	if elapsed > 0 {
		rate = float64(done) / elapsed
	}

	var eta time.Duration
	if rate > 0 && p.total > 0 {
		eta = time.Duration(float64(p.total-done)/rate) * time.Second
	}

	percent := 0.0
	if p.total > 0 {
		percent = 100 * float64(done) / float64(p.total)
	}

	p.log.Info(msg, "stage", p.stage, "done", done, "total", p.total,
		"percent", int(percent), "rate_per_second", int(rate), "eta", eta.String())
}

func (p *Tracker) loop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case <-ticker.C:
			p.emit("progress")
		}
	}
}

func New(ctx context.Context, log *slog.Logger, stage string, total int64) *Tracker {
	p := &Tracker{
		log:   log,
		stage: stage,
		total: total,
		start: time.Now(),
		stop:  make(chan struct{}),
	}
	go p.loop(ctx)

	return p
}
