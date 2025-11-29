package daemon

import (
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
)

const (
	leadDuration     = time.Minute * 5 // leadDuration is the duration before the scheduled time to ask for confirmation.
	preCheckMaxTimes = 30
	preCheckInterval = time.Second * 10
)

type NotifyFunc func(data any)

// TaskFunc represents a runnable task.
type TaskFunc func() error

type Scheduler struct {
	OnUpcoming NotifyFunc // called before running the task
	OnError    NotifyFunc // called on task error
	Task       TaskFunc   // task callback
	PreCheck   TaskFunc   // health / condition check callback

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

func NewScheduler(task, preCheck TaskFunc, onUpcoming, onError NotifyFunc) *Scheduler {
	if task == nil {
		panic("task function cannot be nil")
	}

	s := &Scheduler{
		OnUpcoming: onUpcoming,
		OnError:    onError,
		Task:       task,
		PreCheck:   preCheck,
		parser:     cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor),
		controlCh:  make(chan controlMsg, 4),
		stopCh:     make(chan struct{}),
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

	if !running {
		return fmt.Errorf("no active schedule to postpone")
	}

	pp := orig.Add(d).Truncate(time.Second)
	if pp.Compare(next) >= 0 {
		return fmt.Errorf("postpone duration too long")
	}

	s.trySendControl(ctrlPostpone, pp)
	return nil
}

// Skip skips the next scheduled run.
func (s *Scheduler) Skip() error {
	s.mu.Lock()
	if s.schedule == nil || s.nextRun.IsZero() {
		s.mu.Unlock()
		return fmt.Errorf("no active schedule to skip")
	}
	next := s.schedule.Next(s.nextRun)
	if !s.running {
		s.nextRun = next
		s.mu.Unlock()
		return nil
	}
	s.nextRun = next
	s.mu.Unlock()
	s.trySendControl(ctrlSkip, nil)
	return nil
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
		logrus.Debug("scheduler stopped")
	}()

	logrus.Debug("scheduler started")

	for {
		leading := true

		attempts := 0
		var precheckErr error

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
					logrus.Debugf("upcoming scheduled task at %s", nextRun.Format(time.DateTime))
					leading = false
					runWait := time.Until(nextRun)
					if runWait < 0 {
						runWait = 0
					}
					timer.Reset(runWait)
					s.sendNotify(nextRun)
					continue
				}

				logrus.Debugf("running scheduled task at %s", nextRun.Format(time.DateTime))

				if s.PreCheck != nil {
					if err := s.PreCheck(); err != nil {
						if precheckErr == nil || err.Error() != precheckErr.Error() {
							precheckErr = err
							s.sendError(fmt.Errorf("precheck failed: %v", err))
						}

						attempts++
						if attempts <= preCheckMaxTimes {
							logrus.Debugf("precheck failed (%d/%d): %v; retrying in %s", attempts, preCheckMaxTimes, err, preCheckInterval)
							timer.Reset(preCheckInterval)
							continue
						}

						timer.Stop()
						s.advanceNextRun()
						break
					}
				}

				timer.Stop()

				go func() {
					if err := s.Task(); err != nil {
						s.sendError(fmt.Errorf("task failed: %v", err))
					}
				}()
				s.advanceNextRun()
			case <-s.stopCh:
				timer.Stop()
				s.mu.Lock()
				s.running = false
				s.mu.Unlock()
				return
			case msg := <-s.controlCh: // internal control messages
				logrus.WithFields(logrus.Fields{
					"kind": msg.kind,
					"data": msg.data,
				}).Debug("received control msg")

				switch msg.kind {
				case ctrlRecalculate:
					timer.Stop()
					sh := msg.data.(cron.Schedule)
					s.mu.Lock()
					s.schedule = sh
					s.nextRun = sh.Next(time.Now())
					s.mu.Unlock()
				case ctrlPostpone: // only postpone current run
					pp := msg.data.(time.Time)
					timer.Reset(time.Until(pp))
					continue
				case ctrlSkip:
					timer.Stop()
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

func (s *Scheduler) sendNotify(runAt time.Time) {
	if s.OnUpcoming == nil {
		return
	}

	go s.OnUpcoming(runAt)
}

func (s *Scheduler) sendError(err error) {
	if s.OnError == nil {
		return
	}

	go s.OnError(err)
}

func (s *Scheduler) trySendControl(kind controlKind, data any) {
	select {
	case s.controlCh <- controlMsg{kind: kind, data: data}:
	default:
	}
}
