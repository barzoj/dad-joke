import "@typespec/http";
import "@typespec/openapi";
import "../common/errors.tsp";

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
