import "@typespec/http";

using TypeSpec.Http;

/**
 * Shared low-level policy document shapes for AgentAPI and ControlAPI.
 * Each service imports this file; OpenAPI outputs remain independent.
 */
namespace DadJoke.Common;

/**
 * Versioned low-level desired-state policy for one machine.
 * `document` is an opaque JSON object; product-level quotas are out of scope.
 */
@friendlyName("MachinePolicy")
model MachinePolicy {
  /** Monotonic policy version (1 on first write). */
  @minValue(1)
  version: int32;

  /** Opaque low-level desired-state JSON object. */
  document: Record<unknown>;

  /** When this version was written. */
  updated_at: utcDateTime;
}

/**
 * Control-plane request to create or replace a machine's desired policy.
 * `expected_version` must be 0 for the first write, or equal the current
 * stored version for an update (otherwise 409 Conflict).
 */
@friendlyName("PutMachinePolicyRequest")
model PutMachinePolicyRequest {
  /** Expected current version; 0 creates version 1. */
  @minValue(0)
  expected_version: int32;

  /** Opaque low-level desired-state JSON object. */
  document: Record<unknown>;
}
