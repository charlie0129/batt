package gui

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/distatus/battery"
	"github.com/getlantern/systray"

	"github.com/charlie0129/batt/pkg/config"
)

func onReady() {
	systray.SetTitle("🔋 Loading...")
	systray.SetTooltip("batt - Battery Manager")

	mStatus := systray.AddMenuItem("Status: Connecting...", "Current battery status")
	mStatus.Disable()

	timeToLimit := systray.AddMenuItem("Time to Limit: -", "Time remaining until battery reaches limit")
	timeToLimit.Disable()

	mLimit := systray.AddMenuItem("Limit: -", "Current battery limit")
	mLimit.Disable()

	systray.AddSeparator()

	mLimit80 := systray.AddMenuItem("Set Limit to 80%", "Set charging limit to 80 percent")
	mLimit60 := systray.AddMenuItem("Set Limit to 60%", "Set charging limit to 60 percent")

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit the whole app")

	go func() {
		limitChan := make(chan string)
		go func() {
			for {
				select {
				case <-mLimit80.ClickedCh:
					limitChan <- "80"
				case <-mLimit60.ClickedCh:
					limitChan <- "60"
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()

		for {
			select {
			case newLimit := <-limitChan:
				systray.SetTitle(fmt.Sprintf("Setting to %s%%...", newLimit))
				_, err := apiClient.Put("/limit", newLimit)
				if err != nil {
					log.Printf("Failed to set limit: %v", err)
				}
				time.Sleep(2 * time.Second)

			case <-time.After(2 * time.Second):
				updateStatus(mStatus, mLimit, timeToLimit)
			}
		}
	}()

	updateStatus(mStatus, mLimit, timeToLimit)
}

func onExit() {
	log.Println("batt-gui exiting")
}

func updateStatus(mStatus, mLimit, timeToLimit *systray.MenuItem) {
	chargeJSON, err := apiClient.Get("/current-charge")
	if err != nil {
		systray.SetTitle("🚫 Offline")
		mStatus.SetTitle("Status: Disconnected")
		mLimit.SetTitle("Limit: -")
		timeToLimit.SetTitle("Time to Limit: -")
		log.Printf("Cannot connect to daemon: %v", err)
		return
	}

	configJSON, err := apiClient.Get("/config")
	if err != nil {
		log.Printf("Failed to get config: %v", err)
		return
	}

	batteryInfoJSON, err := apiClient.Get("/battery-info")
	if err != nil {
		log.Printf("Failed to get battery info: %v", err)
		return
	}

	var rawConfig config.RawFileConfig
	if err := json.Unmarshal([]byte(configJSON), &rawConfig); err != nil {
		log.Printf("Failed to unmarshal config: %v", err)
		return
	}

	conf := config.NewFileFromConfig(&rawConfig, "")

	var bat battery.Battery
	if err := json.Unmarshal([]byte(batteryInfoJSON), &bat); err != nil {
		log.Printf("Failed to unmarshal battery info: %v", err)
		return
	}

	currentCharge, _ := strconv.Atoi(chargeJSON)

	statusIcon := ""
	state := "Not charging"
	switch bat.State {
	case battery.Charging:
		statusIcon = "⚡️"
		state = "Charging"
	case battery.Discharging:
		state = "Discharging"
	case battery.Full:
		state = "Full"
	}

	systray.SetTitle(fmt.Sprintf("%s %d%%", statusIcon, currentCharge))
	mStatus.SetTitle(fmt.Sprintf("Status: %s", state))

	timeString := "N/A"
	if bat.State == battery.Charging && currentCharge < conf.UpperLimit() {
		designCapacityWh := bat.Design / 1000.0
		chargeRateW := bat.ChargeRate / 1000.0

		targetCapacityWh := float64(conf.UpperLimit()) / 100.0 * designCapacityWh
		currentCapacityWh := float64(currentCharge) / 100.0 * designCapacityWh
		capacityToChargeWh := targetCapacityWh - currentCapacityWh

		if chargeRateW > 0 && capacityToChargeWh > 0 {
			timeToLimitHours := capacityToChargeWh / chargeRateW
			totalSeconds := timeToLimitHours * 3600
			minutes := int(totalSeconds) / 60
			seconds := int(totalSeconds) % 60

			if minutes > 0 {
				timeString = fmt.Sprintf("~%d min %d sec", minutes, seconds)
			} else if seconds > 0 {
				timeString = fmt.Sprintf("~%d sec", seconds)
			}
		}
	}
	timeToLimit.SetTitle(fmt.Sprintf("Time to Limit: %s", timeString))
	mLimit.SetTitle(fmt.Sprintf("Limit: %d%%", conf.UpperLimit()))
}
