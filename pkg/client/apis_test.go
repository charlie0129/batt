package client

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/charlie0129/batt/pkg/compatibility"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDisableAdapterFor(t *testing.T) {
	client := &Client{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPut {
			t.Fatalf("method = %q, want PUT", request.Method)
		}
		if request.URL.Path != "/adapter/disable" {
			t.Fatalf("path = %q, want /adapter/disable", request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(body); got != `"2h0m0s"` {
			t.Fatalf("body = %q, want quoted duration", got)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`"ok"`)),
			Header:     make(http.Header),
		}, nil
	})}}

	if _, err := client.DisableAdapterFor(2 * time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestGetCompatibility(t *testing.T) {
	client := &Client{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/compatibility" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
                "chargingControl": true,
                "chargeControlMode": "firmware",
                "sleepHooks": false,
                "magSafeLED": false,
                "adapterControl": false,
                "calibration": false
            }`)),
			Header: make(http.Header),
		}, nil
	})}}

	got, err := client.GetCompatibility()
	if err != nil {
		t.Fatal(err)
	}
	if !got.ChargingControl || got.ChargeControlMode != compatibility.ChargeControlFirmware || got.SleepHooks || got.MagSafeLED || got.AdapterControl || got.Calibration {
		t.Fatalf("unexpected compatibility: %+v", got)
	}
}
