# syntax=docker/dockerfile:1

# Frontend: build the React/Vite SPA. Done fresh here rather than trusting
# src/webui/static/dist as checked out — that directory is gitignored and
# only regenerated locally when a developer remembers to (see
# src/webui/frontend/README.md), so the image needs to be reproducible from
# source alone.
FROM node:22-alpine AS frontend
WORKDIR /app/src/webui/frontend
COPY src/webui/frontend/package.json src/webui/frontend/package-lock.json ./
RUN npm ci
COPY src/webui/frontend/ ./
RUN npm run build

# Build: compile the Go binary. No cgo dependencies in this module (aws-sdk-go-v2,
# pgx/v5, grpc are all pure Go), so this is a static binary.
FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/src/webui/static/dist ./src/webui/static/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /bepper .
# newBlobstore's disk fallback (src/blobstore/disk) writes to a relative
# "data/events" dir under the process's cwd. Pre-create it owned by
# distroless's standard nonroot UID/GID (65532) so a named volume mounted
# here inherits writable ownership on first run — the final stage has no
# shell to chown at container start.
RUN mkdir -p /staging/data/events && chown -R 65532:65532 /staging/data

# Final: distroless, no shell/package manager. The "static" variant still
# includes CA certs, needed for HTTPS calls to the OIDC issuer and S3 (when
# configured).
FROM gcr.io/distroless/static-debian12:nonroot AS final
WORKDIR /app
COPY --from=build /bepper /bepper
COPY --from=build --chown=65532:65532 /staging/data ./data
EXPOSE 1985 8080
USER 65532:65532
ENTRYPOINT ["/bepper"]
