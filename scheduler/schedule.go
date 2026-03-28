package scheduler

import (
	"fmt"
	"time"

	"squadron/config"

	"github.com/robfig/cron/v3"
)

// NextFireFunc computes the next fire time after the given time.
type NextFireFunc func(after time.Time) time.Time

// ParseSchedule converts a config.Schedule into a NextFireFunc by compiling
// it to a cron expression and using robfig/cron for next-time calculation.
func ParseSchedule(sched *config.Schedule) (NextFireFunc, error) {
	loc := time.Local
	if sched.Timezone != "" {
		var err error
		loc, err = time.LoadLocation(sched.Timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone %q: %w", sched.Timezone, err)
		}
	}

	expr := sched.ToCron()
	return parseCronExpr(expr, loc)
}

func parseCronExpr(expr string, loc *time.Location) (NextFireFunc, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	cronSched, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return func(after time.Time) time.Time {
		return cronSched.Next(after.In(loc))
	}, nil
}
