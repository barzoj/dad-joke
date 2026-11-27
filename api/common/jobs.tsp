import "@typespec/http";

using TypeSpec.Http;

/**
 * Shared one-shot job queue shapes for AgentAPI and ControlAPI.
 * Each service imports this file; OpenAPI outputs remain independent.
 */
namespace DadJoke.Common;

/** Maximum UTF-8 byte length for job payload and result stdout/stderr. */
const JobIOMaxBytes = 65536;

/** Default agent-side hard timeout when timeout_seconds is omitted (seconds). */
const JobTimeoutDefaultSeconds = 60;

/** Maximum agent-side hard timeout (seconds). */
const JobTimeoutMaxSeconds = 300;

/** Supported remote script interpreters the Windows agent may execute. */
enum JobType {
  powershell,
  cmd,
}

/** Job state machine status. */
enum JobStatus {
  pending,
  running,
  succeeded,
  failed,
  canceled,
  timed_out,
}

/**
 * One-shot remote command/script assignment visible to control plane and agent.
 */
@friendlyName("Job")
model Job {
  /** Stable job id. */
  @format("uuid")
  id: string;

  /** Target machine (RFC 4122 UUIDv4). */
  @format("uuid")
  machine_id: string;

  /** Current status in the job state machine. */
  status: JobStatus;

  /** Interpreter the agent must use (`powershell` or `cmd`). */
  type: JobType;

  /** Command/script payload (hard-capped at 64 KiB). */
  @maxLength(JobIOMaxBytes)
  payload: string;

  /**
   * Optional hard timeout in seconds (1..300). Null/omitted → agent default 60s.
   * Agent clamps values above 300 to 300.
   */
  timeout_seconds: int32 | null;

  /** Process exit code when finished (null while pending/running). */
  exit_code: int32 | null;

  /** Captured stdout (hard-capped at 64 KiB). */
  @maxLength(JobIOMaxBytes)
  stdout: string;

  /** Captured stderr (hard-capped at 64 KiB). */
  @maxLength(JobIOMaxBytes)
  stderr: string;

  /** Wall-clock duration in milliseconds when finished. */
  duration_ms: int64 | null;

  /** When the job was enqueued. */
  created_at: utcDateTime;

  /** Mandatory claim TTL; unclaimed jobs expire to timed_out. */
  expires_at: utcDateTime;

  /** When an agent claimed the job, if claimed. */
  claimed_at: utcDateTime | null;

  /** When the job reached a terminal status, if finished. */
  finished_at: utcDateTime | null;
}

/** Control-plane request to enqueue a one-shot job for a machine. */
@friendlyName("CreateJobRequest")
model CreateJobRequest {
  /** Interpreter the agent must use (`powershell` or `cmd`). */
  type: JobType;

  /** Command/script payload (hard-capped at 64 KiB). */
  @maxLength(JobIOMaxBytes)
  payload: string;

  /** Absolute UTC expiry for unclaimed jobs (mandatory). */
  expires_at: utcDateTime;

  /**
   * Optional hard timeout in seconds (1..300).
   * Omitted → agent default 60s; agent clamps above 300 to 300.
   */
  @minValue(1)
  @maxValue(JobTimeoutMaxSeconds)
  timeout_seconds?: int32;
}

/** Agent request to atomically claim a pending job. */
@friendlyName("ClaimJobRequest")
model ClaimJobRequest {
  /** Claiming machine identity (RFC 4122 UUIDv4); must match the job. */
  @format("uuid")
  machine_id: string;
}

/** Terminal outcome statuses an agent may report. */
enum JobResultStatus {
  succeeded,
  failed,
  timed_out,
}

/** Agent request to submit the result of a claimed job. */
@friendlyName("JobResultRequest")
model JobResultRequest {
  /** Completing machine identity (RFC 4122 UUIDv4); must match the job. */
  @format("uuid")
  machine_id: string;

  /** Terminal status reported by the agent. */
  status: JobResultStatus;

  /** Process exit code. */
  exit_code?: int32 | null;

  /** Captured stdout (hard-capped at 64 KiB). */
  @maxLength(JobIOMaxBytes)
  stdout?: string;

  /** Captured stderr (hard-capped at 64 KiB). */
  @maxLength(JobIOMaxBytes)
  stderr?: string;

  /** Wall-clock duration in milliseconds. */
  duration_ms?: int64 | null;
}
