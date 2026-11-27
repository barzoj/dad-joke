import "@typespec/http";
import "@typespec/openapi";
import "../common/errors.tsp";

using TypeSpec.Http;
using TypeSpec.OpenAPI;
using DadJoke.Common;

/**
 * ControlAPI: HTTP surface of the control plane / product logic.
 * Source of truth is this TypeSpec; OpenAPI 3.0.0 and Go are generated.
 *
 * Authenticated routes require HTTP Bearer credentials with the `dj_pat_`
 * prefix (256-bit entropy). `GET /healthz` remains unauthenticated.
 *
 * Agent tokens (`dj_agt_...`) are issued here for AgentAPI; their raw secret
 * appears only in the 201 Created response models.
 */
@service(#{ title: "ControlAPI" })
@info(#{ version: "0.1.0" })
@useAuth(ControlBearerAuth)
namespace ControlAPI;

/**
 * HTTP Bearer security scheme for control-plane / PAT credentials (`dj_pat_...`).
 * Distinct from AgentAPI's AgentBearerAuth (`dj_agt_...`).
 */
model ControlBearerAuth {
  type: AuthType.http;
  scheme: "Bearer";
}

/** Liveness payload for GET /healthz. */
model HealthResponse {
  /** Always "ok" when the process can serve HTTP. */
  status: "ok";
}

/** Request to issue a new agent token (`dj_agt_...`). */
model CreateAgentTokenRequest {
  /** Human-readable label for the token. */
  name: string;

  /** Optional expiry; omit or null for no expiry. */
  expires_at?: utcDateTime | null;
}

/**
 * Agent token metadata returned by list/get-style responses.
 * Never includes the raw secret.
 */
model AgentTokenSummary {
  /** Stable token id. */
  @format("uuid")
  id: string;

  /** Human-readable label. */
  name: string;

  /** Non-secret identifying prefix (includes `dj_agt_`). */
  prefix: string;

  /** When the token was issued. */
  created_at: utcDateTime;

  /** Last successful authentication, if any. */
  last_used_at: utcDateTime | null;

  /** When the token was revoked, if revoked. */
  revoked_at: utcDateTime | null;

  /** Optional expiry timestamp. */
  expires_at: utcDateTime | null;
}

/**
 * Agent token creation response. The raw `token` secret appears ONLY here
 * (201 Created) and must never appear on GET list/summary models.
 */
model CreatedAgentToken {
  /** Stable token id. */
  @format("uuid")
  id: string;

  /** Human-readable label. */
  name: string;

  /** Non-secret identifying prefix (includes `dj_agt_`). */
  prefix: string;

  /**
   * Raw secret credential (`dj_agt_` + 256-bit entropy).
   * Returned exactly once at creation; store hashed at rest thereafter.
   */
  @secret
  token: string;

  /** When the token was issued. */
  created_at: utcDateTime;

  /** Last successful authentication (null at creation). */
  last_used_at: utcDateTime | null;

  /** Optional expiry timestamp. */
  expires_at: utcDateTime | null;
}

/** Request to issue a new control-plane API key (`dj_pat_...`). */
model CreateApiKeyRequest {
  /** Human-readable label for the key. */
  name: string;

  /** Optional expiry; omit or null for no expiry. */
  expires_at?: utcDateTime | null;
}

/**
 * API key metadata for list responses. Never includes the raw secret.
 */
model ApiKeySummary {
  /** Stable key id. */
  @format("uuid")
  id: string;

  /** Human-readable label. */
  name: string;

  /** Non-secret identifying prefix (includes `dj_pat_`). */
  prefix: string;

  /** When the key was issued. */
  created_at: utcDateTime;

  /** Last successful authentication, if any. */
  last_used_at: utcDateTime | null;

  /** When the key was revoked, if revoked. */
  revoked_at: utcDateTime | null;

  /** Optional expiry timestamp. */
  expires_at: utcDateTime | null;
}

/**
 * API key creation response. The raw `token` secret appears ONLY here
 * (201 Created) and must never appear on GET list/summary models.
 */
model CreatedApiKey {
  /** Stable key id. */
  @format("uuid")
  id: string;

  /** Human-readable label. */
  name: string;

  /** Non-secret identifying prefix (includes `dj_pat_`). */
  prefix: string;

  /**
   * Raw secret credential (`dj_pat_` + 256-bit entropy).
   * Returned exactly once at creation; store hashed at rest thereafter.
   */
  @secret
  token: string;

  /** When the key was issued. */
  created_at: utcDateTime;

  /** Last successful authentication (null at creation). */
  last_used_at: utcDateTime | null;

  /** Optional expiry timestamp. */
  expires_at: utcDateTime | null;
}

/** Connectivity status derived from recent heartbeats. */
enum MachineStatus {
  online,
  offline,
}

/** Enrolled machine metadata visible to the control plane. */
model Machine {
  /** Persistent machine identity (RFC 4122 UUIDv4). */
  @format("uuid")
  machine_id: string;

  /** Last reported host name. */
  hostname: string;

  /** Last reported OS version. */
  os_version: string;

  /** Timestamp of the most recent successful heartbeat. */
  last_seen_at: utcDateTime;

  /** Last reported agent binary version. */
  agent_version: string;

  /** Policy version the agent last reported as applied. */
  @minValue(0)
  applied_policy_version: int32;

  /** Policy version the control plane currently desires. */
  @minValue(0)
  desired_policy_version: int32;

  /** online if recently heartbeating; otherwise offline. */
  status: MachineStatus;
}

@route("/healthz")
@useAuth(NoAuth)
interface Health {
  /**
   * Liveness probe. Returns 200 with a JSON body when the control HTTP surface is up.
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

@route("/v1/agent-tokens")
interface AgentTokens {
  /**
   * Issue a new agent token (`dj_agt_...`, 256-bit entropy).
   * Raw secret is returned only in this 201 response.
   */
  @post
  @operationId("createAgentToken")
  op create(@body body: CreateAgentTokenRequest): {
    @statusCode _: 201;
    @header contentType: "application/json";
    @body body: CreatedAgentToken;
  } | StandardErrors;

  /**
   * List agent tokens for the tenant (metadata only; no raw secrets).
   */
  @get
  @operationId("listAgentTokens")
  op list(): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: AgentTokenSummary[];
  } | StandardErrors;

  /**
   * Revoke/delete an agent token by id.
   */
  @delete
  @route("{id}")
  @operationId("deleteAgentToken")
  op delete(
    /** Token id. */
    @path @format("uuid") id: string
  ): {
    @statusCode _: 204;
  } | StandardErrors;
}

@route("/v1/api-keys")
interface ApiKeys {
  /**
   * Issue a new control-plane API key (`dj_pat_...`, 256-bit entropy) for MCP/automation.
   * Raw secret is returned only in this 201 response.
   */
  @post
  @operationId("createApiKey")
  op create(@body body: CreateApiKeyRequest): {
    @statusCode _: 201;
    @header contentType: "application/json";
    @body body: CreatedApiKey;
  } | StandardErrors;

  /**
   * List API keys for the tenant (metadata only; no raw secrets).
   */
  @get
  @operationId("listApiKeys")
  op list(): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: ApiKeySummary[];
  } | StandardErrors;

  /**
   * Revoke/delete an API key by id.
   */
  @delete
  @route("{id}")
  @operationId("deleteApiKey")
  op delete(
    /** API key id. */
    @path @format("uuid") id: string
  ): {
    @statusCode _: 204;
  } | StandardErrors;
}

@route("/v1/machines")
interface Machines {
  /**
   * List enrolled machines and their heartbeat-derived status.
   */
  @get
  @operationId("listMachines")
  op list(): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: Machine[];
  } | StandardErrors;

  /**
   * Get a single enrolled machine by id.
   */
  @get
  @route("{machine_id}")
  @operationId("getMachine")
  op get(
    /** Machine id (UUIDv4). */
    @path @format("uuid") machine_id: string
  ): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: Machine;
  } | StandardErrors;
}
