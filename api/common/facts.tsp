import "@typespec/http";

using TypeSpec.Http;

/**
 * Shared agent facts ingest and machine usage shapes for AgentAPI and ControlAPI.
 * Each service imports this file; OpenAPI outputs remain independent.
 *
 * Facts ingest semantics (AgentAPI POST /v1/facts):
 * - Hard limits: max 500 facts per batch; max 256 KiB request body.
 *   Oversized fact count → 400; oversized body → 413.
 * - `seq` is per *batch*, monotonic within a `stream_id`. A retry re-sends
 *   the identical batch. `(stream_id, seq)` is the idempotency key; the
 *   acknowledgement sets `duplicate:true` on replay.
 * - Empty `facts` array is accepted as a no-op success.
 * - `time_zone` (IANA) + `utc_offset_minutes` ride with every batch: the
 *   **day** of a fact is its server-corrected timestamp in the batch's zone.
 *   The offset is a cross-check for a wrong Windows→IANA mapping.
 * - `title` is bounded by **characters/runes** (256); the server counts
 *   runes at apply time (not UTF-8 bytes). Titles longer than 256 runes are
 *   truncated to 256 runes without splitting a code point (stored value is
 *   the truncated one); invalid UTF-8 is rejected with 400.
 * - Default retention for stored facts is 30 days.
 *
 * Per-kind required members of `FactData` (enforced server-side; documented
 * here for clients):
 * - `session.state`: `session_id`, `username`, `state`
 * - `proc.sample`: `exes` (may be empty)
 * - `fg.sample`: `exe`, `pid`, `title`, `idle_s`; optional `path`
 * - `queue.dropped`: `dropped_batches`, `dropped_bytes`
 * - `session.restarted`: optional markers only (no required data members)
 *
 * ControlAPI usage (`GET /v1/machines/{machine_id}/usage?date=`):
 * - `date` is a required calendar day (`YYYY-MM-DD`); there is no `tz`
 *   query parameter (zone comes from ingested facts/heartbeats).
 * - Response lists per-app totals for that machine-local day.
 */
namespace DadJoke.Common;

/** Maximum facts accepted in one AgentAPI ingest batch. */
const FactBatchMaxCount = 500;

/** Maximum UTF-8 byte length of the POST /v1/facts request body. */
const FactBatchMaxBytes = 262144;

/** Maximum `dur_s` (seconds) allowed on a FactItem. */
const FactDurMaxSeconds = 300;

/** Maximum `title` length in characters/runes (not bytes). */
const FactTitleMaxLen = 256;

/** Default retention for stored facts (days). */
const FactRetentionDays = 30;

/** Kind of a single fact sample / marker. */
enum FactKind {
  SessionState: "session.state",
  ProcSample: "proc.sample",
  FgSample: "fg.sample",
  QueueDropped: "queue.dropped",
  SessionRestarted: "session.restarted",
}

/** Interactive session state reported by `session.state` facts. */
enum FactSessionState {
  active,
  locked,
  disconnected,
}

/** Windows image / short path name (≤ 260 characters). */
@maxLength(260)
scalar ExeName extends string;

/**
 * Flat optional payload for a FactItem.
 * One model (not a polymorphic union) keeps oapi-codegen output simple;
 * new kinds only add optional members. Per-kind required-member rules
 * are enforced server-side (see file doc comment).
 */
@friendlyName("FactData")
model FactData {
  /** Session identity (required for `session.state`). */
  session_id?: string;

  /** Windows username for the session (required for `session.state`). */
  username?: string;

  /** Session state (required for `session.state`). */
  state?: FactSessionState;

  /**
   * Process image names in a sample (required for `proc.sample`; may be empty).
   * Hard-capped at 200 items; each path/name ≤ 260 characters.
   */
  @maxItems(200)
  exes?: ExeName[];

  /** Foreground executable name (required for `fg.sample`). */
  exe?: ExeName;

  /** Full path of the foreground executable (optional on `fg.sample`). */
  @maxLength(1024)
  path?: string;

  /** Process id (required for `fg.sample`; ≥ 0). */
  @minValue(0)
  pid?: int32;

  /**
   * Foreground window title (required for `fg.sample`).
   * Bound is characters/runes (256), not UTF-8 bytes. Longer values are
   * truncated to FactTitleMaxLen runes at ingest (never mid-code-point).
   */
  @maxLength(FactTitleMaxLen)
  title?: string;

  /** Seconds the user has been idle (required for `fg.sample`; ≥ 0). */
  @minValue(0)
  idle_s?: int32;

  /** Count of dropped fact batches (`queue.dropped`). */
  @minValue(0)
  dropped_batches?: int32;

  /** Bytes dropped from the local fact queue (`queue.dropped`). */
  @minValue(0)
  dropped_bytes?: int64;
}

/** One fact in an AgentAPI ingest batch. */
@friendlyName("FactItem")
model FactItem {
  /** Fact wall-clock time (RFC 3339 UTC). */
  ts: utcDateTime;

  /** Fact kind discriminator. */
  kind: FactKind;

  /** Plugin / collector id that produced this fact (1..32 characters). */
  @minLength(1)
  @maxLength(32)
  plugin: string;

  /** Optional Windows user SID (≤ 184 characters). */
  @maxLength(184)
  user_sid?: string;

  /**
   * Duration this sample covers in seconds (0..300).
   * Zero is allowed for instantaneous markers.
   */
  @minValue(0)
  @maxValue(FactDurMaxSeconds)
  dur_s: int32;

  /** Kind-specific payload (flat optional members). */
  data: FactData;
}

/** AgentAPI request body for batch facts ingest. */
@friendlyName("PostFactsRequest")
model PostFactsRequest {
  /** Reporting machine identity (RFC 4122 UUIDv4). */
  @format("uuid")
  machine_id: string;

  /** Monotonic stream identity for this agent process/run (RFC 4122 UUIDv4). */
  @format("uuid")
  stream_id: string;

  /**
   * Per-batch sequence number within `stream_id` (monotonic, ≥ 1).
   * Retries re-send the same `(stream_id, seq)` with an identical body.
   */
  @minValue(1)
  seq: int64;

  /** Client wall-clock when the batch was sent (RFC 3339 UTC). */
  sent_at: utcDateTime;

  /** IANA time zone id for machine-local day boundaries (1..64 characters). */
  @minLength(1)
  @maxLength(64)
  time_zone: string;

  /**
   * UTC offset of the machine-local zone at `sent_at`, in minutes (−840..840).
   * Cross-check for a wrong Windows→IANA mapping.
   * Required (must be present and non-null). Modeled as `int32 | null` so
   * generated Go bindings use `*int32` and omitted/null values bind to nil
   * instead of silently becoming 0; the server rejects nil.
   */
  @minValue(-840)
  @maxValue(840)
  utc_offset_minutes: int32 | null;

  /**
   * Compact batch of facts (hard-capped at 500 items).
   * Empty array is accepted as a no-op success.
   */
  @maxItems(FactBatchMaxCount)
  facts: FactItem[];
}

/** AgentAPI acknowledgement for a facts batch. */
@friendlyName("FactsAck")
model FactsAck {
  /** Sequence number that was accepted (echo of request `seq`). */
  @minValue(1)
  accepted_seq: int64;

  /** True when `(stream_id, seq)` was already accepted (idempotent replay). */
  duplicate: boolean;
}

/** Per-app usage totals for one machine-local calendar day. */
@friendlyName("AppUsage")
model AppUsage {
  /** Executable / app key (e.g. image name). */
  exe: string;

  /** Total active seconds attributed to this app (≥ 0). */
  @minValue(0)
  seconds: int64;

  /** Foreground seconds attributed to this app (≥ 0). */
  @minValue(0)
  fg_seconds: int64;
}

/** ControlAPI response for one machine-local day of usage. */
@friendlyName("MachineUsage")
model MachineUsage {
  /** Calendar day that was queried (`YYYY-MM-DD`). */
  date: plainDate;

  /** IANA time zone used to bound that day (from ingested facts/heartbeats). */
  time_zone: string;

  /** Per-app totals for the day (empty list when none). */
  apps: AppUsage[];
}
