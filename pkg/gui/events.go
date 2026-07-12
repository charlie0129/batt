package gui

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/events"
)

func (c *menuController) subscribeEvents(ctx context.Context) {
	for event := range c.api.SubscribeEvents(ctx) {
		logrus.WithFields(logrus.Fields{
			"event": event.Name,
			"data":  string(event.Data),
		}).Debug("new event")

		switch event.Name {
		case events.CalibrationAction:
			payload, err := events.DecodeAs[events.CalibrationActionEvent](event)
			if err != nil {
				logrus.WithError(err).Error("failed to decode calibration.action event")
				continue
			}
			showNotification("Calibration", payload.Message)
		case events.CalibrationPhase:
			payload, err := events.DecodeAs[events.CalibrationPhaseEvent](event)
			if err != nil {
				logrus.WithError(err).Error("failed to decode calibration.phase event")
				continue
			}
			if calibrationPhaseNotifies(calibration.Phase(payload.To)) {
				showNotification("Calibration", payload.Message)
			}
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
