package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/smc"
)

var (
	smcConn *smc.AppleSMC
	conf    config.Config
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
	router.PUT("/lower-limit-delta", setLowerLimitDelta)
	router.PUT("/prevent-idle-sleep", setPreventIdleSleep)
	router.PUT("/disable-charging-pre-sleep", setDisableChargingPreSleep)
	router.PUT("/prevent-system-sleep", setPreventSystemSleep)
	router.PUT("/adapter", setAdapter)
	router.GET("/adapter", getAdapter)
	router.GET("/charging", getCharging)
	router.GET("/battery-info", getBatteryInfo)
	router.PUT("/magsafe-led", setControlMagSafeLED)
	router.GET("/current-charge", getCurrentCharge)
	router.GET("/plugged-in", getPluggedIn)
	router.GET("/charging-control-capable", getChargingControlCapable)
	router.GET("/version", getVersion)

	return router
}

func Run(configPath string, unixSocketPath string, allowNonRoot bool) error {
	router := setupRoutes()

	var err error
	conf, err = config.NewFile(configPath)
	if err != nil {
		logrus.Fatalf("failed to parse config during startup: %v", err)
	}
	logrus.WithFields(conf.LogrusFields()).Infof("config loaded")

	// Receive SIGHUP to reload config
	go func() {
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, syscall.SIGHUP)
		for range sigc {
			err := conf.Load()
			if err != nil {
				logrus.Errorf("failed to reload config: %v", err)
				continue
			}
			logrus.Infof("config reloaded")
		}
	}()

	srv := &http.Server{
		Handler: router,
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

	// Listen to system sleep notifications.
	go func() {
		err := listenNotifications()
		if err != nil {
			logrus.Errorf("failed to listen to system sleep notifications: %v", err)
			os.Exit(1)
		}
	}()

	// Open Apple SMC for read/writing
	smcConn = smc.New()
	if err := smcConn.Open(); err != nil {
		logrus.Fatal(err)
	}

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

	logrus.Info("shutting down http server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = srv.Shutdown(ctx)
	if err != nil {
		logrus.Errorf("failed to shutdown http server: %v", err)
	}
	cancel()

	logrus.Info("stopping listening notifications")
	stopListeningNotifications()

	if err := AllowSleepOnAC(); err != nil {
		logrus.Errorf("failed to remove PM assertion before exiting: %v", err)
	}

	if err := smcConn.EnableCharging(); err != nil {
		logrus.Errorf("failed to re-enable charging before exiting: %v", err)
	}

	logrus.Info("closing smc connection")
	err = smcConn.Close()
	if err != nil {
		logrus.Errorf("failed to close smc connection: %v", err)
	}

	logrus.Info("exiting")
	return nil
}
