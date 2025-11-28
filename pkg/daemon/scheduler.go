package daemon

import (
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// leadDuration is the duration before the scheduled time to ask for confirmation.
const leadDuration = time.Minute * 5

type NotifyFunc func(data any)

// TaskFunc represents a runnable task.
type TaskFunc func() error

type Scheduler struct {
	BeforeRun NotifyFunc // called before running the task
	Error     NotifyFunc // called on task error
	Task      TaskFunc   // task callback
	PreCheck  TaskFunc   // health / condition check callback

	parser cron.Parser

	schedule cron.Schedule
	nextRun  time.Time

	mu      sync.Mutex
	running bool

	controlCh chan controlMsg
	stopCh    chan struct{}
}

// internal control kinds (not user visible events)
type controlKind int

const (
	ctrlRecalculate controlKind = iota // timer needs recalculation due to schedule change
	ctrlPostpone                       // next run postponed
	ctrlSkip                           // next run skipped
)

type controlMsg struct {
	kind controlKind
	data any
}

func NewScheduler(task, preCheck TaskFunc, beforeRun, error NotifyFunc) *Scheduler {
	if task == nil {
		panic("task function cannot be nil")
	}

	s := &Scheduler{
		BeforeRun: beforeRun,
		Error:     error,
		Task:      task,
		PreCheck:  preCheck,
		parser:    cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor),
		controlCh: make(chan controlMsg, 4),
		stopCh:    make(chan struct{}),
	}
	return s
}

func (s *Scheduler) Stop() {
	select {
	case <-s.stopCh: // already closed
	default:
		close(s.stopCh)
	}
}

func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	go s.runScheduled()
}

func (s *Scheduler) Schedule(cronExpr string) error {
	sh, err := s.parser.Parse(cronExpr)
	if err != nil {
		return err
	}

	s.mu.Lock()
	running := s.running
	if !running {
		s.schedule = sh
		s.nextRun = sh.Next(time.Now())
	}
	s.mu.Unlock()

	if running {
		s.trySendControl(ctrlRecalculate, sh)
	}
	return nil
}

// Postpone postpones the next scheduled run by the given duration.
func (s *Scheduler) Postpone(d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("postpone duration must be positive")
	}

	s.mu.Lock()
	if s.schedule == nil || s.nextRun.IsZero() {
		s.mu.Unlock()
		return fmt.Errorf("no active schedule to postpone")
	}
	orig := s.nextRun
	next := s.schedule.Next(orig).Truncate(time.Second)
	running := s.running
	s.mu.Unlock()

	pp := orig.Add(d).Truncate(time.Second)
	if pp.Compare(next) >= 0 {
		return fmt.Errorf("postpone duration too long")
	}

	if !running {
		s.mu.Lock()
		if s.nextRun.Equal(orig) {
			s.nextRun = pp
		}
		s.mu.Unlock()
		return nil
	}
	s.trySendControl(ctrlPostpone, pp)
	return nil
}

// Skip skips the next scheduled run.
func (s *Scheduler) Skip() {
	s.mu.Lock()
	if s.schedule == nil || s.nextRun.IsZero() {
		s.mu.Unlock()
		return
	}
	if !s.running {
		s.nextRun = s.schedule.Next(s.nextRun)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.trySendControl(ctrlSkip, nil)
}

func (s *Scheduler) Status() (nextRun time.Time, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nextRun = s.nextRun
	running = s.running
	return
}

func (s *Scheduler) runScheduled() {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	for {
		leading := true
		schedule, nextRun := s.snapshot()
		var timer *time.Timer
		if schedule == nil || nextRun.IsZero() {
			timer = time.NewTimer(time.Hour * 10000)
		} else {
			wait := time.Until(nextRun) - leadDuration
			if wait < 0 {
				wait = 0
			}
			timer = time.NewTimer(wait)
		}

		for {
			select {
			case <-timer.C:
				if schedule == nil || nextRun.IsZero() {
					break
				}

				if leading {
					leading = false
					runWait := time.Until(nextRun)
					if runWait < 0 {
						runWait = 0
					}
					timer.Reset(runWait)
					s.sendNotify(nextRun)
					continue
				}

				timer.Stop()
				if s.PreCheck != nil {
					if err := s.PreCheck(); err != nil {
						s.sendError(fmt.Errorf("precheck failed: %v", err))
						s.advanceNextRun()
						break
					}
				}

				go s.Task()
				s.advanceNextRun()
			case <-s.stopCh:
				timer.Stop()
				return
			case msg := <-s.controlCh: // internal control messages
				timer.Stop()

				switch msg.kind {
				case ctrlRecalculate:
					sh := msg.data.(cron.Schedule)
					s.mu.Lock()
					s.schedule = sh
					s.nextRun = sh.Next(time.Now())
					s.mu.Unlock()
				case ctrlPostpone:
					pp := msg.data.(time.Time)
					s.mu.Lock()
					s.nextRun = pp
					s.mu.Unlock()
				case ctrlSkip:
					s.mu.Lock()
					if s.schedule != nil && !s.nextRun.IsZero() {
						s.nextRun = s.schedule.Next(s.nextRun)
					}
					s.mu.Unlock()
				}
			}

			break
		}
	}
}

func (s *Scheduler) snapshot() (cron.Schedule, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.schedule, s.nextRun
}

func (s *Scheduler) advanceNextRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schedule == nil {
		return
	}
	s.nextRun = s.schedule.Next(s.nextRun)
}

func (s *Scheduler) sendNotify(data any) {
	if s.BeforeRun == nil {
		return
	}

	go s.BeforeRun(data)
}

func (s *Scheduler) sendError(err error) {
	if s.Error == nil {
		return
	}

	go s.Error(err)
}

func (s *Scheduler) trySendControl(kind controlKind, data any) {
	select {
	case s.controlCh <- controlMsg{kind: kind, data: data}:
	default:
	}
}
