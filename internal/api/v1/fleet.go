package v1

import (
	"fmt"
	"time"
)

const (
	DefaultComputeBoxSlots = 4
	MaxComputeBoxSlots     = 32
)

type FleetSlotState string

const (
	FleetSlotStarting  FleetSlotState = "starting"
	FleetSlotFree      FleetSlotState = "free"
	FleetSlotReserved  FleetSlotState = "reserved"
	FleetSlotOccupied  FleetSlotState = "occupied"
	FleetSlotDraining  FleetSlotState = "draining"
	FleetSlotUnhealthy FleetSlotState = "unhealthy"
	FleetSlotStopped   FleetSlotState = "stopped"
)

type LogicalBoxState string

const (
	LogicalBoxDetached    LogicalBoxState = "detached"
	LogicalBoxReserved    LogicalBoxState = "reserved"
	LogicalBoxAttaching   LogicalBoxState = "attaching"
	LogicalBoxRunning     LogicalBoxState = "running"
	LogicalBoxDraining    LogicalBoxState = "draining"
	LogicalBoxHibernating LogicalBoxState = "hibernating"
	LogicalBoxHibernated  LogicalBoxState = "hibernated"
	LogicalBoxDeleting    LogicalBoxState = "deleting"
	LogicalBoxFailed      LogicalBoxState = "failed"
)

// A box role is set when the workspace is created. A manager holds the
// fleet-wide contact permission; a worker only has explicit contact edges.
type BoxRole string

const (
	BoxRoleWorker  BoxRole = "worker"
	BoxRoleManager BoxRole = "manager"
)

func ValidBoxRole(role string) bool {
	return role == string(BoxRoleWorker) || role == string(BoxRoleManager)
}

type FleetConfig struct {
	Region             string    `json:"region,omitempty"`
	Provider           string    `json:"provider"`
	ProviderCredential string    `json:"providerCredential,omitempty"`
	ComputeBoxSlots    int       `json:"compute_box_slots"`
	UpdatedAt          time.Time `json:"updatedAt,omitempty"`
}

func (c FleetConfig) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if c.ComputeBoxSlots < 0 || c.ComputeBoxSlots > MaxComputeBoxSlots {
		return fmt.Errorf("compute_box_slots must be between 0 and %d", MaxComputeBoxSlots)
	}
	return nil
}

type SetFleetSlotsRequest struct {
	Provider           string `json:"provider"`
	ProviderCredential string `json:"providerCredential,omitempty"`
	ComputeBoxSlots    int    `json:"compute_box_slots"`
}

type ComputeSlot struct {
	ID                   string         `json:"id"`
	AccountID            string         `json:"accountId,omitempty"`
	Provider             string         `json:"provider"`
	ProviderCredential   string         `json:"providerCredential,omitempty"`
	Ordinal              int            `json:"ordinal"`
	State                FleetSlotState `json:"state"`
	ServiceID            string         `json:"serviceId,omitempty"`
	ServiceName          string         `json:"serviceName,omitempty"`
	DeploymentInstanceID string         `json:"deploymentInstanceId,omitempty"`
	LogicalBoxID         string         `json:"logicalBoxId,omitempty"`
	LogicalBoxName       string         `json:"logicalBoxName,omitempty"`
	Region               string         `json:"region,omitempty"`
	Image                string         `json:"image,omitempty"`
	ImageVersion         string         `json:"imageVersion,omitempty"`
	Health               string         `json:"health,omitempty"`
	AssignmentGeneration int64          `json:"assignmentGeneration"`
	LeaseOwner           string         `json:"leaseOwner,omitempty"`
	LeaseExpiresAt       *time.Time     `json:"leaseExpiresAt,omitempty"`
	FailureReason        string         `json:"failureReason,omitempty"`
	CreatedAt            time.Time      `json:"createdAt,omitempty"`
	UpdatedAt            time.Time      `json:"updatedAt,omitempty"`
}

type LogicalBox struct {
	Tools                []string        `json:"tools,omitempty"`
	ID                   string          `json:"id"`
	AccountID            string          `json:"accountId,omitempty"`
	Name                 string          `json:"name"`
	Provider             string          `json:"provider"`
	ProviderCredential   string          `json:"providerCredential,omitempty"`
	DefaultAgent         string          `json:"defaultAgent"`
	Role                 string          `json:"role,omitempty"`
	OwnerUserID          string          `json:"ownerUserId,omitempty"`
	State                LogicalBoxState `json:"state"`
	VolumeID             string          `json:"volumeId"`
	VolumeName           string          `json:"volumeName"`
	SlotID               string          `json:"slotId,omitempty"`
	AssignmentGeneration int64           `json:"assignmentGeneration"`
	LeaseOwner           string          `json:"leaseOwner,omitempty"`
	LeaseExpiresAt       *time.Time      `json:"leaseExpiresAt,omitempty"`
	RestorationState     string          `json:"restorationState,omitempty"`
	FailureReason        string          `json:"failureReason,omitempty"`
	CreatedAt            time.Time       `json:"createdAt,omitempty"`
	UpdatedAt            time.Time       `json:"updatedAt,omitempty"`
}

type Allocation struct {
	RequestID            string     `json:"requestId"`
	IdempotencyKey       string     `json:"idempotencyKey"`
	State                string     `json:"state"`
	QueuePosition        int        `json:"queuePosition,omitempty"`
	LogicalBoxID         string     `json:"logicalBoxId"`
	LogicalBoxName       string     `json:"logicalBoxName"`
	SlotID               string     `json:"slotId,omitempty"`
	ServiceID            string     `json:"serviceId,omitempty"`
	AssignmentGeneration int64      `json:"assignmentGeneration,omitempty"`
	FencingToken         string     `json:"fencingToken,omitempty"`
	LeaseOwner           string     `json:"leaseOwner,omitempty"`
	LeaseExpiresAt       *time.Time `json:"leaseExpiresAt,omitempty"`
	CreatedAt            time.Time  `json:"createdAt,omitempty"`
	Phase                string     `json:"phase,omitempty"`
	RetryCount           int        `json:"retryCount,omitempty"`
	FailureReason        string     `json:"failureReason,omitempty"`
	UpdatedAt            time.Time  `json:"updatedAt,omitempty"`
}

type FleetStatus struct {
	Provider             string        `json:"provider"`
	ProviderCredential   string        `json:"providerCredential,omitempty"`
	DesiredSlots         int           `json:"desiredSlots"`
	ActualSlots          int           `json:"actualSlots"`
	FreeSlots            int           `json:"freeSlots"`
	OccupiedSlots        int           `json:"occupiedSlots"`
	StartingSlots        int           `json:"startingSlots"`
	DrainingSlots        int           `json:"drainingSlots"`
	UnhealthySlots       int           `json:"unhealthySlots"`
	StoppedSlots         int           `json:"stoppedSlots"`
	PendingAllocations   int           `json:"pendingAllocationRequests"`
	Slots                []ComputeSlot `json:"slots"`
	DetachedLogicalBoxes []LogicalBox  `json:"detachedLogicalBoxes"`
}

type FleetCost struct {
	Currency  string  `json:"currency,omitempty"`
	Accrued   float64 `json:"accrued,omitempty"`
	Estimated bool    `json:"estimated,omitempty"`
	Available bool    `json:"available"`
	Detail    string  `json:"detail,omitempty"`
}

type FleetSlotCost struct {
	Ordinal        int            `json:"ordinal"`
	State          FleetSlotState `json:"state"`
	ServiceID      string         `json:"serviceId,omitempty"`
	ServiceName    string         `json:"serviceName,omitempty"`
	LogicalBoxName string         `json:"logicalBoxName,omitempty"`
	ObservedAt     time.Time      `json:"observedAt,omitempty"`
	Cost           FleetCost      `json:"cost"`
}

type FleetCostOverview struct {
	Provider             string          `json:"provider"`
	ProviderCredential   string          `json:"providerCredential,omitempty"`
	Period               string          `json:"period"`
	ObservedAt           time.Time       `json:"observedAt,omitempty"`
	Total                FleetCost       `json:"total"`
	AvailableSlotCount   int             `json:"availableSlotCount"`
	UnavailableSlotCount int             `json:"unavailableSlotCount"`
	Slots                []FleetSlotCost `json:"slots"`
}
