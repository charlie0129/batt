package client

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/charlie0129/batt/pkg/compatibility"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
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
