package gui

import (
	"context"
	"log/slog"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/events"
)

func (c *menuController) subscribeEvents(ctx context.Context) {
	for event := range c.api.SubscribeEvents(ctx) {
		slog.Debug("new event",
			"event", event.Name,
			"data", string(event.Data),
		)

		switch event.Name {
		case events.CalibrationAction:
			payload, err := events.DecodeAs[events.CalibrationActionEvent](event)
			if err != nil {
				slog.Error("failed to decode calibration.action event", "error", err)
				continue
			}
			showNotification("Calibration", payload.Message)
		case events.CalibrationPhase:
			payload, err := events.DecodeAs[events.CalibrationPhaseEvent](event)
			if err != nil {
				slog.Error("failed to decode calibration.phase event", "error", err)
				continue
			}
			if calibrationPhaseNotifies(calibration.Phase(payload.To)) {
				showNotification("Calibration", payload.Message)
			}
		case events.ChargeOnceAction:
			payload, err := events.DecodeAs[events.ChargeOnceActionEvent](event)
			if err != nil {
				slog.Error("failed to decode charge-once.action event", "error", err)
				continue
			}
			showNotification("One-Time Charge", payload.Message)
		}
	}
}

func calibrationPhaseNotifies(phase calibration.Phase) bool {
	switch phase {
	case calibration.PhaseDischarge,
		calibration.PhaseCharge,
		calibration.PhaseHold,
		calibration.PhasePostHold,
		calibration.PhaseRestore,
		calibration.PhaseError:
		return true
	default:
		return false
	}
}
