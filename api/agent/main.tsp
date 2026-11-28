import "@typespec/http";
import "@typespec/openapi";
import "../common/errors.tsp";
import "../common/events.tsp";
import "../common/facts.tsp";
import "../common/jobs.tsp";
import "../common/policy.tsp";

using TypeSpec.Http;
using TypeSpec.OpenAPI;
using DadJoke.Common;

/**
 * AgentAPI: contract between the control plane and the on-device agent.
 * Source of truth is this TypeSpec; OpenAPI 3.0.0 and Go are generated.
 *
 * Authenticated routes require HTTP Bearer credentials with the `dj_agt_`
 * prefix (256-bit entropy). `GET /healthz` remains unauthenticated.
 */
@service(#{ title: "AgentAPI" })
@info(#{ version: "0.1.0" })
@useAuth(AgentBearerAuth)
namespace AgentAPI;

/**
 * HTTP Bearer security scheme for agent tokens (`dj_agt_...`).
 * Distinct from ControlAPI's ControlBearerAuth (`dj_pat_...`).
 */
model AgentBearerAuth {
  type: AuthType.http;
  scheme: "Bearer";
}

/** Liveness payload for GET /healthz. */
model HealthResponse {
  /** Always "ok" when the process can serve HTTP. */
  status: "ok";
}

/**
 * Heartbeat request from an enrolled agent.
 * `machine_id` is the persistent RFC 4122 UUIDv4 from local config.
 */
model HeartbeatRequest {
  /** Persistent machine identity (RFC 4122 UUIDv4). */
  @format("uuid")
  machine_id: string;

  /** Agent binary version string (e.g. semver or build id). */
  agent_version: string;

  /** Host name reported by the agent OS. */
  hostname: string;

  /** OS version string reported by the agent. */
  os_version: string;

  /** Policy version the agent has successfully applied locally. */
  @minValue(0)
  applied_policy_version: int32;

  /** IANA time zone id for machine-local day boundaries (1..64 characters). */
  @minLength(1)
  @maxLength(64)
  time_zone: string;

  /**
   * UTC offset of the machine-local zone at heartbeat time, in minutes (−840..840).
   * Cross-check for a wrong Windows→IANA mapping.
   */
  @minValue(-840)
  @maxValue(840)
  utc_offset_minutes: int32;
}

/** Heartbeat acknowledgement and next-desired-state hints. */
model HeartbeatResponse {
  /** Server wall-clock time (RFC 3339 UTC). */
  server_time: utcDateTime;

  /** Policy version the control plane wants applied. */
  @minValue(0)
  desired_policy_version: int32;

  /** Job ids waiting for this machine (opaque strings). */
  pending_job_ids: string[];
}

@route("/healthz")
@useAuth(NoAuth)
interface Health {
  /**
   * Liveness probe. Returns 200 with a JSON body when the agent HTTP surface is up.
   * Unauthenticated.
   */
  @get
  @operationId("getHealthz")
  op getHealthz(): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: HealthResponse;
  };
}

@route("/v1/heartbeat")
interface Heartbeat {
  /**
   * Agent liveness + applied policy report.
   * Requires AgentBearerAuth (`Authorization: Bearer dj_agt_...`).
   */
  @post
  @operationId("postHeartbeat")
  op postHeartbeat(@body body: HeartbeatRequest): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: HeartbeatResponse;
  } | StandardErrors;
}

@route("/v1/policy")
interface Policy {
  /**
   * Fetch the desired low-level policy for a machine in the authenticated tenant.
   * Requires AgentBearerAuth (`Authorization: Bearer dj_agt_...`).
   * `machine_id` must identify a machine that has a stored policy for this
   * tenant; missing or cross-tenant ids return 404 (never an empty policy).
   * Control-plane credentials (`dj_pat_` / session) are rejected with 401.
   */
  @get
  @operationId("getPolicy")
  op getPolicy(
    /** Persistent machine identity (RFC 4122 UUIDv4). */
    @query @format("uuid") machine_id: string
  ): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: MachinePolicy;
  } | StandardErrors;
}

@route("/v1/events")
interface Events {
  /**
   * Ingest a compact batch of agent telemetry events.
   * Requires AgentBearerAuth (`Authorization: Bearer dj_agt_...`).
   *
   * Hard limits: max 100 events (`400` when exceeded) and max 256 KiB
   * request body (`413`). Validation is all-or-nothing before persist;
   * duplicate `event_id` values are skipped idempotently (within and
   * across batches). Malformed or excessively future-dated `ts`
   * (> 5 minutes ahead of server clock) → `400`.
   */
  @post
  @operationId("postEvents")
  op postEvents(@body body: PostEventsRequest): {
    @statusCode _: 204;
  } | StandardErrors;
}

/** Documented failure statuses for AgentAPI facts ingest. */
alias FactsIngestErrors = BadRequestError
  | UnauthorizedError
  | PayloadTooLargeError
  | TooManyRequestsError
  | ServiceUnavailableError;

@route("/v1/facts")
interface Facts {
  /**
   * Ingest a compact batch of raw agent facts (session/process/fg samples).
   * Requires AgentBearerAuth (`Authorization: Bearer dj_agt_...`).
   *
   * Hard limits: max 500 facts (`400` when exceeded) and max 256 KiB
   * request body (`413`). `(stream_id, seq)` is the idempotency key;
   * identical retries return `duplicate:true`. Empty `facts` is a
   * no-op success. See `api/common/facts.tsp` for per-kind data rules.
   */
  @post
  @operationId("postFacts")
  op postFacts(@body body: PostFactsRequest): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: FactsAck;
  } | FactsIngestErrors;
}

@route("/v1/jobs")
interface Jobs {
  /**
   * Atomically claim a pending job for this agent machine (`pending → running`).
   * Second claim, wrong machine, expired, or terminal jobs → 409/404 as documented.
   * Cross-tenant ids return 404 (never 403).
   */
  @post
  @route("{id}/claim")
  @operationId("claimJob")
  op claim(
    /** Job id. */
    @path @format("uuid") id: string,
    @body body: ClaimJobRequest
  ): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: Job;
  } | StandardErrors;

  /**
   * Submit the terminal result of a previously claimed job.
   * Results for canceled/finished jobs → 409. Cross-tenant → 404.
   * stdout/stderr over 64 KiB → 413/400.
   */
  @post
  @route("{id}/result")
  @operationId("postJobResult")
  op postResult(
    /** Job id. */
    @path @format("uuid") id: string,
    @body body: JobResultRequest
  ): {
    @statusCode _: 204;
  } | StandardErrors;
}
