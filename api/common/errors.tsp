import "@typespec/http";

using TypeSpec.Http;

/**
 * Shared HTTP error contracts for AgentAPI and ControlAPI.
 * Each service imports this file; OpenAPI outputs remain independent
 * (no splice/merge of YAML).
 */
namespace DadJoke.Common;

/** Unified API error body for all documented failure statuses. */
@friendlyName("ErrorResponse")
model ErrorResponse {
  /** Machine-readable error code (stable for clients). */
  code: string;

  /** Human-readable description of the failure. */
  message: string;

  /** Correlation id for this request (also used in logs/audit). */
  request_id: string;
}

/** 400 Bad Request */
model BadRequestError {
  @statusCode _: 400;
  @header contentType: "application/json";
  @body body: ErrorResponse;
}

/** 401 Unauthorized */
model UnauthorizedError {
  @statusCode _: 401;
  @header contentType: "application/json";
  @body body: ErrorResponse;
}

/** 403 Forbidden */
model ForbiddenError {
  @statusCode _: 403;
  @header contentType: "application/json";
  @body body: ErrorResponse;
}

/** 404 Not Found */
model NotFoundError {
  @statusCode _: 404;
  @header contentType: "application/json";
  @body body: ErrorResponse;
}

/** 409 Conflict */
model ConflictError {
  @statusCode _: 409;
  @header contentType: "application/json";
  @body body: ErrorResponse;
}

/** 413 Payload Too Large */
model PayloadTooLargeError {
  @statusCode _: 413;
  @header contentType: "application/json";
  @body body: ErrorResponse;
}

/** 429 Too Many Requests (includes Retry-After). */
model TooManyRequestsError {
  @statusCode _: 429;
  @header contentType: "application/json";
  /**
   * Seconds (or HTTP-date) until the client may retry.
   * Emitted as the `Retry-After` response header.
   */
  @header("Retry-After") retryAfter: string;
  @body body: ErrorResponse;
}

/** 500 Internal Server Error */
model InternalServerError {
  @statusCode _: 500;
  @header contentType: "application/json";
  @body body: ErrorResponse;
}

/** 503 Service Unavailable */
model ServiceUnavailableError {
  @statusCode _: 503;
  @header contentType: "application/json";
  @body body: ErrorResponse;
}

/** Standard error union for authenticated mutating/read operations. */
alias StandardErrors = BadRequestError
  | UnauthorizedError
  | ForbiddenError
  | NotFoundError
  | ConflictError
  | PayloadTooLargeError
  | TooManyRequestsError
  | InternalServerError
  | ServiceUnavailableError;
