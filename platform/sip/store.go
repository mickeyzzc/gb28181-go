package sip

import (
	"context"
	"time"
)

// GB28181Device is the persisted form of a registered GB28181 device.
type GB28181Device struct {
	ID            string
	Name          string
	Manufacturer  string
	Model         string
	Status        string // "online" | "offline"
	LastKeepalive time.Time
	RegisteredAt  time.Time
}

// GB28181Channel is the persisted form of a channel in a GB28181 device's
// catalog. CameraID links the channel to a host camera once bound.
type GB28181Channel struct {
	ID           string
	DeviceID     string
	Name         string
	Manufacturer string
	Parental     int
	Status       string // "idle" | "inviting" | "playing"
	CameraID     string
	UpdatedAt    time.Time
}

// DeviceStore persists device registrations and catalog data so the host
// application (REST API, UI) reflects live SIP state. Implementations live on
// the host side (MiBeeNvr adapts its storage.DB); pass nil to Server to skip
// persistence. All methods must be safe for concurrent use.
type DeviceStore interface {
	UpsertGB28181Device(ctx context.Context, device GB28181Device) error
	UpsertGB28181Channel(ctx context.Context, channel GB28181Channel) error
	ListGB28181Devices(ctx context.Context) ([]GB28181Device, error)
	ListGB28181Channels(ctx context.Context, deviceID string) ([]GB28181Channel, error)
	MarkDeviceOffline(ctx context.Context, id string) error
	BindChannelCamera(ctx context.Context, channelID, cameraID string) error
	DeleteGB28181Device(ctx context.Context, id string) error
	GetGB28181Device(ctx context.Context, id string) (*GB28181Device, error)
	DeleteGB28181Channel(ctx context.Context, channelID string) error
}
