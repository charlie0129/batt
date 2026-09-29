package main

import (
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/charlie0129/batt/pkg/client"
)

func TestChargeCommandRegistrationAndNoArgs(t *testing.T) {
	command := NewChargeCommand()
	for _, name := range []string{"now", "full", "cancel"} {
		subcommand, _, err := command.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		if subcommand.Name() != name {
			t.Fatalf("find %s returned %s", name, subcommand.Name())
		}
		if err := subcommand.Args(subcommand, []string{"unexpected"}); err == nil {
			t.Errorf("%s accepts a positional argument, want cobra.NoArgs", name)
		}
	}
}

func TestChargeCommandsCallTheExpectedEndpoint(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "now", path: "/charge-once/limit"},
		{name: "full", path: "/charge-once/full"},
		{name: "cancel", path: "/charge-once/cancel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketFile, err := os.CreateTemp("/tmp", "batt-test-*.sock")
			if err != nil {
				t.Fatal(err)
			}
			socketPath := socketFile.Name()
			if err := socketFile.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(socketPath); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(socketPath) })

			listener, err := net.Listen("unix", socketPath)
			if err != nil {
				t.Fatal(err)
			}

			request := make(chan *http.Request, 1)
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request <- r
				w.WriteHeader(http.StatusCreated)
			})}
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close() })

			previousClient := apiClient
			apiClient = client.NewClient(socketPath)
			t.Cleanup(func() { apiClient = previousClient })

			command := NewChargeCommand()
			subcommand, _, err := command.Find([]string{tt.name})
			if err != nil {
				t.Fatal(err)
			}
			if err := subcommand.RunE(subcommand, nil); err != nil {
				t.Fatal(err)
			}

			got := <-request
			if got.Method != http.MethodPost {
				t.Fatalf("method = %q, want POST", got.Method)
			}
			if got.URL.Path != tt.path {
				t.Fatalf("path = %q, want %q", got.URL.Path, tt.path)
			}
		})
	}
}
