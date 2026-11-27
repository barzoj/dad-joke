import "@typespec/http";
import "@typespec/openapi";
import "../common/errors.tsp";
import "../common/events.tsp";
import "../common/jobs.tsp";
import "../common/policy.tsp";

using TypeSpec.Http;
using TypeSpec.OpenAPI;
using DadJoke.Common;

/**
 * ControlAPI: HTTP surface of the control plane / product logic.
 * Source of truth is this TypeSpec; OpenAPI 3.0.0 and Go are generated.
 *
 * Authenticated routes accept either HTTP Bearer `dj_pat_...` (256-bit entropy)
 * or a user session (`dj_ses_...` via Bearer and/or `dj_session` cookie).
 * `GET /healthz`, `POST /v1/auth/register`, and `POST /v1/auth/login` are
 * unauthenticated. Agent tokens (`dj_agt_...`) are rejected on this surface.
 *
 * Agent tokens (`dj_agt_...`) are issued here for AgentAPI; their raw secret
 * appears only in the 201 Created response models.
 */
@service(#{ title: "ControlAPI" })
@info(#{ version: "0.1.0" })
@useAuth(ControlBearerAuth)
namespace ControlAPI;

/**
 * HTTP Bearer security scheme for control-plane credentials: `dj_pat_...`
 * API keys or `dj_ses_...` user session tokens.
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

/** Email/password registration body. */
model RegisterRequest {
  /** Account email (normalized to lowercase). */
  @format("email")
  email: string;

  /** Password (hashed with argon2id at rest; never logged). */
  @minLength(8)
  password: string;

  /** Optional tenant display name; defaults to the normalized email. */
  tenant_name?: string;
}

/** Email/password login body. */
model LoginRequest {
  /** Account email. */
  @format("email")
  email: string;

  /** Password. */
  @minLength(1)
  password: string;
}

/** Authenticated user profile (never includes password_hash). */
model UserMe {
  /** Stable user id. */
  @format("uuid")
  id: string;

  /** Normalized email. */
  @format("email")
  email: string;

  /** Tenant the user belongs to. */
  @format("uuid")
  tenant_id: string;
}

/**
 * Session issued by register/login.
 * Raw `access_token` (`dj_ses_...`) is also set as an HttpOnly cookie.
 */
model AuthSession {
  /** Opaque session secret (`dj_ses_` + 256-bit entropy). */
  @secret
  access_token: string;

  /** Always "Bearer". */
  token_type: "Bearer";

  /** When the session expires. */
  expires_at: utcDateTime;

  /** Authenticated user (no password material). */
  user: UserMe;
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

@route("/v1/auth")
interface Auth {
  /**
   * Register a new email/password user and tenant; returns a working session.
   * Unauthenticated. Disabled with ALLOW_SIGNUP=false → 403.
   */
  @post
  @route("register")
  @useAuth(NoAuth)
  @operationId("register")
  op register(@body body: RegisterRequest): {
    @statusCode _: 201;
    @header contentType: "application/json";
    /** Session cookie: HttpOnly; SameSite=Lax; Secure (unless COOKIE_SECURE=false). */
    @header("Set-Cookie") setCookie: string;
    @body body: AuthSession;
  } | StandardErrors;

  /**
   * Log in with email/password; returns a working session.
   * Unauthenticated. Failed attempts are rate-limited (429 + Retry-After).
   */
  @post
  @route("login")
  @useAuth(NoAuth)
  @operationId("login")
  op login(@body body: LoginRequest): {
    @statusCode _: 200;
    @header contentType: "application/json";
    /** Session cookie: HttpOnly; SameSite=Lax; Secure (unless COOKIE_SECURE=false). */
    @header("Set-Cookie") setCookie: string;
    @body body: AuthSession;
  } | StandardErrors;

  /**
   * Invalidate the current user session (cookie and/or Bearer dj_ses_...).
   * Requires an authenticated user session (PAT alone is insufficient).
   */
  @post
  @route("logout")
  @operationId("logout")
  op logout(): {
    @statusCode _: 204;
    /** Clears the session cookie. */
    @header("Set-Cookie") setCookie: string;
  } | StandardErrors;

  /**
   * Return the authenticated user for the current user session
   * (cookie and/or Bearer dj_ses_...). Requires an authenticated user
   * session (PAT alone is insufficient → 401).
   */
  @get
  @route("me")
  @operationId("getMe")
  op me(): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: UserMe;
  } | StandardErrors;
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

  /**
   * Get the versioned low-level desired policy for a machine.
   * Missing policy (unknown machine_id or never written) → 404.
   */
  @get
  @route("{machine_id}/policy")
  @operationId("getMachinePolicy")
  op getPolicy(
    /** Machine id (UUIDv4). */
    @path @format("uuid") machine_id: string
  ): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: MachinePolicy;
  } | StandardErrors;

  /**
   * Create or replace the versioned low-level desired policy for a machine.
   * `expected_version` 0 creates version 1; otherwise the stored version must
   * match or the server returns 409 Conflict (lost-update protection).
   * Successful writes append an audit_logs entry and are visible immediately
   * via AgentAPI `POST /v1/heartbeat` (`desired_policy_version`).
   */
  @put
  @route("{machine_id}/policy")
  @operationId("putMachinePolicy")
  op putPolicy(
    /** Machine id (UUIDv4). */
    @path @format("uuid") machine_id: string,
    @body body: PutMachinePolicyRequest
  ): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: MachinePolicy;
  } | StandardErrors;

  /**
   * Enqueue a one-shot command/script job for a machine.
   * `payload` is hard-capped at 64 KiB; oversized → 413/400.
   * `expires_at` is mandatory (TTL for unclaimed jobs).
   * Successful create appends an audit_logs entry and is visible immediately
   * via AgentAPI `POST /v1/heartbeat` (`pending_job_ids`).
   */
  @post
  @route("{machine_id}/jobs")
  @operationId("createMachineJob")
  op createJob(
    /** Machine id (UUIDv4). */
    @path @format("uuid") machine_id: string,
    @body body: CreateJobRequest
  ): {
    @statusCode _: 201;
    @header contentType: "application/json";
    @body body: Job;
  } | StandardErrors;

  /**
   * List jobs for a machine (tenant-scoped). Empty list → `[]`.
   */
  @get
  @route("{machine_id}/jobs")
  @operationId("listMachineJobs")
  op listJobs(
    /** Machine id (UUIDv4). */
    @path @format("uuid") machine_id: string
  ): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: Job[];
  } | StandardErrors;

  /**
   * List telemetry events for a machine (strictly tenant-scoped).
   * Ordered by `ts` ascending with `event_id` as tie-breaker.
   * Cross-tenant `machine_id` returns an empty page (never another
   * tenant's events). `limit` must be 1..100 when provided (default 50);
   * invalid limit or cursor → `400`.
   */
  @get
  @route("{machine_id}/events")
  @operationId("listMachineEvents")
  op listEvents(
    /** Machine id (UUIDv4). */
    @path @format("uuid") machine_id: string,

    /** Optional event type filter (exact match). */
    @query type?: string,

    /** Inclusive lower bound on event `ts` (RFC 3339). */
    @query from?: utcDateTime,

    /** Exclusive upper bound on event `ts` (RFC 3339). */
    @query to?: utcDateTime,

    /**
     * Page size (1..100). Default 50 when omitted.
     * Values outside the range → `400`.
     */
    @query @minValue(1) @maxValue(100) limit?: int32,

    /** Opaque cursor from a previous `next_cursor` (resume after that event). */
    @query cursor?: string
  ): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: EventListResponse;
  } | StandardErrors;
}

@route("/v1/jobs")
interface Jobs {
  /**
   * Get a single job by id (tenant-scoped). Cross-tenant → 404.
   */
  @get
  @route("{id}")
  @operationId("getJob")
  op get(
    /** Job id. */
    @path @format("uuid") id: string
  ): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: Job;
  } | StandardErrors;

  /**
   * Cancel a pending job (`pending → canceled`).
   * Canceling running or terminal jobs → 409 Conflict (no silent overwrite).
   * Cross-tenant → 404.
   */
  @post
  @route("{id}/cancel")
  @operationId("cancelJob")
  op cancel(
    /** Job id. */
    @path @format("uuid") id: string
  ): {
    @statusCode _: 204;
  } | StandardErrors;
}
