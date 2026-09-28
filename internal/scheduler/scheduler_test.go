package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type recordingStore struct {
	workerID string
	worked   bool
	err      error
}

func (s *recordingStore) AdmitDueReviewSchedule(_ context.Context, workerID string) (domain.ReviewSchedule, bool, error) {
	s.workerID = workerID
	return domain.ReviewSchedule{}, s.worked, s.err
}

func TestProcessorAdmitsAtMostOneDueSchedule(t *testing.T) {
	backend := &recordingStore{worked: true}
	active := 0
	processor := Processor{Store: backend, WorkerID: "scheduler-a", TaskStarted: func() func() {
		active++
		return func() { active-- }
	}}
	worked, err := processor.RunOnce(context.Background())
	if err != nil || !worked || backend.workerID != "scheduler-a" || active != 0 {
		t.Fatalf("worked=%t worker=%q active=%d err=%v", worked, backend.workerID, active, err)
	}
}

func TestProcessorReturnsStoreError(t *testing.T) {
	backend := &recordingStore{err: errors.New("database unavailable")}
	worked, err := (Processor{Store: backend, WorkerID: "scheduler-a"}).RunOnce(context.Background())
	if worked || err == nil {
		t.Fatalf("worked=%t err=%v", worked, err)
	}
}
