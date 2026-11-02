package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog"
)

const (
	jobStopTimeout = 2 * time.Second
	resultError    = "error"
	resultOK       = "ok"
)

type Job struct {
	Interval time.Duration
	Name     string
	Run      func(context.Context) error
}

type Runner struct {
	log       zerolog.Logger
	runs      *prometheus.CounterVec
	scheduler gocron.Scheduler
}

func (r *Runner) Register(jobs ...Job) error {
	for _, job := range jobs {
		task := gocron.NewTask(func(ctx context.Context) { r.run(ctx, job) })

		_, err := r.scheduler.NewJob(
			gocron.DurationJob(job.Interval),
			task,
			gocron.WithName(job.Name),
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
			gocron.WithStartAt(gocron.WithStartImmediately()),
		)
		if err != nil {
			return fmt.Errorf("registering %s: %w", job.Name, err)
		}
	}

	return nil
}

func (r *Runner) Start() {
	r.scheduler.Start()
}

func (r *Runner) Stop() error {
	if err := r.scheduler.Shutdown(); err != nil {
		return fmt.Errorf("shutting down scheduler: %w", err)
	}

	return nil
}

func (r *Runner) run(ctx context.Context, job Job) {
	result := resultOK
	if err := job.Run(ctx); err != nil {
		result = resultError

		if ctx.Err() == nil {
			r.log.Warn().Err(err).Str("worker", job.Name).Msg("worker failed")
		}
	}

	r.runs.WithLabelValues(job.Name, result).Inc()
}

func New(log zerolog.Logger, reg prometheus.Registerer) (*Runner, error) {
	scheduler, err := gocron.NewScheduler(gocron.WithStopTimeout(jobStopTimeout))
	if err != nil {
		return nil, fmt.Errorf("creating scheduler: %w", err)
	}

	runs := promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Help: "Total number of background worker runs.",
		Name: "sentinel_dvpnd_worker_runs_total",
	}, []string{"worker", "result"})

	return &Runner{
		log:       log,
		runs:      runs,
		scheduler: scheduler,
	}, nil
}
