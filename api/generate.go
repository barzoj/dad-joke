// Package api holds go:generate directives for OpenAPI → Go codegen.
// TypeSpec compilation is orchestrated by `make generate` (npm + tsp) before
// these directives run.
package api

//go:generate go tool oapi-codegen -config oapi-codegen/agent.yaml ../gen/openapi/agent/openapi.yaml
//go:generate go tool oapi-codegen -config oapi-codegen/control.yaml ../gen/openapi/control/openapi.yaml
