package server

import (
	"fmt"
	"unicode/utf8"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/store"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	timeZoneMinLen = 1
	timeZoneMaxLen = 64
	utcOffsetMin   = -840
	utcOffsetMax   = 840

	factPluginMinLen  = 1
	factPluginMaxLen  = 32
	factUserSIDMaxLen = 184
	factExeMaxLen     = 260
	factPathMaxLen    = 1024
	factExesMaxItems  = 200
)

// validateMachineTimeZone enforces HeartbeatRequest / PostFactsRequest zone fields.
func validateMachineTimeZone(timeZone string, utcOffsetMinutes int32) error {
	n := utf8.RuneCountInString(timeZone)
	if n < timeZoneMinLen || n > timeZoneMaxLen {
		return fmt.Errorf("time_zone must be 1..64 characters")
	}
	if utcOffsetMinutes < utcOffsetMin || utcOffsetMinutes > utcOffsetMax {
		return fmt.Errorf("utc_offset_minutes must be between %d and %d", utcOffsetMin, utcOffsetMax)
	}
	return nil
}

// validateUsageDate rejects missing/empty plainDate query values (Bind accepts "").
func validateUsageDate(date openapi_types.Date) error {
	if date.IsZero() {
		return fmt.Errorf("date is required")
	}
	return nil
}

// validatePostFactsRequest enforces PostFactsRequest / FactItem contract bounds
// that BindJSONBody does not check (min/max, enums, maxItems, rune maxLength).
func validatePostFactsRequest(body *agentapi.PostFactsRequest) error {
	if body == nil {
		return fmt.Errorf("missing body")
	}
	if body.Seq < 1 {
		return fmt.Errorf("seq must be >= 1")
	}
	if err := validateMachineTimeZone(body.TimeZone, body.UtcOffsetMinutes); err != nil {
		return err
	}
	if body.SentAt.IsZero() {
		return fmt.Errorf("sent_at is required")
	}
	if len(body.Facts) > store.FactBatchMaxCount {
		return fmt.Errorf("facts exceeds max of %d items", store.FactBatchMaxCount)
	}
	for i, item := range body.Facts {
		if err := validateFactItem(i, item); err != nil {
			return err
		}
	}
	return nil
}

func validateFactItem(i int, item agentapi.FactItem) error {
	prefix := fmt.Sprintf("facts[%d]", i)
	if item.Ts.IsZero() {
		return fmt.Errorf("%s: ts is required", prefix)
	}
	if !item.Kind.Valid() {
		return fmt.Errorf("%s: unknown kind %q", prefix, item.Kind)
	}
	pluginLen := utf8.RuneCountInString(item.Plugin)
	if pluginLen < factPluginMinLen || pluginLen > factPluginMaxLen {
		return fmt.Errorf("%s: plugin must be 1..32 characters", prefix)
	}
	if item.UserSid != nil && utf8.RuneCountInString(*item.UserSid) > factUserSIDMaxLen {
		return fmt.Errorf("%s: user_sid exceeds %d characters", prefix, factUserSIDMaxLen)
	}
	if item.DurS < 0 || item.DurS > int32(store.FactDurMaxSeconds) {
		return fmt.Errorf("%s: dur_s must be 0..%d", prefix, store.FactDurMaxSeconds)
	}
	return validateFactData(prefix, item.Data)
}

func validateFactData(prefix string, data agentapi.FactData) error {
	if data.State != nil && !data.State.Valid() {
		return fmt.Errorf("%s: unknown state %q", prefix, *data.State)
	}
	if data.Exes != nil {
		if len(*data.Exes) > factExesMaxItems {
			return fmt.Errorf("%s: exes exceeds max of %d items", prefix, factExesMaxItems)
		}
		for j, exe := range *data.Exes {
			if utf8.RuneCountInString(exe) > factExeMaxLen {
				return fmt.Errorf("%s: exes[%d] exceeds %d characters", prefix, j, factExeMaxLen)
			}
		}
	}
	if data.Exe != nil && utf8.RuneCountInString(*data.Exe) > factExeMaxLen {
		return fmt.Errorf("%s: exe exceeds %d characters", prefix, factExeMaxLen)
	}
	if data.Path != nil && utf8.RuneCountInString(*data.Path) > factPathMaxLen {
		return fmt.Errorf("%s: path exceeds %d characters", prefix, factPathMaxLen)
	}
	if data.Pid != nil && *data.Pid < 0 {
		return fmt.Errorf("%s: pid must be >= 0", prefix)
	}
	if data.Title != nil && utf8.RuneCountInString(*data.Title) > store.FactTitleMaxLen {
		return fmt.Errorf("%s: title exceeds %d characters", prefix, store.FactTitleMaxLen)
	}
	if data.IdleS != nil && *data.IdleS < 0 {
		return fmt.Errorf("%s: idle_s must be >= 0", prefix)
	}
	if data.DroppedBatches != nil && *data.DroppedBatches < 0 {
		return fmt.Errorf("%s: dropped_batches must be >= 0", prefix)
	}
	if data.DroppedBytes != nil && *data.DroppedBytes < 0 {
		return fmt.Errorf("%s: dropped_bytes must be >= 0", prefix)
	}
	return nil
}
