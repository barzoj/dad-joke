# Multi-stage build: TypeSpec + oapi-codegen → static Linux server binary →
# non-root distroless runtime. Works from a fresh git checkout (gen/ is gitignored).

# ---- codegen (Node TypeSpec → OpenAPI) ----
FROM node:22-bookworm AS codegen
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY api/ ./api/
# tspconfig paths write under /src/gen/openapi/{agent,control}/
RUN npm run generate:typespec

# ---- build (oapi-codegen + Go compile) ----
FROM golang:1.26-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=codegen /src/gen/openapi ./gen/openapi
RUN mkdir -p gen/go/agentapi gen/go/controlapi \
	&& go generate ./api \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/server /server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
