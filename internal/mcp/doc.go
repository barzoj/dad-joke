// Package mcp exposes a remote Streamable HTTP MCP server on top of ControlAPI.
//
// It talks only through the generated gen/go/controlapi HTTP client and must not
// import internal/store or go.mongodb.org/mongo-driver.
package mcp
