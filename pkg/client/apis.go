package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkgerrors "github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/events"
	"github.com/charlie0129/batt/pkg/powerinfo"
)

func (c *Client) SetLimit(l int) (string, error) {
	return c.Put("/limit", strconv.Itoa(l))
}

func (c *Client) SetAdapter(enabled bool) (string, error) {
	return c.Put("/adapter", strconv.FormatBool(enabled))
}

func (c *Client) GetAdapter() (bool, error) {
	ret, err := c.Get("/adapter")
	if err != nil {
		return false, pkgerrors.Wrapf(err, "failed to get power adapter status")
	}
	return parseBoolResponse(ret)
}

func (c *Client) SetLowerLimitDelta(delta int) (string, error) {
	return c.Put("/lower-limit-delta", strconv.Itoa(delta))
}

func (c *Client) SetPreventIdleSleep(enabled bool) (string, error) {
	return c.Put("/prevent-idle-sleep", strconv.FormatBool(enabled))
}

func (c *Client) SetDisableChargingPreSleep(enabled bool) (string, error) {
	return c.Put("/disable-charging-pre-sleep", strconv.FormatBool(enabled))
}

func (c *Client) SetPreventSystemSleep(enabled bool) (string, error) {
	return c.Put("/prevent-system-sleep", strconv.FormatBool(enabled))
}

func (c *Client) SetControlMagSafeLED(mode config.ControlMagSafeMode) (string, error) {
	payload, err := json.Marshal(mode)
	if err != nil {
		return "", err
	}
	return c.Put("/magsafe-led", string(payload))
}

func (c *Client) GetCharging() (bool, error) {
	ret, err := c.Get("/charging")
	if err != nil {
		return false, pkgerrors.Wrapf(err, "failed to get charging status")
	}
	return parseBoolResponse(ret)
}

func (c *Client) GetPluggedIn() (bool, error) {
	ret, err := c.Get("/plugged-in")
	if err != nil {
		return false, pkgerrors.Wrapf(err, "failed to check if you are plugged in")
	}
	return parseBoolResponse(ret)
}

func (c *Client) GetCurrentCharge() (int, error) {
	ret, err := c.Get("/current-charge")
	if err != nil {
		return 0, pkgerrors.Wrapf(err, "failed to get current charge")
	}
	currentCharge, err := strconv.Atoi(ret)
	if err != nil {
		return 0, pkgerrors.Wrapf(err, "failed to unmarshal current charge")
	}
	return currentCharge, nil
}

func (c *Client) GetBatteryInfo() (*powerinfo.Battery, error) {
	ret, err := c.Get("/battery-info")
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to get battery info")
	}

	var bat powerinfo.Battery
	if err := json.Unmarshal([]byte(ret), &bat); err != nil {
		return nil, fmt.Errorf("failed to unmarshal battery info: %w", err)
	}

	return &bat, nil
}

func (c *Client) GetChargingControlCapable() (bool, error) {
	ret, err := c.Get("/charging-control-capable")
	if err != nil {
		return false, pkgerrors.Wrapf(err, "failed to get charging control capability")
	}

	capable, err := strconv.ParseBool(ret)
	if err != nil {
		return false, pkgerrors.Wrapf(err, "failed to parse charging control capability response")
	}

	return capable, nil
}

func (c *Client) GetConfig() (*config.RawFileConfig, error) {
	ret, err := c.Get("/config")
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to get config")
	}

	var conf config.RawFileConfig
	if err := json.Unmarshal([]byte(ret), &conf); err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to unmarshal config")
	}

	return &conf, nil
}

func (c *Client) GetVersion() (string, error) {
	ret, err := c.Get("/version")
	if err != nil {
		return "", pkgerrors.Wrapf(err, "failed to get version")
	}
	// Remove "" around JSON string. I don't want to use a JSON decoder just for this.
	ret = ret[1 : len(ret)-1] // remove the surrounding quotes
	return ret, nil
}

func (c *Client) GetPowerTelemetry() (*powerinfo.PowerTelemetry, error) {
	ret, err := c.Get("/power-telemetry")
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to get power telemetry")
	}

	var info powerinfo.PowerTelemetry
	if err := json.Unmarshal([]byte(ret), &info); err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to unmarshal power telemetry")
	}
	return &info, nil
}

// TelemetryResponse represents unified telemetry returned from the daemon.
type TelemetryResponse struct {
	Power       *powerinfo.PowerTelemetry `json:"power,omitempty"`
	Calibration *calibration.Status       `json:"calibration,omitempty"`
}

// GetTelemetry fetches unified telemetry; set power or calibration to false to exclude.
func (c *Client) GetTelemetry(includePower, includeCalibration bool) (*TelemetryResponse, error) {
	// Build query params: default include if true
	q := ""
	if !includePower {
		q += "power=0&"
	}
	if !includeCalibration {
		q += "calibration=0&"
	}
	if len(q) > 0 {
		q = "?" + q[:len(q)-1]
	}
	ret, err := c.Get("/telemetry" + q)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to get unified telemetry")
	}
	var tr TelemetryResponse
	if err := json.Unmarshal([]byte(ret), &tr); err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to unmarshal unified telemetry")
	}
	return &tr, nil
}

// SubscribeEvents connects to /event and streams SSE events.
// It will auto-reconnect until ctx is canceled. Returned channel is closed on ctx.Done().
func (c *Client) SubscribeEvents(ctx context.Context) <-chan events.Event {
	ch := make(chan events.Event, 32)
	go func() {
		defer close(ch)
		retry := 3 * time.Second
		for {
			if ctx.Err() != nil {
				return
			}

			req, err := http.NewRequestWithContext(ctx, "GET", "http://unix/event", nil)
			if err != nil {
				logrus.WithError(err).Warn("SSE request build failed; retrying")
				select {
				case <-time.After(retry):
				case <-ctx.Done():
					return
				}
				continue
			}
			resp, err := c.httpClient.Do(req)
			if err != nil {
				logrus.WithError(err).Warn("SSE connect failed; retrying")
				select {
				case <-time.After(retry):
				case <-ctx.Done():
					return
				}
				continue
			}

			reader := bufio.NewReader(resp.Body)
			var curName string
			var curData strings.Builder

			flushFrame := func() {
				if curName == "" || curData.Len() == 0 {
					curName = ""
					curData.Reset()
					return
				}
				payload := json.RawMessage([]byte(curData.String()))
				select {
				case ch <- events.Event{Name: curName, Data: payload}:
				default:
					// drop if slow
				}
				curName = ""
				curData.Reset()
			}

		loop:
			for {
				select {
				case <-ctx.Done():
					_ = resp.Body.Close()
					return
				default:
					line, err := reader.ReadString('\n')
					if err != nil {
						_ = resp.Body.Close()
						logrus.WithError(err).Debug("SSE stream ended")
						break loop
					}
					line = strings.TrimRight(line, "\r\n")
					if len(line) == 0 { // frame end
						flushFrame()
						continue
					}
					if strings.HasPrefix(line, ":") {
						continue
					}
					if strings.HasPrefix(line, "retry:") {
						// parse retry ms if provided
						v := strings.TrimSpace(strings.TrimPrefix(line, "retry:"))
						if ms, err := time.ParseDuration(v + "ms"); err == nil && ms > 0 {
							retry = ms
						}
						continue
					}
					if strings.HasPrefix(line, "event:") {
						curName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
						continue
					}
					if strings.HasPrefix(line, "data:") {
						if curData.Len() > 0 {
							curData.WriteByte('\n')
						}
						curData.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
						continue
					}
				}
			}

			time.Sleep(retry)
		}
	}()
	return ch
}

func (c *Client) StartCalibration() (string, error) {
	return c.Send("POST", "/calibration/start", "")
}

func (c *Client) PauseCalibration() (string, error) { return c.Send("POST", "/calibration/pause", "") }
func (c *Client) ResumeCalibration() (string, error) {
	return c.Send("POST", "/calibration/resume", "")
}
func (c *Client) CancelCalibration() (string, error) {
	return c.Send("POST", "/calibration/cancel", "")
}

func (c *Client) Schedule(cronExpr string) ([]time.Time, error) {
	var nextRuns []time.Time
	resp, err := c.Put("/schedule", cronExpr)
	if err != nil {
		return nextRuns, pkgerrors.Wrapf(err, "failed to set cron expression")
	}

	if err := json.Unmarshal([]byte(resp), &nextRuns); err != nil {
		return nextRuns, pkgerrors.Wrapf(err, "failed to read next runs")
	}
	return nextRuns, nil
}
func (c *Client) PostponeSchedule(d time.Duration) (string, error) {
	return c.Put("/schedule/postpone", d.String())
}
func (c *Client) SkipSchedule() (string, error) {
	return c.Put("/schedule/skip", "")
}

func parseBoolResponse(resp string) (bool, error) {
	switch resp {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, pkgerrors.Errorf("unexpected response: %s", resp)
	}
}
