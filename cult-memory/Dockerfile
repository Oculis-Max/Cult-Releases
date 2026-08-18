FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/cult-memory .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tar && rm -rf /var/lib/apt/lists/*
ARG CULT_VERSION=1.0.0
RUN set -eux; \
    curl -fL "https://github.com/Oculis-Max/Cult-Releases/releases/download/v${CULT_VERSION}/cult-${CULT_VERSION}-linux-amd64.tar.gz" -o /tmp/cult.tar.gz; \
    mkdir -p /tmp/cult; \
    tar -xzf /tmp/cult.tar.gz -C /tmp/cult; \
    CULT_PATH="$(find /tmp/cult -type f -name cult | head -n 1)"; \
    test -n "$CULT_PATH"; \
    install -m 0755 "$CULT_PATH" /usr/local/bin/cult; \
    rm -rf /tmp/cult /tmp/cult.tar.gz

WORKDIR /app
COPY --from=build /out/cult-memory /usr/local/bin/cult-memory
COPY workflow.cult /app/workflow.cult
ENV PORT=8080 \
    CULT_BIN=/usr/local/bin/cult \
    CULT_WORKFLOW=/app/workflow.cult \
    CULT_REQUIRED=true \
    AWS_REGION=ap-south-1 \
    BEDROCK_MODEL_ID=amazon.nova-lite-v1:0 \
    BEDROCK_EMBED_MODEL_ID=amazon.titan-embed-text-v2:0
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/cult-memory"]
