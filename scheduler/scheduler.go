// Package scheduler provides a cron-based scheduler that periodically runs
// the Collector to keep the movie database up to date.
package scheduler

import (
	"log"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"my-movie-site/collector"
)

// Scheduler wraps a cron runner and the configuration needed to create
// Collector instances on each invocation.
type Scheduler struct {
	c      *cron.Cron
	apiURL string
	db     *gorm.DB
	// IncrementalHours is the look-back window passed to incremental runs.
	// Defaults to 24 (hours).
	IncrementalHours int
}

// New creates a Scheduler that is ready to be started.
func New(apiURL string, db *gorm.DB) *Scheduler {
	return &Scheduler{
		c:                cron.New(),
		apiURL:           apiURL,
		db:               db,
		IncrementalHours: 24,
	}
}

// Start registers the cron job and starts the scheduler in the background.
// The schedule expression follows standard 5-field cron syntax.
// For example "0 */6 * * *" runs at minute 0 of every 6th hour.
func (s *Scheduler) Start(schedule string) error {
	_, err := s.c.AddFunc(schedule, s.runIncremental)
	if err != nil {
		return err
	}
	s.c.Start()
	log.Printf("scheduler: started with schedule %q (incremental window: %d h)", schedule, s.IncrementalHours)
	return nil
}

// Stop gracefully shuts down the scheduler, waiting for any running job to
// finish.
func (s *Scheduler) Stop() {
	ctx := s.c.Stop()
	<-ctx.Done()
	log.Print("scheduler: stopped")
}

// runIncremental is the function executed on each cron tick.
func (s *Scheduler) runIncremental() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("scheduler: recovered from panic: %v", r)
		}
	}()
	start := time.Now()
	log.Printf("scheduler: incremental collection started at %s", start.Format(time.RFC3339))

	col := collector.NewIncremental(s.apiURL, s.db, s.IncrementalHours)
	if err := col.Run(); err != nil {
		log.Printf("scheduler: incremental collection failed after %s: %v", time.Since(start).Round(time.Millisecond), err)
		return
	}

	log.Printf("scheduler: incremental collection finished in %s", time.Since(start).Round(time.Millisecond))
}
