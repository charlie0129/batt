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
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/events"
	"github.com/charlie0129/batt/pkg/smc"
)

var (
	smcConn      *smc.AppleSMC
	conf         config.Config
	capabilities compatibility.Capabilities

	sseHub    *events.EventHub // global hub instance initialized in Run()
	scheduler *Scheduler
)

func setupRoutes() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	// TODO: unify these ugly handlers

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(ginLogger(logrus.StandardLogger()))
	router.GET("/config", getConfig)
	router.GET("/limit", getLimit)
	router.PUT("/limit", setLimit)
	router.PUT("/disable", setDisableFor)
	router.POST("/charge-once/limit", postChargeOnceToLimit)
	router.POST("/charge-once/full", postChargeOnceToFull)
	router.POST("/charge-once/cancel", postCancelChargeOnce)
	router.PUT("/lower-limit-delta", setLowerLimitDelta)
	router.PUT("/prevent-idle-sleep", setPreventIdleSleep)
	router.PUT("/disable-charging-pre-sleep", setDisableChargingPreSleep)
	router.PUT("/prevent-system-sleep", setPreventSystemSleep)
	router.PUT("/prevent-sleep-on-adapter-disable", setPreventSleepOnAdapterDisable)
	router.PUT("/adapter", setAdapter)
	router.PUT("/adapter/disable", setAdapterDisableFor)
	router.GET("/adapter", getAdapter)
	router.GET("/charging", getCharging)
	router.GET("/battery-info", getBatteryInfo)
	router.PUT("/magsafe-led", setControlMagSafeLED)
	router.PUT("/adapter-mode", setAdapterMode)
	router.GET("/current-charge", getCurrentCharge)
	router.GET("/plugged-in", getPluggedIn)
	router.GET("/charging-control-capable", getChargingControlCapable)
	router.GET("/compatibility", getCompatibility)
	router.GET("/version", getVersion)
	// Deprecated
	router.GET("/power-telemetry", getPowerTelemetry)
	router.GET("/telemetry", getUnifiedTelemetry)
	router.GET("/event", getEventStream)

	// Calibration endpoints (status folded into /telemetry)
	router.POST("/calibration/start", postStartCalibration)
	router.POST("/calibration/pause", postPauseCalibration)
	router.POST("/calibration/resume", postResumeCalibration)
	router.POST("/calibration/cancel", postCancelCalibration)
	router.PUT("/schedule", setSchedule)
	router.PUT("/schedule/postpone", postponeSchedule)
	router.PUT("/schedule/skip", skipSchedule)

	// Calibration settings endpoints
	router.PUT("/calibration/discharge-threshold", setCalibrationDischargeThreshold)
	router.PUT("/calibration/hold-duration", setCalibrationHoldDurationMinutes)

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

func Run(configPath string, unixSocketPath string, allowNonRoot bool) error {
	var err error
	conf, err = config.NewFile(configPath)
	if err != nil {
		logrus.Fatalf("failed to parse config during startup: %v", err)
	}
	logrus.WithFields(conf.LogrusFields()).Infof("config loaded")

	// Open Apple SMC and detect the charge-control mechanism from key
	// presence before starting any loop, listener, scheduler, or API server.
	smcConn = smc.New()
	if err := smcConn.Open(); err != nil {
		return fmt.Errorf("open Apple SMC: %w", err)
	}
	caps := detectCapabilities()
	setChargeControl(caps)
	logrus.WithFields(capabilityLogFields(caps)).Info("detected hardware capabilities")
	disableUnsupportedConfiguredFeatures()

	initRuntimeStates(configPath)
	disableUnsupportedCalibrationState()
	restoreCalibrationSleepAssertion()

	if err := ensureStartupSleepPolicy(); err != nil {
		return err
	}

	router := setupRoutes()
	sseHub = events.NewEventHub()

	// Receive SIGHUP to reload config
	go func() {
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, syscall.SIGHUP)
		for range sigc {
			chargeControlTransitionMu.Lock()
			err := conf.Load()
			if err == nil {
				disableUnsupportedConfiguredFeatures()
				err = reconcileReloadedSleepPolicy()
			}
			chargeControlTransitionMu.Unlock()
			if err != nil {
				logrus.WithError(err).Error("failed to reload config safely")
				continue
			}
			logrus.Infof("config reloaded")
		}
	}()

	scheduler = NewScheduler(
		func() error {
			threshold := conf.CalibrationDischargeThreshold()
			hold := conf.CalibrationHoldDurationMinutes()
			return startCalibration(threshold, hold)
		},
		func() error {
			status := getCalibrationStatus()
			if status.Phase != calibration.PhaseIdle {
				return ErrCalibrationInProgress
			}
			if !status.PluggedIn {
				return errors.New("the Mac must be plugged in to start calibration")
			}
			return nil
		},
		func(data any) {
			runAt := data.(time.Time)
			sseHub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
				Action:  string(calibration.ActionScheduleUpComing),
				Message: fmt.Sprintf("Calibration will start at %s", runAt.Format("Jan _2 15:04")),
				Ts:      time.Now().Unix(),
			})
		},
		func(data any) {
			err := data.(error)
			sseHub.Publish(events.CalibrationAction, events.CalibrationActionEvent{
				Action:  string(calibration.ActionScheduleError),
				Message: err.Error(),
				Ts:      time.Now().Unix(),
			})
		},
	)
	defer scheduler.Stop()

	// Load persisted schedule from config
	if cronExpr := conf.Cron(); getCapabilities().Calibration && cronExpr != "" {
		if err := scheduler.Schedule(cronExpr); err != nil {
			logrus.WithError(err).Warn("failed to restore schedule from config")
		} else {
			scheduler.Start()
			logrus.WithField("cron", cronExpr).Info("restored schedule from config")
		}
	}

	srv := &http.Server{
		Handler: router,
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

	if conf.AllowNonRootAccess() || allowNonRoot {
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
		if err := listenNotifications(); err != nil {
			logrus.Errorf("failed to listen to system sleep notifications: %v", err)
			os.Exit(1)
		}
	}()

	go func() {
		logrus.Debugln("main loop starts")

		infiniteLoop()

		logrus.Errorf("main loop exited unexpectedly")
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
	stopListeningNotifications()

	if err := AllowSleepOnAC(); err != nil {
		logrus.Errorf("failed to remove PM assertion before exiting: %v", err)
	}
	if err := AllowCalibrationSleep(); err != nil {
		logrus.Errorf("failed to remove calibration sleep assertion before exiting: %v", err)
	}

	exitCaps := getCapabilities()
	if exitCaps.ChargingControl {
		if err := resetChargeControl(); err != nil {
			logrus.Errorf("failed to reset charge control before exiting: %v", err)
		}
	}

	if err := shutdownAdapterAndSleep(); err != nil {
		logrus.Errorf("failed to restore adapter/sleep state before exiting: %v", err)
	}

	logrus.Info("closing smc connection")
	err = smcConn.Close()
	if err != nil {
		logrus.Errorf("failed to close smc connection: %v", err)
	}

	logrus.Info("exiting")
	return nil
}

func reconcileReloadedSleepPolicy() error {
	if !adapterSleepPolicyCapable() {
		return nil
	}
	if err := reconcileAdapterSleepPolicy(); err != nil {
		return restoreAdapterAfterPolicyError(err)
	}
	return nil
}

// ensureStartupSleepPolicy reconciles the sleep hold with the adapter state
// before the daemon serves requests. An unusable snapshot file does not stop the
// daemon: the charge limit does not depend on it. The file stays in place, no
// hold can be taken while it exists, and wall power is already restored when
// protection was missing. Every other failure stops the start.
func ensureStartupSleepPolicy() error {
	err := reconcileStartupSleepPolicy()
	var snapshotErr *sleepSnapshotError
	if errors.As(err, &snapshotErr) {
		logrus.WithError(err).Errorf("ignoring the unusable sleep snapshot %s; batt starts without it and cannot hold sleep until it is fixed or removed. If the Mac no longer sleeps, run `sudo pmset -a disablesleep 0`", snapshotErr.path)
		return nil
	}
	return err
}

func reconcileStartupSleepPolicy() error {
	if adapterSleepPolicyCapable() {
		if err := reconcileAdapterSleepPolicy(); err != nil {
			return fmt.Errorf("startup: %w", restoreAdapterAfterPolicyError(err))
		}
		return nil
	}
	if err := restorePendingSleepDisabled(); err != nil {
		return fmt.Errorf("failed to restore pending sleep-disabled state during startup: %w", err)
	}
	return nil
}

func shutdownAdapterAndSleep() error {
	if adapterSleepPolicyCapable() {
		if err := smcEnableAdapter(); err != nil {
			logrus.Errorf("failed to re-enable adapter before exiting: %v", err)
			return err
		}
		if err := releaseAllSleepHolds(); err != nil {
			logrus.Errorf("failed to restore SleepDisabled before exiting: %v", err)
			return err
		}
	} else {
		if err := releaseAllSleepHolds(); err != nil {
			logrus.Errorf("failed to restore SleepDisabled before exiting: %v", err)
			return err
		}
	}
	return nil
}

func initRuntimeStates(configPath string) {
	dir := "/etc"
	if configPath != "" {
		dir = filepath.Dir(configPath)
	}
	initCalibrationState(filepath.Join(dir, "batt.state.json"))
	if err := initSleepDisabledState(filepath.Join(dir, "batt.sleep.json")); err != nil {
		logrus.WithError(err).Warn("failed to initialize sleep disabled state")
	}
}
