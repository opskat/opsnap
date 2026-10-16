package scheduler

import (
	"errors"
	"sort"

	"github.com/opskat/opsnap/internal/service/run_svc"
)

func isAlreadyRunning(err error) bool { return errors.Is(err, run_svc.ErrAlreadyRunning) }

// sortPlanned 按时间升序，同一时刻按任务 ID
func sortPlanned(p []PlannedRun) {
	sort.SliceStable(p, func(i, j int) bool {
		if !p[i].At.Equal(p[j].At) {
			return p[i].At.Before(p[j].At)
		}
		return p[i].JobID < p[j].JobID
	})
}
