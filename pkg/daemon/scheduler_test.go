package daemon

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerScheduleStatus(t *testing.T) {
	s := NewScheduler(func() error { return nil }, nil, nil, nil)

	if err := s.Schedule("@every 1s"); err != nil {
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

func TestSchedulerPostponeAndSkip(t *testing.T) {
	s := NewScheduler(func() error { return nil }, nil, nil, nil)
	if err := s.Schedule("@every 2s"); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	orig, _ := s.Status()
	if orig.IsZero() {
		t.Fatalf("expected next run after scheduling")
	}

	if err := s.Postpone(time.Second); err != nil {
		t.Fatalf("Postpone returned error: %v", err)
	}

	postponed, _ := s.Status()
	if !postponed.After(orig) {
		t.Fatalf("expected postponed time after original, got %v <= %v", postponed, orig)
	}

	s.Skip()
	skipped, _ := s.Status()
	if !skipped.After(postponed) {
		t.Fatalf("expected skip to move schedule forward, got %v <= %v", skipped, postponed)
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

	initialNext, _ := s.Status()

	s.mu.Lock()
	s.nextRun = time.Now().Add(50 * time.Millisecond)
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

	time.Sleep(10 * time.Millisecond)
	nextAfterFail, _ := s.Status()
	if !nextAfterFail.After(initialNext) {
		t.Fatalf("expected next run to advance after failure, before=%v after=%v", initialNext, nextAfterFail)
	}
}
