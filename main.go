package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charlie0129/batt/smc"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	ginlogrus "github.com/toorop/gin-logrus"
)

func setupLogger() {
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05.000",
		FullTimestamp:   true,
	})
}

func setupRoutes() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	router := gin.Default()
	router.Use(ginlogrus.Logger(logrus.StandardLogger()), gin.Recovery())
	router.GET("/limit", getLimit)
	router.PUT("/limit", setLimit)
	router.POST("/maintain", enableMaintain)
	router.DELETE("/maintain", disableMaintain)

	return router
}

var (
	smcConn *smc.Connection
)

func main() {
	setupLogger()

	router := setupRoutes()

	err := loadConfig()
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("config loaded: %#v", config)

	srv := &http.Server{
		Handler: router,
	}

	// Create the socket to listen on:
	l, err := net.Listen("unix", "/var/run/batt.sock")
	if err != nil {
		logrus.Fatal(err)
		return
	}

	// Serve HTTP on unix socket
	go func() {
		logrus.Infof("http server listening on %s", l.Addr().String())
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrus.Fatal(err)
		}
	}()

	// Listen to system sleep notifications.
	go listenNotifications()

	// Open Apple SMC for read/writing
	smcConn = smc.New()
	if err := smcConn.Open(); err != nil {
		logrus.Fatal(err)
	}

	go func() {
		logrus.Infof("main loop starts")

		for mainLoop() {
		}

		logrus.Errorf("main loop exited unexpectedly")
	}()

	// Handle common process-killing signals so we can gracefully shut down:
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	// Wait for a SIGINT or SIGKILL:
	sig := <-sigc
	logrus.Infof("Caught signal %s: shutting down.", sig)
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	srv.Shutdown(ctx)
	// Stop listening (and unlink the socket if unix type):
	l.Close()
	smcConn.Close()
	saveConfig()
	os.Exit(0)
}
