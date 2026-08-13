package audio

import "strings"

type DeviceType int

const (
	DeviceTypeMic DeviceType = iota
	DeviceTypeOutput
	DeviceTypeMonitor
)

func (d DeviceType) String() string {
	switch d {
	case DeviceTypeMic:
		return "mic"
	case DeviceTypeOutput:
		return "output"
	case DeviceTypeMonitor:
		return "monitor"
	default:
		return "unknown"
	}
}

type Device struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Type        DeviceType `json:"-"`
	IsDefault   bool       `json:"-"`
	Index       int        `json:"index"`
	State       string     `json:"state"`
}

const sinkNamePrefix = "recgo_record_sink"

func FindDefaultMic(devices []Device) *Device {
	for i, d := range devices {
		if d.IsDefault && d.Type == DeviceTypeMic {
			return &devices[i]
		}
	}
	if len(devices) > 0 {
		return &devices[0]
	}
	return nil
}

func FindDefaultMonitor(devices []Device) *Device {
	for i, d := range devices {
		if d.IsDefault && d.Type == DeviceTypeMonitor {
			return &devices[i]
		}
	}
	if defaultSink, _ := GetDefaultSink(); defaultSink != "" {
		monitorName := defaultSink + ".monitor"
		for i, d := range devices {
			if d.Name == monitorName {
				return &devices[i]
			}
		}
	}
	if len(devices) > 0 {
		return &devices[0]
	}
	return nil
}

func IsOwnSink(name string) bool {
	return strings.HasPrefix(strings.TrimSuffix(name, ".monitor"), sinkNamePrefix)
}

func GetSinkNameFromMonitor(monitorName string) string {
	return strings.TrimSuffix(monitorName, ".monitor")
}
