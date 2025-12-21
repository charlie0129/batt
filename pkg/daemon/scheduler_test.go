package daemon

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

func TestCronParse(t *testing.T) {
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	schedule, err := parser.Parse("@every 10m")
	if err != nil {
		t.Fatalf("failed to parse cron expression: %v", err)
	}

	now := time.Now()
	next1 := schedule.Next(now)
	t.Logf("next1: %v", next1)
	next2 := schedule.Next(next1)
	t.Logf("next2: %v", next2)

	if !next2.After(next1) {
		t.Fatalf("expected next2 to be after next1, got next1=%v next2=%v", next1, next2)
	}
}

func TestSchedulerScheduleStatus(t *testing.T) {
	s := NewScheduler(func() error { return nil }, nil, nil, nil)

	if err := s.Schedule("@every 1m"); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	next, running := s.Status()
	if running {
		t.Fatalf("scheduler should not be running")
	}
	if next.IsZero() {
		t.Fatalf("next run should be set after scheduling")
	}
}

func TestSchedulerSkip(t *testing.T) {
	s := NewScheduler(func() error { return nil }, nil, nil, nil)
	if err := s.Schedule("@every 10m"); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	orig, _ := s.Status()
	if orig.IsZero() {
		t.Fatalf("expected next run after scheduling")
	}

	s.Start()
	defer s.Stop()

	s.Skip()
	skipped, _ := s.Status()
	if !skipped.After(orig) {
		t.Fatalf("expected skip to move schedule forward, got %v <= %v", skipped, orig)
	}
}

func TestSchedulerRunCycle(t *testing.T) {
	notifyCh := make(chan struct{}, 1)
	taskCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	var preChecks int32

	task := func() error {
		taskCh <- struct{}{}
		return nil
	}

	preCheck := func() error {
		atomic.AddInt32(&preChecks, 1)
		return nil
	}

	beforeRun := func(data any) {
		notifyCh <- struct{}{}
	}

	onError := func(data any) {
		if err, ok := data.(error); ok {
			errCh <- err
		}
	}

	s := NewScheduler(task, preCheck, beforeRun, onError)
	if err := s.Schedule("@every 1s"); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	s.mu.Lock()
	s.nextRun = time.Now().Add(50 * time.Millisecond)
	s.mu.Unlock()

	s.Start()
	defer s.Stop()

	select {
	case <-notifyCh:
	case <-time.After(time.Second):
		t.Fatalf("did not receive before-run notification in time")
	}

	select {
	case <-taskCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("task did not execute in time")
	}

	if atomic.LoadInt32(&preChecks) == 0 {
		t.Fatalf("precheck should have been executed")
	}

	select {
	case err := <-errCh:
		t.Fatalf("unexpected error callback: %v", err)
	default:
	}
}

func TestSchedulerPreCheckFailure(t *testing.T) {
	taskCh := make(chan struct{}, 1)
	errCh := make(chan error, 2)

	task := func() error {
		taskCh <- struct{}{}
		return nil
	}

	preCheck := func() error {
		return errors.New("boom")
	}

	onError := func(data any) {
		if err, ok := data.(error); ok {
			errCh <- err
		}
	}

	s := NewScheduler(task, preCheck, nil, onError)
	if err := s.Schedule("@every 1s"); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	forcedNext := time.Now().Add(50 * time.Millisecond)

	s.mu.Lock()
	s.nextRun = forcedNext
	s.mu.Unlock()

	s.Start()
	defer s.Stop()

	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatalf("expected error callback from failed precheck")
	}

	select {
	case <-taskCh:
		t.Fatalf("task should not execute when precheck fails")
	default:
	}

}
