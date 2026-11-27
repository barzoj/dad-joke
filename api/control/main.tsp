import "@typespec/http";
import "@typespec/openapi";

using TypeSpec.Http;
using TypeSpec.OpenAPI;

/**
 * ControlAPI: HTTP surface of the control plane / product logic.
 * Source of truth is this TypeSpec; OpenAPI 3.0.0 and Go are generated.
 */
@service(#{ title: "ControlAPI" })
@info(#{ version: "0.1.0" })
namespace ControlAPI;

/** Liveness payload for GET /healthz. */
model HealthResponse {
  /** Always "ok" when the process can serve HTTP. */
  status: "ok";
}

@route("/healthz")
interface Health {
  /**
   * Liveness probe. Returns 200 with a JSON body when the control HTTP surface is up.
   */
  @get
  @operationId("getHealthz")
  op getHealthz(): {
    @statusCode _: 200;
    @header contentType: "application/json";
    @body body: HealthResponse;
  };
}
