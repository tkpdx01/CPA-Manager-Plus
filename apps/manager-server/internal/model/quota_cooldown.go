package model

const (
	QuotaCooldownOwnerUsage429                  = "cpamp_usage_429"
	QuotaCooldownOwnerAntigravityInspection    = "antigravity_inspection"

	QuotaCooldownStatusActive    = "active"
	QuotaCooldownStatusRecovered = "recovered"
	QuotaCooldownStatusSkipped   = "skipped"
)

type QuotaCooldown struct {
	ID               int64
	AuthFileName     string
	AuthIndex        string
	AccountSnapshot  string
	Provider         string
	RecoverAtMS      int64
	Owner            string
	EventHash        string
	PreDisabledState bool
	Status           string
	DisabledAtMS     int64
	RecoveredAtMS    int64
	LastError        string
	CreatedAtMS      int64
	UpdatedAtMS      int64
}

type QuotaCooldownUpsert struct {
	AuthFileName     string
	AuthIndex        string
	AccountSnapshot  string
	Provider         string
	RecoverAtMS      int64
	Owner            string
	EventHash        string
	PreDisabledState bool
	DisabledAtMS     int64
}
