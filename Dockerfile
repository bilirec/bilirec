FROM golang:1.26.3-alpine AS builder

WORKDIR /app

ARG TARGETARCH
ARG FRP_TOKEN_INJECTED=""
ARG PRODUCTION="false"

ENV GOCACHE=/root/.cache/go-build
ENV GOMODCACHE=/go/pkg/mod

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

RUN ls -lah /root/.cache/go-build || echo "No cache found"

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
        LDFLAGS="-X github.com/bilirec/bilirec/internal/modules/config.frpTokenInjected=$FRP_TOKEN_INJECTED"; \
        if [ "$PRODUCTION" = "true" ]; then \
            LDFLAGS="$LDFLAGS -s -w"; \
        fi; \
        GOOS=linux GOARCH=$TARGETARCH go build -v \
            -ldflags "$LDFLAGS" \
            -o bilirec ./cmd/backend

FROM alpine:latest AS ffmpeg-builder

RUN apk add --no-cache \
    build-base \
    nasm \
    yasm \
    git \
    tar \
    zlib-dev

WORKDIR /src
RUN git clone --depth 1 --branch n8.1.1 https://github.com/FFmpeg/FFmpeg.git .

# input accept: flv, mp4, mov, mpegts (ts), live_flv
# output: mp4, mov
RUN ./configure \
    --prefix=/build \
    --disable-everything \
    --disable-programs \
    --enable-ffmpeg \
    --enable-ffprobe \
    --enable-muxer=mp4,mov \
    --enable-demuxer=mov,mp4,flv,mpegts,live_flv \
    --enable-protocol=file \
    --enable-parser=h264,aac,hevc \
    --enable-bsf=aac_adtstoasc,h264_mp4toannexb,hevc_mp4toannexb \
    --disable-doc \
    --disable-htmlpages \
    --disable-manpages \
    --disable-podpages \
    --disable-txtpages \
    --disable-debug \
    --extra-cflags="-Os" \
    --extra-ldflags="-s"

RUN make -j$(nproc) && make install

RUN strip /build/bin/ffmpeg /build/bin/ffprobe

FROM alpine:latest
WORKDIR /app

COPY --from=ffmpeg-builder /build/bin/ffmpeg /usr/local/bin/
COPY --from=ffmpeg-builder /build/bin/ffprobe /usr/local/bin/

RUN ffmpeg -version && ffprobe -version

COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /app/bilirec .
COPY --from=builder /app/docs ./docs
RUN chmod +x ./bilirec

ENV TZ=Asia/Hong_Kong

ENV BILIBILI_LOGIN_MODE=controller \
    HOST= \
    PORT=8080 \
    TRUSTED_PROXIES=161.33.159.26 \
    FRP_ENABLED=false \
    FRP_SERVER=tunnel.bilirec.org:7000 \
    FRP_TOKEN= \
    FRP_BASE_DOMAIN=tunnel.bilirec.org \
    FRP_HTTPS=false \
    FRP_SCHEME_HTTPS=true \
    MAX_CONCURRENT_RECORDINGS=3 \
    MAX_RECORDING_HOURS=5 \
    MAX_RECOVERY_ATTEMPTS=15 \
    MAX_RETRY_MINUTES=10 \
    OUTPUT_DIR=records \
    SECRET_DIR=secrets \
    DATABASE_DIR=database \
    CONVERT_TO_MP4=false \
    DELETE_SOURCE_AFTER_CONVERT=false \
    NO_CONVERT_IF_INVALID=false \
    CLOUDCONVERT_THRESHOLD=1073741824 \
    CLOUDCONVERT_API_KEY= \
    CLOUDCONVERT_CHECK_INTERVAL_SECS=180 \
    CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS=1 \
    CLOUDCONVERT_ALLOW_DURING_RECORDING=false \
    CLOUDCONVERT_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS=1 \
    FFMPEG_CHECK_INTERVAL_SECS=60 \
    FFMPEG_MAX_CONCURRENT_TASKS=1 \
    FFMPEG_ALLOW_DURING_RECORDING=false \
    FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS=1 \
    SUBCHECK_ROOMS_PER_SHARD=50 \
    MIN_DISK_SPACE_BYTES=5368709120 \
    FRONTEND_URL=https://app.bilirec.org \
    PUBLIC_BASE_URL= \
    WEBPUSH_SUBSCRIBER=mailto:webpush@example.com \
    JWT_SECRET=bilirec_secret \
    DEBUG=false \
    PRODUCTION_MODE=false \
    SILENT_ACCESS_LOG=false \
    UPLOAD_BUFFER_SIZE=5242880 \
    DOWNLOAD_BUFFER_SIZE=5242880 \
    STREAM_WRITER_BUFFER_SIZE=1048576 \
    READ_STREAM_BYTES_POOL_SIZE=524288 \
    READ_STREAM_CHAN_BUFFER_SIZE=16 \
    READ_STREAM_BYTES_POOL_SIZE_HIGH=1048576 \
    READ_STREAM_CHAN_BUFFER_SIZE_HIGH=48 \
    LIVE_STREAM_WRITER_BUFFER_SIZE=8388608 \
    LIVE_STREAM_WRITER_SYNC_PERIOD_SECS=0 \
    LIVE_STREAM_WRITER_COLD_CACHE_RELEASE_SECS=60 \
    LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS=15 \
    LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE=64 \
    LIVE_STREAM_WRITER_BYTES_POOL_SIZE=524288 \
    LIVE_STREAM_WRITER_BYTES_POOL_SIZE_HIGH=1048576 \
    SKIP_SMALL_FLUSH_THRESHOLD=1048576 \
    SKIP_SMALL_FLUSH=true \
    SEQUENTIAL_WRITE=true \
    DROP_FILE_PAGE_CACHE=true

# Optional HTTPS configuration (SERVER_CRT and SERVER_KEY):
# When both are set, fiber will use HTTPS
# Example in docker run:
#   -e SERVER_CRT=/app/certs/server.crt \
#   -e SERVER_KEY=/app/certs/server.key \
#   -v /path/to/certs:/app/certs:ro

ENV GOMEMLIMIT=768MiB
ENV GOGC=100

ENTRYPOINT ["./bilirec"]