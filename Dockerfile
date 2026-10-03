# Stage 1: Builder
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=1.0.0
ARG GIT_COMMIT=dev
ARG BUILD_DATE=now
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.Version=${VERSION} \
              -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.GitCommit=${GIT_COMMIT} \
              -X github.com/mrpk1906/haproxy-metrics-spoa/internal/version.BuildDate=${BUILD_DATE}" \
    -o /bin/haproxy-metrics-spoa ./cmd/spoa
RUN CGO_ENABLED=0 GOOS=linux go build \
    -o /bin/mock-backend ./test/e2e/mockbackend

# Stage 2: Production daemon image
FROM alpine:3.21 AS daemon
RUN apk --no-cache add ca-certificates tzdata
RUN mkdir -p /var/run/haproxy && chmod 777 /var/run/haproxy
COPY --from=builder /bin/haproxy-metrics-spoa /usr/local/bin/haproxy-metrics-spoa
ENTRYPOINT ["/usr/local/bin/haproxy-metrics-spoa"]

# Stage 3: Mock backend image
FROM alpine:3.21 AS mock-backend
COPY --from=builder /bin/mock-backend /usr/local/bin/mock-backend
ENTRYPOINT ["/usr/local/bin/mock-backend"]
