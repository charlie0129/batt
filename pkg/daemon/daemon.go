package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/events"
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

func (d *Daemon) routes() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(ginLogger(logrus.StandardLogger()))
	router.GET("/config", d.getConfig)
	router.GET("/limit", d.getLimit)
	router.PUT("/limit", d.setLimit)
	router.PUT("/disable", d.setDisableFor)
	router.POST("/charge-once/limit", d.postChargeOnceToLimit)
	router.POST("/charge-once/full", d.postChargeOnceToFull)
	router.POST("/charge-once/cancel", d.postCancelChargeOnce)
	router.PUT("/lower-limit-delta", d.setLowerLimitDelta)
	router.PUT("/prevent-idle-sleep", d.setPreventIdleSleep)
	router.PUT("/disable-charging-pre-sleep", d.setDisableChargingPreSleep)
	router.PUT("/prevent-system-sleep", d.setPreventSystemSleep)
	router.PUT("/prevent-sleep-on-adapter-disable", d.setPreventSleepOnAdapterDisable)
	router.PUT("/adapter", d.setAdapter)
	router.PUT("/adapter/disable", d.setAdapterDisableFor)
	router.GET("/adapter", d.getAdapter)
	router.GET("/charging", d.getCharging)
	router.GET("/battery-info", d.getBatteryInfo)
	router.PUT("/magsafe-led", d.setControlMagSafeLED)
	router.PUT("/adapter-mode", d.setAdapterMode)
	router.GET("/current-charge", d.getCurrentCharge)
	router.GET("/plugged-in", d.getPluggedIn)
	router.GET("/charging-control-capable", d.getChargingControlCapable)
	router.GET("/compatibility", d.getCompatibility)
	router.GET("/version", d.getVersion)
	// Deprecated
	router.GET("/power-telemetry", d.getPowerTelemetry)
	router.GET("/telemetry", d.getUnifiedTelemetry)
	router.GET("/event", d.getEventStream)

	// Calibration endpoints (status folded into /telemetry)
	router.POST("/calibration/start", d.postStartCalibration)
	router.POST("/calibration/pause", d.postPauseCalibration)
	router.POST("/calibration/resume", d.postResumeCalibration)
	router.POST("/calibration/cancel", d.postCancelCalibration)
	router.PUT("/schedule", d.setSchedule)
	router.PUT("/schedule/postpone", d.postponeSchedule)
	router.PUT("/schedule/skip", d.skipSchedule)

	// Calibration settings endpoints
	router.PUT("/calibration/discharge-threshold", d.setCalibrationDischargeThreshold)
	router.PUT("/calibration/hold-duration", d.setCalibrationHoldDurationMinutes)

	return router
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
	logrus.Infof("removed stale unix socket %s left by a previous daemon", path)
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
	logrus.WithFields(conf.LogrusFields()).Infof("config loaded")

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
	logrus.WithFields(capabilityLogFields(caps)).Info("detected hardware capabilities")
	d.disableUnsupportedConfiguredFeatures()

	d.loadCalibrationState(filepath.Join(stateDir, "batt.state.json"))
	if err := d.holds.init(filepath.Join(stateDir, "batt.sleep.json")); err != nil {
		logrus.WithError(err).Warn("failed to initialize sleep disabled state")
	}
	d.disableUnsupportedCalibrationState()
	d.restoreCalibrationSleepAssertion()

	if err := d.ensureStartupSleepPolicy(); err != nil {
		return err
	}

	// Load persisted schedule from config
	if cronExpr := d.conf.Cron(); d.getCapabilities().Calibration && cronExpr != "" {
		if err := d.scheduler.Schedule(cronExpr); err != nil {
			logrus.WithError(err).Warn("failed to restore schedule from config")
		} else {
			d.scheduler.Start()
			logrus.WithField("cron", cronExpr).Info("restored schedule from config")
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
				logrus.WithError(err).Error("failed to reload config safely")
				continue
			}
			logrus.Infof("config reloaded")
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
		logrus.Fatal(err)
	}

	// Create the socket to listen on:
	l, err := net.Listen("unix", unixSocketPath)
	if err != nil {
		logrus.Fatal(err)
	}

	if d.conf.AllowNonRootAccess() || allowNonRoot {
		logrus.Infof("non-root access is allowed, chaning permissions of %s to 0777", unixSocketPath)
		err = os.Chmod(unixSocketPath, 0777)
		if err != nil {
			logrus.Fatal(err)
		}
	}

	// Serve HTTP on unix socket
	go func() {
		logrus.Infof("http server listening on %s", l.Addr().String())
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrus.Fatal(err)
		}
	}()

	// Always listen: the callbacks no-op unless batt actively controls charging,
	// and this lets a runtime adapter-mode toggle get working sleep hooks.
	go func() {
		if err := d.notifier.Listen(daemonSleepHandler{d}); err != nil {
			logrus.Errorf("failed to listen to system sleep notifications: %v", err)
			os.Exit(1)
		}
	}()

	loopCtx, stopLoop := context.WithCancel(context.Background())
	defer stopLoop()
	go func() {
		logrus.Debugln("main loop starts")
		d.runLoop(loopCtx)
	}()

	// Handle common process-killing signals, so we can gracefully shut down:
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	// Wait for a SIGINT or SIGTERM:
	sig := <-sigc
	logrus.Infof("caught signal \"%s\": shutting down.", sig)

	logrus.Info("gracefully shutting down http server")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = srv.Shutdown(ctx)
	if err != nil {
		logrus.Errorf("failed to gracefully shutdown http server, closing it immediately: %v", err)
		_ = srv.Close()
	}
	cancel()

	logrus.Info("stopping listening notifications")
	d.notifier.Stop()
	stopLoop()
	d.scheduler.Stop()

	nativeShutdown := d.getCapabilities().NativeExactTargets
	cleanupErr := d.shutdown()

	logrus.Info("exiting")
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
		logrus.Errorf("failed to remove PM assertion before exiting: %v", err)
	}
	if err := d.assertions.AllowCalibrationSleep(); err != nil {
		logrus.Errorf("failed to remove calibration sleep assertion before exiting: %v", err)
	}

	cleanupErr := d.releaseHardwareOnExit()

	logrus.Info("closing smc connection")
	if err := d.smc.Close(); err != nil {
		logrus.Errorf("failed to close smc connection: %v", err)
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
		logrus.WithError(err).Errorf("ignoring the unusable sleep snapshot %s; batt starts without it and cannot hold sleep until it is fixed or removed. If the Mac no longer sleeps, run `sudo pmset -a disablesleep 0`", snapshotErr.path)
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
		logrus.Errorf("failed to reset charge control before exiting: %v", err)
		cleanupErr = errors.Join(cleanupErr, err)
	}

	if err := d.shutdownAdapterAndSleep(); err != nil {
		logrus.Errorf("failed to restore adapter/sleep state before exiting: %v", err)
		cleanupErr = errors.Join(cleanupErr, err)
	}

	if err := d.releaseMagSafeLEDOnExit(); err != nil {
		logrus.Errorf("failed to return MagSafe LED control to the system before exiting: %v", err)
		cleanupErr = errors.Join(cleanupErr, err)
	}
	return cleanupErr
}

func (d *Daemon) shutdownAdapterAndSleep() error {
	if d.adapterSleepPolicyCapable() {
		if err := d.enableAdapter(); err != nil {
			logrus.Errorf("failed to re-enable adapter before exiting: %v", err)
			return err
		}
	}
	if err := d.holds.releaseAll(); err != nil {
		logrus.Errorf("failed to restore SleepDisabled before exiting: %v", err)
		return err
	}
	return nil
}
