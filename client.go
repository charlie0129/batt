package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

func send(method string, path string, data string) (string, error) {
	logrus.WithFields(logrus.Fields{
		"method": method,
		"path":   path,
		"data":   data,
		"unix":   unixSocketPath,
	}).Debug("sending request")

	httpc := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				conn, err := net.Dial("unix", unixSocketPath)
				if err != nil {
					if os.IsNotExist(err) {
						return nil, ErrDaemonNotRunning
					}
					if os.IsPermission(err) {
						return nil, ErrPermissionDenied
					}
					logrus.Errorf("failed to connect to unix socket: %v", err)
					return nil, err
				}
				return conn, err
			},
		},
	}

	var resp *http.Response
	var err error

	switch method {
	case "GET":
		resp, err = httpc.Get("http://unix" + path)
	case "POST":
		resp, err = httpc.Post("http://unix"+path, "application/octet-stream", strings.NewReader(data))
	case "PUT":
		req, err2 := http.NewRequest("PUT", "http://unix"+path, strings.NewReader(data))
		if err2 != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}
		resp, err = httpc.Do(req)
	case "DELETE":
		req, err2 := http.NewRequest("DELETE", "http://unix"+path, nil)
		if err2 != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}
		resp, err = httpc.Do(req)
	default:
		return "", fmt.Errorf("unknown method: %s", method)
	}

	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	body := string(b)

	code := resp.StatusCode

	logrus.WithFields(logrus.Fields{
		"code": code,
		"body": body,
	}).Debug("got response")

	if code < 200 || code > 299 {
		return "", fmt.Errorf("got %d: %s", code, body)
	}

	return body, nil
}

func get(path string) (string, error) {
	return send("GET", path, "")
}

//nolint:unused
func post(path string, data string) (string, error) {
	return send("POST", path, data)
}

func put(path string, data string) (string, error) {
	return send("PUT", path, data)
}

//nolint:unused
func del(path string) (string, error) {
	return send("DELETE", path, "")
}
