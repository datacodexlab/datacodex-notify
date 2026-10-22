// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"fmt"

	"datacodex-notify/internal/store"
)

// AlertType names a kind of message the app raises itself. Each kind has its own
// on/off switch and delivery mode in the Rules tab.
type AlertType string

const (
	TypeHeartbeatLost AlertType = "heartbeat_lost"
	TypeHeartbeatBack AlertType = "heartbeat_back"
	TypeConnection    AlertType = "connection_warning"
	TypeDigest        AlertType = "digest"
	TypeAgentConnect  AlertType = "agent_connect"
)

// AlertTypes lists every kind in display order.
var AlertTypes = []AlertType{TypeHeartbeatLost, TypeHeartbeatBack, TypeConnection, TypeDigest, TypeAgentConnect}

// AlertSetting is the switch and delivery mode of one alert kind.
type AlertSetting struct {
	Enabled bool       `json:"enabled"`
	Mode    store.Mode `json:"mode"`
}

var alertDefaults = map[AlertType]AlertSetting{
	TypeHeartbeatLost: {Enabled: true, Mode: store.ModeBoth},
	TypeHeartbeatBack: {Enabled: true, Mode: store.ModeText},
	TypeConnection:    {Enabled: true, Mode: store.ModeText},
	TypeDigest:        {Enabled: true, Mode: store.ModeText},
	TypeAgentConnect:  {Enabled: false, Mode: store.ModeText},
}

// ValidAlertType reports whether t is a known kind.
func ValidAlertType(t AlertType) bool {
	_, ok := alertDefaults[t]
	return ok
}

// AlertEnabledKey is the settings key of a kind's on/off switch.
func AlertEnabledKey(t AlertType) string { return "alert_" + string(t) + "_enabled" }

// AlertModeKey is the settings key of a kind's delivery mode.
func AlertModeKey(t AlertType) string { return "alert_" + string(t) + "_mode" }

// AlertSettingFor returns the saved setting of a kind, or its default.
func (s *Service) AlertSettingFor(ctx context.Context, t AlertType) AlertSetting {
	setting := alertDefaults[t]
	if value, err := s.store.GetSetting(ctx, AlertEnabledKey(t)); err == nil {
		switch value {
		case "true":
			setting.Enabled = true
		case "false":
			setting.Enabled = false
		}
	}
	if value, err := s.store.GetSetting(ctx, AlertModeKey(t)); err == nil {
		if mode := store.Mode(value); validAlertMode(mode) {
			setting.Mode = mode
		}
	}
	return setting
}

// AlertSettings returns the setting of every kind.
func (s *Service) AlertSettings(ctx context.Context) map[AlertType]AlertSetting {
	out := make(map[AlertType]AlertSetting, len(AlertTypes))
	for _, t := range AlertTypes {
		out[t] = s.AlertSettingFor(ctx, t)
	}
	return out
}

func validAlertMode(m store.Mode) bool {
	return m == store.ModeText || m == store.ModeVoice || m == store.ModeBoth
}

// SaveAlertSetting validates and stores the setting of one kind.
func (s *Service) SaveAlertSetting(ctx context.Context, t AlertType, setting AlertSetting) error {
	if !ValidAlertType(t) {
		return fmt.Errorf("unknown alert kind %q", t)
	}
	if !validAlertMode(setting.Mode) {
		return fmt.Errorf("invalid mode %q: must be text, voice, or both", setting.Mode)
	}
	if err := s.store.SetSetting(ctx, AlertEnabledKey(t), fmt.Sprintf("%t", setting.Enabled)); err != nil {
		return err
	}
	return s.store.SetSetting(ctx, AlertModeKey(t), string(setting.Mode))
}
