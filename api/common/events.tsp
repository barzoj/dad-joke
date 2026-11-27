import "@typespec/http";

using TypeSpec.Http;

/**
 * Shared agent telemetry / event shapes for AgentAPI and ControlAPI.
 * Each service imports this file; OpenAPI outputs remain independent.
 *
 * Batch ingest semantics (AgentAPI POST /v1/events):
 * - Hard limits: max 100 events per batch; max 256 KiB request body.
 *   Oversized event count → 400; oversized body → 413.
 * - Validation is all-or-nothing: every event in the batch is validated
 *   before any persist. A single malformed / future-dated `ts` rejects
 *   the entire batch with 400 and writes nothing.
 * - `event_id` is a client idempotency key. Resubmitting the same
 *   `(tenant_id, machine_id, event_id)` (across batches or within one
 *   batch) is a no-op success — duplicates are skipped, never duplicated.
 * - `ts` must be a valid RFC 3339 timestamp and must not be more than
 *   5 minutes ahead of the server clock (injectable in tests).
 *
 * ControlAPI listing is strictly tenant-scoped and ordered by `ts`
 * ascending with `event_id` as a deterministic tie-breaker. Cursor
 * pagination is opaque and resumes after the last returned event.
 */
namespace DadJoke.Common;

/** Maximum events accepted in one AgentAPI ingest batch. */
const EventBatchMaxCount = 100;

/** Maximum UTF-8 byte length of the POST /v1/events request body. */
const EventBatchMaxBytes = 262144;

/** Default page size for ControlAPI event listing. */
const EventListDefaultLimit = 50;

/** Minimum allowed `limit` for ControlAPI event listing. */
const EventListMinLimit = 1;

/** Maximum allowed `limit` for ControlAPI event listing. */
const EventListMaxLimit = 100;

/**
 * One telemetry event in an AgentAPI ingest batch.
 * `event_id` is the client-supplied idempotency key.
 */
@friendlyName("EventIngestItem")
model EventIngestItem {
  /** Client-supplied idempotency key (unique per machine within a tenant). */
  @minLength(1)
  @maxLength(128)
  event_id: string;

  /** Event wall-clock time (RFC 3339 UTC). Must not be > 5 minutes ahead of server clock. */
  ts: utcDateTime;

  /** Event type (e.g. start, update, policy_change, error). */
  @minLength(1)
  @maxLength(64)
  type: string;

  /** Opaque JSON payload (compact telemetry; not every ping). */
  payload: unknown;
}

/** AgentAPI request body for batch event ingest. */
@friendlyName("PostEventsRequest")
model PostEventsRequest {
  /** Reporting machine identity (RFC 4122 UUIDv4). */
  @format("uuid")
  machine_id: string;

  /**
   * Compact batch of events (hard-capped at 100 items).
   * Empty array is accepted as a no-op success.
   */
  @maxItems(EventBatchMaxCount)
  events: EventIngestItem[];
}

/** One stored event returned by ControlAPI listing. */
@friendlyName("MachineEvent")
model MachineEvent {
  /** Client-supplied idempotency key. */
  event_id: string;

  /** Event wall-clock time (RFC 3339 UTC). */
  ts: utcDateTime;

  /** Event type. */
  type: string;

  /** Opaque JSON payload. */
  payload: unknown;
}

/** ControlAPI paginated event list response. */
@friendlyName("EventListResponse")
model EventListResponse {
  /** Events matching the query, ordered by ts then event_id ascending. */
  events: MachineEvent[];

  /**
   * Opaque cursor for the next page. Omitted when there are no further results.
   * Pass as `cursor` on the subsequent list request.
   */
  next_cursor?: string | null;
}
