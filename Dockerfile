FROM golang:1.25-alpine AS build

WORKDIR /app

RUN apk add --no-cache tzdata

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

RUN --mount=type=cache,target=/go/pkg/mod \
    go install github.com/swaggo/swag/cmd/swag@v1.16.6

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    /go/bin/swag init -g internal/modules/rest/rest.go -o docs

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -v -o bilirec

FROM alpine:latest
WORKDIR /app

RUN apk update && \
    apk add --no-cache ffmpeg \
    && rm -rf /var/cache/apk/*

COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /app/bilirec .
COPY --from=build /app/docs ./docs
RUN chmod +x ./bilirec

ENV TZ=Asia/Hong_Kong

ENV ANONYMOUS_LOGIN=false \
    PORT=8080 \
    MAX_CONCURRENT_RECORDINGS=3 \
    MAX_RECORDING_HOURS=5 \
    MAX_RECOVERY_ATTEMPTS=5 \
    MAX_RETRY_MINUTES=10 \
    OUTPUT_DIR=records \
    SECRET_DIR=secrets \
    DATABASE_DIR=database \
    CONVERT_TO_MP4=false \
    DELETE_SOURCE_AFTER_CONVERT=false \
    CLOUDCONVERT_THRESHOLD=1073741824 \
    CLOUDCONVERT_CHECK_INTERVAL_SECS=180 \
    CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS=1 \
    FFMPEG_CHECK_INTERVAL_SECS=60 \
    FFMPEG_MAX_CONCURRENT_TASKS=1 \
    FFMPEG_ALLOW_DURING_RECORDING=false \
    FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS=1 \
    MIN_DISK_SPACE_BYTES=5368709120 \
    FRONTEND_URL=http://localhost:8080 \
    BACKEND_HOST=localhost:8080 \
    WEBPUSH_SUBSCRIBER=mailto:webpush@example.com \
    JWT_SECRET=bilirec_secret \
    DEBUG=false \
    PRODUCTION_MODE=false \
    SILENT_ACCESS_LOG=false \
    UPLOAD_BUFFER_SIZE=5242880 \
    DOWNLOAD_BUFFER_SIZE=5242880 \
    STREAM_WRITER_BUFFER_SIZE=1048576 \
    LIVE_STREAM_WRITER_BUFFER_SIZE=8388608 \
    LIVE_STREAM_WRITER_SYNC_PERIOD_SECS=0 \
    LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS=10 \
    LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE=64 \
    LIVE_STREAM_WRITER_BYTES_POOL_SIZE=524288 \
    SKIP_SMALL_FLUSH=true \
    SEQUENTIAL_WRITE=true

ENV GOMEMLIMIT=768MiB
ENV GOGC=100

ENTRYPOINT ["./bilirec"]