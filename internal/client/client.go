package client

// TODO: make this package public, so other clients can call it

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

// Client is a struct for communicating with batt daemon
type Client struct {
	socketPath string
	httpClient *http.Client
}

// NewClient is a constructor for creating a new Client
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					conn, err := net.Dial("unix", socketPath)
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
		},
	}
}

// Send is a method for sending a request to the batt daemon
func (c *Client) Send(method string, path string, data string) (string, error) {
	logrus.WithFields(logrus.Fields{
		"method": method,
		"path":   path,
		"data":   data,
		"unix":   c.socketPath,
	}).Debug("sending request")

	var resp *http.Response
	var err error
	url := "http://unix" + path

	switch method {
	case "GET":
		resp, err = c.httpClient.Get(url)
	case "POST":
		resp, err = c.httpClient.Post(url, "application/octet-stream", strings.NewReader(data))
	case "PUT":
		req, err2 := http.NewRequest("PUT", url, strings.NewReader(data))
		if err2 != nil {
			return "", fmt.Errorf("failed to create request: %w", err2)
		}
		resp, err = c.httpClient.Do(req)
	default:
		return "", fmt.Errorf("unknown method: %s", method)
	}

	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			logrus.Errorf("failed to close response body: %v", err)
		}
	}()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	body := string(b)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("got %d: %s", resp.StatusCode, body)
	}

	return body, nil
}

// Get is a method for sending a GET request to the batt daemon
func (c *Client) Get(path string) (string, error) {
	return c.Send("GET", path, "")
}

// Put is a method for sending a PUT request to the batt daemon
func (c *Client) Put(path string, data string) (string, error) {
	return c.Send("PUT", path, data)
}
