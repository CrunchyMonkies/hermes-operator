# Build the hermes-reloader sidecar. It runs INSIDE the agent image so it has
# brew, python3, the hermes CLI, and the shared volume layout on PATH.
#
# Build context is the repository root:
#   docker build -f images/reloader/Dockerfile \
#     --build-arg AGENT_IMAGE=harbor.bne1.ouchi.com.au/applications/hermes-agent:<tag> .

# Global ARG so it is in scope for the final FROM below.
ARG AGENT_IMAGE=harbor.bne1.ouchi.com.au/applications/hermes-agent:v2026.5.29.2

FROM golang:1.26 AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /workspace
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o hermes-reloader ./cmd/reloader

# The reloader binary is layered onto the derived agent image.
FROM ${AGENT_IMAGE}
COPY --from=builder /workspace/hermes-reloader /usr/local/bin/hermes-reloader
USER 10000:10000
ENTRYPOINT [ "/usr/local/bin/hermes-reloader" ]
