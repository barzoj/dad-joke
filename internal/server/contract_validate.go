package server

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/usage"
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

// validateMachineTimeZone enforces HeartbeatRequest / PostFactsRequest zone
// length/offset bounds. utcOffsetMinutes must be present (non-nil): omission
// and JSON null bind to nil on the generated *int32 field and are rejected so
// a missing offset cannot silently become 0.
func validateMachineTimeZone(timeZone string, utcOffsetMinutes *int32) error {
	if !utf8.ValidString(timeZone) {
		return fmt.Errorf("time_zone must be valid UTF-8")
	}
	n := utf8.RuneCountInString(timeZone)
	if n < timeZoneMinLen || n > timeZoneMaxLen {
		return fmt.Errorf("time_zone must be 1..64 characters")
	}
	if utcOffsetMinutes == nil {
		return fmt.Errorf("utc_offset_minutes is required")
	}
	if *utcOffsetMinutes < utcOffsetMin || *utcOffsetMinutes > utcOffsetMax {
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
// that BindJSONBody does not check (min/max, enums, maxItems).
// Semantic checks (zone load, offset cross-check, ts/retention, per-kind dur_s
// and required data) are in validateFactsBatchSemantics.
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
	if !utf8.ValidString(item.Plugin) {
		return fmt.Errorf("%s: plugin must be valid UTF-8", prefix)
	}
	pluginLen := utf8.RuneCountInString(item.Plugin)
	if pluginLen < factPluginMinLen || pluginLen > factPluginMaxLen {
		return fmt.Errorf("%s: plugin must be 1..32 characters", prefix)
	}
	if item.UserSid != nil {
		if !utf8.ValidString(*item.UserSid) {
			return fmt.Errorf("%s: user_sid must be valid UTF-8", prefix)
		}
		if utf8.RuneCountInString(*item.UserSid) > factUserSIDMaxLen {
			return fmt.Errorf("%s: user_sid exceeds %d characters", prefix, factUserSIDMaxLen)
		}
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
			if !utf8.ValidString(exe) {
				return fmt.Errorf("%s: exes[%d] must be valid UTF-8", prefix, j)
			}
			if utf8.RuneCountInString(exe) > factExeMaxLen {
				return fmt.Errorf("%s: exes[%d] exceeds %d characters", prefix, j, factExeMaxLen)
			}
		}
	}
	if data.Exe != nil {
		if !utf8.ValidString(*data.Exe) {
			return fmt.Errorf("%s: exe must be valid UTF-8", prefix)
		}
		if utf8.RuneCountInString(*data.Exe) > factExeMaxLen {
			return fmt.Errorf("%s: exe exceeds %d characters", prefix, factExeMaxLen)
		}
	}
	if data.Path != nil {
		if !utf8.ValidString(*data.Path) {
			return fmt.Errorf("%s: path must be valid UTF-8", prefix)
		}
		if utf8.RuneCountInString(*data.Path) > factPathMaxLen {
			return fmt.Errorf("%s: path exceeds %d characters", prefix, factPathMaxLen)
		}
	}
	if data.Pid != nil && *data.Pid < 0 {
		return fmt.Errorf("%s: pid must be >= 0", prefix)
	}
	if data.Title != nil && !utf8.ValidString(*data.Title) {
		return fmt.Errorf("%s: title must be valid UTF-8", prefix)
	}
	// Titles longer than FactTitleMaxLen are truncated at apply time (not rejected).
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

// validateFactsBatchSemantics enforces zone loadability, offset cross-check,
// ts/sent_at/retention bounds, per-kind dur_s, and per-kind required data.
// now is the injectable server clock; retentionDays bounds how old ts may be.
func validateFactsBatchSemantics(body *agentapi.PostFactsRequest, now time.Time, retentionDays int) error {
	if body == nil {
		return fmt.Errorf("missing body")
	}
	if retentionDays < store.FactRetentionDaysMin {
		retentionDays = store.FactRetentionDaysDefault
	}
	if err := usage.LoadZone(body.TimeZone); err != nil {
		return err
	}
	sentAt := body.SentAt.UTC()
	// Presence already enforced by validatePostFactsRequest / validateMachineTimeZone.
	if err := usage.CheckZoneOffset(sentAt, body.TimeZone, *body.UtcOffsetMinutes); err != nil {
		return err
	}
	oldest := now.UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	for i, item := range body.Facts {
		prefix := fmt.Sprintf("facts[%d]", i)
		ts := item.Ts.UTC()
		if ts.After(sentAt) {
			return fmt.Errorf("%s: ts must be <= sent_at", prefix)
		}
		if ts.Before(oldest) {
			return fmt.Errorf("%s: ts is older than retention (%d days)", prefix, retentionDays)
		}
		if err := validateFactKindSemantics(prefix, item); err != nil {
			return err
		}
	}
	return nil
}

func validateFactKindSemantics(prefix string, item agentapi.FactItem) error {
	switch item.Kind {
	case agentapi.SessionState, agentapi.ProcSample, agentapi.FgSample:
		if item.DurS < 1 || item.DurS > int32(store.FactDurMaxSeconds) {
			return fmt.Errorf("%s: dur_s must be 1..%d for kind %s", prefix, store.FactDurMaxSeconds, item.Kind)
		}
	case agentapi.QueueDropped, agentapi.SessionRestarted:
		if item.DurS != 0 {
			return fmt.Errorf("%s: dur_s must be 0 for kind %s", prefix, item.Kind)
		}
	}
	data := item.Data
	switch item.Kind {
	case agentapi.SessionState:
		if data.SessionId == nil || *data.SessionId == "" {
			return fmt.Errorf("%s: session_id is required", prefix)
		}
		if data.Username == nil || *data.Username == "" {
			return fmt.Errorf("%s: username is required", prefix)
		}
		if data.State == nil || !data.State.Valid() {
			return fmt.Errorf("%s: state is required", prefix)
		}
	case agentapi.ProcSample:
		if data.Exes == nil {
			return fmt.Errorf("%s: exes is required", prefix)
		}
	case agentapi.FgSample:
		if data.Exe == nil {
			return fmt.Errorf("%s: exe is required", prefix)
		}
		if data.Pid == nil {
			return fmt.Errorf("%s: pid is required", prefix)
		}
		if data.Title == nil {
			return fmt.Errorf("%s: title is required", prefix)
		}
		if data.IdleS == nil {
			return fmt.Errorf("%s: idle_s is required", prefix)
		}
	case agentapi.QueueDropped:
		if data.DroppedBatches == nil {
			return fmt.Errorf("%s: dropped_batches is required", prefix)
		}
		if data.DroppedBytes == nil {
			return fmt.Errorf("%s: dropped_bytes is required", prefix)
		}
	case agentapi.SessionRestarted:
		// no required data members
	}
	return nil
}
