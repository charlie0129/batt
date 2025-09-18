package client

import (
	"encoding/json"
	"fmt"
	"strconv"

	pkgerrors "github.com/pkg/errors"

	"github.com/charlie0129/batt/pkg/config"
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
