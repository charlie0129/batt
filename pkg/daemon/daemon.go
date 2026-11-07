package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/events"
	"github.com/charlie0129/batt/pkg/logging"
	"github.com/charlie0129/batt/pkg/powerui"
	"github.com/charlie0129/batt/pkg/smc"
)

// Daemon is one running batt daemon: its configuration, the system interfaces
// it drives, and all of its runtime state. New builds one, Run drives it.
//
// Nothing in this package is global, so tests build a Daemon per test against
// a simulated Mac and run in parallel.
type Daemon struct {
	conf config.Config

	smc        SMC
	native     powerui.Controller
	assertions SleepAssertions
	notifier   SleepNotifier
	battery    BatteryInfo

	hub       *events.EventHub
	scheduler *Scheduler

	// euid reports the effective user id. Native charge control needs root.
	euid func() int

	// chargeControlMu guards capabilities and charger, which are set as a
	// pair at startup and whenever adapter mode is toggled at runtime. It is a
	// leaf lock.
	chargeControlMu sync.RWMutex
	capabilities    compatibility.Capabilities
	charger         chargeSwitch

	// transitionMu serializes admission of calibration and temporary
	// charge/adapter disable operations so concurrent requests cannot both
	// pass their conflict checks before either operation persists its state.
	transitionMu sync.Mutex

	// loopMu serializes maintenance passes and guards the loop state below.
	loopMu                       sync.Mutex
	maintainedChargingInProgress bool
	// chargeControlStopped is set once shutdown has released charging. A
	// queued maintenance pass must not put the limit back before exit.
	chargeControlStopped bool
	// pendingNativeLimit is the native target macOS had accepted but not yet
	// applied at the last maintenance pass, or zero. Handlers read it without
	// waiting for a pass.
	pendingNativeLimit atomic.Int32

	// loopDelay holds the maintain loop back around sleep and wake. See
	// holdMaintainLoop.
	loopDelay               sync.WaitGroup
	loopInterval            time.Duration
	loopRecorder            *TimeSeriesRecorder
	continuousLoopThreshold time.Duration
	// preSleepLoopDelay is how long the loop pauses after charging was
	// disabled for sleep; postSleepLoopDelay how long after wake.
	preSleepLoopDelay  time.Duration
	postSleepLoopDelay time.Duration
	lastWakeTime       time.Time
	lastPrintTime      time.Time
	lastStatus         loopStatus

	calibrationMu         sync.Mutex
	calibrationState      *calibration.State
	calibrationStatePath  string
	lastCalibrationStatus calibrationStatus

	// adapterPolicyMu serializes adapter switches with the sleep policy that
	// protects them.
	adapterPolicyMu sync.Mutex
	holds           *sleepHolds
}

// New wires a daemon to conf and sys. It does not touch the hardware; Run and
// start do.
func New(conf config.Config, sys System) *Daemon {
	const loopInterval = 10 * time.Second
	d := &Daemon{
		conf:       conf,
		smc:        sys.SMC,
		native:     sys.NativeLimit,
		assertions: sys.Assertions,
		notifier:   sys.Notifier,
		battery:    sys.Battery,
		hub:        events.NewEventHub(),
		euid:       os.Geteuid,

		loopInterval:            loopInterval,
		loopRecorder:            NewTimeSeriesRecorder(60, loopInterval),
		continuousLoopThreshold: 1*time.Minute + 20*time.Second, // add 20s to be sure
		preSleepLoopDelay:       60 * time.Second,
		postSleepLoopDelay:      30 * time.Second,
		lastWakeTime:            time.Now(),

		calibrationState: &calibration.State{Phase: calibration.PhaseIdle},
		holds:            newSleepHolds(sys.Sleep),
	}
	// Default to the legacy charge keys until capabilities are detected.
	d.charger = chargeKeySwitch{d}
	d.scheduler = NewScheduler(d.runScheduledCalibration, d.scheduledCalibrationPreCheck, d.notifyScheduleUpcoming, d.notifyScheduleError)
	return d
}

// routes is the daemon's HTTP API.
func (d *Daemon) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /config", d.getConfig)
	mux.HandleFunc("GET /limit", d.getLimit)
	mux.HandleFunc("PUT /limit", d.setLimit)
	mux.HandleFunc("PUT /disable", d.setDisableFor)
	mux.HandleFunc("POST /charge-once/limit", d.postChargeOnceToLimit)
	mux.HandleFunc("POST /charge-once/full", d.postChargeOnceToFull)
	mux.HandleFunc("POST /charge-once/cancel", d.postCancelChargeOnce)
	mux.HandleFunc("PUT /lower-limit-delta", d.setLowerLimitDelta)
	mux.HandleFunc("PUT /prevent-idle-sleep", d.setPreventIdleSleep)
	mux.HandleFunc("PUT /disable-charging-pre-sleep", d.setDisableChargingPreSleep)
	mux.HandleFunc("PUT /prevent-system-sleep", d.setPreventSystemSleep)
	mux.HandleFunc("PUT /prevent-sleep-on-adapter-disable", d.setPreventSleepOnAdapterDisable)
	mux.HandleFunc("PUT /adapter", d.setAdapter)
	mux.HandleFunc("PUT /adapter/disable", d.setAdapterDisableFor)
	mux.HandleFunc("GET /adapter", d.getAdapter)
	mux.HandleFunc("GET /charging", d.getCharging)
	mux.HandleFunc("GET /battery-info", d.getBatteryInfo)
	mux.HandleFunc("PUT /magsafe-led", d.setControlMagSafeLED)
	mux.HandleFunc("PUT /adapter-mode", d.setAdapterMode)
	mux.HandleFunc("GET /current-charge", d.getCurrentCharge)
	mux.HandleFunc("GET /plugged-in", d.getPluggedIn)
	mux.HandleFunc("GET /charging-control-capable", d.getChargingControlCapable)
	mux.HandleFunc("GET /compatibility", d.getCompatibility)
	mux.HandleFunc("GET /version", d.getVersion)
	// Deprecated
	mux.HandleFunc("GET /power-telemetry", d.getPowerTelemetry)
	mux.HandleFunc("GET /telemetry", d.getUnifiedTelemetry)
	mux.HandleFunc("GET /event", d.getEventStream)

	// Calibration endpoints (status folded into /telemetry)
	mux.HandleFunc("POST /calibration/start", d.postStartCalibration)
	mux.HandleFunc("POST /calibration/pause", d.postPauseCalibration)
	mux.HandleFunc("POST /calibration/resume", d.postResumeCalibration)
	mux.HandleFunc("POST /calibration/cancel", d.postCancelCalibration)
	mux.HandleFunc("PUT /schedule", d.setSchedule)
	mux.HandleFunc("PUT /schedule/postpone", d.postponeSchedule)
	mux.HandleFunc("PUT /schedule/skip", d.skipSchedule)

	// Calibration settings endpoints
	mux.HandleFunc("PUT /calibration/discharge-threshold", d.setCalibrationDischargeThreshold)
	mux.HandleFunc("PUT /calibration/hold-duration", d.setCalibrationHoldDurationMinutes)

	return accessLog(recoverPanics(mux))
}

// removeStaleSocket removes a unix socket file left behind by a daemon that was
// killed uncleanly, so a launchd-restarted daemon can bind again. It refuses to
// remove a socket that another daemon is still actively listening on.
func removeStaleSocket(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat socket %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket; refusing to remove", path)
	}
	if c, derr := net.DialTimeout("unix", path, 500*time.Millisecond); derr == nil {
		_ = c.Close()
		return fmt.Errorf("another batt daemon is already listening on %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", path, err)
	}
	slog.Info(fmt.Sprintf("removed stale unix socket %s left by a previous daemon", path))
	return nil
}

// stateDir is where the daemon keeps its runtime state files, next to the
// config file.
func stateDir(configPath string) string {
	if configPath == "" {
		return "/etc"
	}
	return filepath.Dir(configPath)
}

// Run loads the config, opens the Apple SMC and runs a daemon on the real
// hardware until SIGINT or SIGTERM.
func Run(configPath string, unixSocketPath string, allowNonRoot bool) error {
	conf, err := config.NewFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to parse config during startup: %w", err)
	}
	slog.Info("config loaded", "config", conf)

	// Open Apple SMC and detect the charge-control mechanism from key
	// presence before starting any loop, listener, scheduler, or API server.
	conn := smc.New()
	if err := conn.Open(); err != nil {
		return fmt.Errorf("open Apple SMC: %w", err)
	}

	d := New(conf, darwinSystem(conn))
	return d.Run(stateDir(configPath), unixSocketPath, allowNonRoot)
}

// start detects the hardware, reconciles persisted state with it and restores
// what a previous run left behind. It runs before any loop, listener or API
// server, so a failure leaves the hardware untouched.
func (d *Daemon) start(stateDir string) error {
	caps := d.detectCapabilities()
	if caps.NativeExactTargets && d.euid() != 0 {
		return fmt.Errorf("native macOS 27 charge control requires the root batt daemon")
	}
	d.setChargeControl(caps)
	slog.Info("detected hardware capabilities", capabilityLogAttrs(caps)...)
	d.disableUnsupportedConfiguredFeatures()

	d.loadCalibrationState(filepath.Join(stateDir, "batt.state.json"))
	if err := d.holds.init(filepath.Join(stateDir, "batt.sleep.json")); err != nil {
		slog.Warn("failed to initialize sleep disabled state", "error", err)
	}
	d.disableUnsupportedCalibrationState()
	d.restoreCalibrationSleepAssertion()

	if err := d.ensureStartupSleepPolicy(); err != nil {
		return err
	}

	// Load persisted schedule from config
	if cronExpr := d.conf.Cron(); d.getCapabilities().Calibration && cronExpr != "" {
		if err := d.scheduler.Schedule(cronExpr); err != nil {
			slog.Warn("failed to restore schedule from config", "error", err)
		} else {
			d.scheduler.Start()
			slog.Info("restored schedule from config", "cron", cronExpr)
		}
	}
	return nil
}

// Run starts the daemon and blocks until SIGINT or SIGTERM, then hands the
// hardware back to macOS.
func (d *Daemon) Run(stateDir string, unixSocketPath string, allowNonRoot bool) error {
	if err := d.start(stateDir); err != nil {
		_ = d.smc.Close()
		return err
	}
	defer d.scheduler.Stop()

	// Receive SIGHUP to reload config
	go func() {
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, syscall.SIGHUP)
		for range sigc {
			if err := d.reload(); err != nil {
				slog.Error("failed to reload config safely", "error", err)
				continue
			}
			slog.Info("config reloaded")
		}
	}()

	srv := &http.Server{
		Handler: d.routes(),
	}

	// A daemon killed uncleanly (SIGKILL, panic, power loss) leaves its unix
	// socket file behind. bind() then fails with EADDRINUSE and, because
	// launchd keeps restarting us, traps the daemon in a crash loop that never
	// recovers — leaving the charge limit unenforced (and, in adapter mode, the
	// adapter cut and the Mac on battery). launchd runs a single instance, so
	// no live daemon owns the path here; remove a stale socket before binding.
	if err := removeStaleSocket(unixSocketPath); err != nil {
		_ = d.smc.Close()
		return err
	}

	// Create the socket to listen on:
	l, err := net.Listen("unix", unixSocketPath)
	if err != nil {
		_ = d.smc.Close()
		return fmt.Errorf("listen on %s: %w", unixSocketPath, err)
	}

	if d.conf.AllowNonRootAccess() || allowNonRoot {
		slog.Info(fmt.Sprintf("non-root access is allowed, chaning permissions of %s to 0777", unixSocketPath))
		if err := os.Chmod(unixSocketPath, 0777); err != nil {
			_ = l.Close()
			_ = d.smc.Close()
			return fmt.Errorf("chmod %s: %w", unixSocketPath, err)
		}
	}

	// Serve HTTP on unix socket
	go func() {
		slog.Info(fmt.Sprintf("http server listening on %s", l.Addr().String()))
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.Fatal("http server failed", "error", err)
		}
	}()

	// Always listen: the callbacks no-op unless batt actively controls charging,
	// and this lets a runtime adapter-mode toggle get working sleep hooks.
	go func() {
		if err := d.notifier.Listen(daemonSleepHandler{d}); err != nil {
			slog.Error("failed to listen to system sleep notifications", "error", err)
			os.Exit(1)
		}
	}()

	loopCtx, stopLoop := context.WithCancel(context.Background())
	defer stopLoop()
	go func() {
		slog.Debug("main loop starts")
		d.runLoop(loopCtx)
	}()

	// Handle common process-killing signals, so we can gracefully shut down:
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	// Wait for a SIGINT or SIGTERM:
	sig := <-sigc
	slog.Info(fmt.Sprintf("caught signal \"%s\": shutting down.", sig))

	slog.Info("gracefully shutting down http server")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = srv.Shutdown(ctx)
	if err != nil {
		slog.Error("failed to gracefully shutdown http server, closing it immediately", "error", err)
		_ = srv.Close()
	}
	cancel()

	slog.Info("stopping listening notifications")
	d.notifier.Stop()
	stopLoop()
	d.scheduler.Stop()

	nativeShutdown := d.getCapabilities().NativeExactTargets
	cleanupErr := d.shutdown()

	slog.Info("exiting")
	if nativeShutdown {
		return cleanupErr
	}
	return nil
}

// reload re-reads the config file, as on SIGHUP, and applies the parts of it
// that take effect without a restart.
func (d *Daemon) reload() error {
	d.transitionMu.Lock()
	defer d.transitionMu.Unlock()
	if err := d.conf.Load(); err != nil {
		return err
	}
	d.disableUnsupportedConfiguredFeatures()
	return d.reconcileReloadedSleepPolicy()
}

// shutdown hands charging, the adapter, sleep and the MagSafe LED back to
// macOS and closes the SMC connection. It is the last thing Run does.
func (d *Daemon) shutdown() error {
	if err := d.assertions.AllowSleepOnAC(); err != nil {
		slog.Error("failed to remove PM assertion before exiting", "error", err)
	}
	if err := d.assertions.AllowCalibrationSleep(); err != nil {
		slog.Error("failed to remove calibration sleep assertion before exiting", "error", err)
	}

	cleanupErr := d.releaseHardwareOnExit()

	slog.Info("closing smc connection")
	if err := d.smc.Close(); err != nil {
		slog.Error("failed to close smc connection", "error", err)
		cleanupErr = errors.Join(cleanupErr, err)
	}
	return cleanupErr
}

func (d *Daemon) reconcileReloadedSleepPolicy() error {
	if !d.adapterSleepPolicyCapable() {
		return nil
	}
	if err := d.reconcileAdapterSleepPolicy(); err != nil {
		return d.restoreAdapterAfterPolicyError(err)
	}
	return nil
}

// ensureStartupSleepPolicy reconciles the sleep hold with the adapter state
// before the daemon serves requests. An unusable snapshot file does not stop the
// daemon: the charge limit does not depend on it. The file stays in place, no
// hold can be taken while it exists, and wall power is already restored when
// protection was missing. Every other failure stops the start.
func (d *Daemon) ensureStartupSleepPolicy() error {
	err := d.reconcileStartupSleepPolicy()
	var snapshotErr *sleepSnapshotError
	if errors.As(err, &snapshotErr) {
		slog.Error(fmt.Sprintf("ignoring the unusable sleep snapshot %s; batt starts without it and cannot hold sleep until it is fixed or removed. If the Mac no longer sleeps, run `sudo pmset -a disablesleep 0`", snapshotErr.path), "error", err)
		return nil
	}
	return err
}

func (d *Daemon) reconcileStartupSleepPolicy() error {
	if d.adapterSleepPolicyCapable() {
		if err := d.reconcileAdapterSleepPolicy(); err != nil {
			return fmt.Errorf("startup: %w", d.restoreAdapterAfterPolicyError(err))
		}
		return nil
	}
	if err := d.holds.restorePending(); err != nil {
		return fmt.Errorf("failed to restore pending sleep-disabled state during startup: %w", err)
	}
	return nil
}

// releaseHardwareOnExit hands charging, the adapter, sleep and the MagSafe
// LED back to macOS before the SMC connection is closed.
func (d *Daemon) releaseHardwareOnExit() error {
	var cleanupErr error
	if err := d.releaseChargeControlOnExit(); err != nil {
		slog.Error("failed to reset charge control before exiting", "error", err)
		cleanupErr = errors.Join(cleanupErr, err)
	}

	if err := d.shutdownAdapterAndSleep(); err != nil {
		slog.Error("failed to restore adapter/sleep state before exiting", "error", err)
		cleanupErr = errors.Join(cleanupErr, err)
	}

	if err := d.releaseMagSafeLEDOnExit(); err != nil {
		slog.Error("failed to return MagSafe LED control to the system before exiting", "error", err)
		cleanupErr = errors.Join(cleanupErr, err)
	}
	return cleanupErr
}

func (d *Daemon) shutdownAdapterAndSleep() error {
	if d.adapterSleepPolicyCapable() {
		if err := d.enableAdapter(); err != nil {
			slog.Error("failed to re-enable adapter before exiting", "error", err)
			return err
		}
	}
	if err := d.holds.releaseAll(); err != nil {
		slog.Error("failed to restore SleepDisabled before exiting", "error", err)
		return err
	}
	return nil
}
