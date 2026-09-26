FROM golang:1.26.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/jev-mcp ./cmd/jev-mcp

FROM alpine:3.22
ARG TARGETARCH
RUN apk add --no-cache ca-certificates curl && \
    curl --fail --location --silent --show-error -o /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/v1.34.0/bin/linux/${TARGETARCH}/kubectl" && \
    curl --fail --location --silent --show-error -o /tmp/kubectl.sha256 \
      "https://dl.k8s.io/release/v1.34.0/bin/linux/${TARGETARCH}/kubectl.sha256" && \
    echo "$(cat /tmp/kubectl.sha256)  /usr/local/bin/kubectl" | sha256sum -c - && \
    chmod 0755 /usr/local/bin/kubectl && \
    addgroup -S knull && adduser -S -G knull -u 10001 knull
COPY --from=build /out/api /usr/local/bin/knull-api
COPY --from=build /out/jev-mcp /usr/local/bin/knull-jev-mcp
USER 10001:10001
EXPOSE 8080 8090
ENTRYPOINT ["/usr/local/bin/knull-api"]
