FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/yingku ./cmd/yingku

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata ffmpeg \
    && mkdir -p /config /media
WORKDIR /app
COPY --from=build /out/yingku /app/yingku
COPY LICENSE THIRD_PARTY_NOTICES.md /app/
ENV YINGKU_ADDR=0.0.0.0:8787 \
    YINGKU_DATA_DIR=/config \
    YINGKU_MEDIA_DIR=/media \
    TZ=Asia/Shanghai
EXPOSE 8787
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/yingku", "-healthcheck"]
ENTRYPOINT ["/app/yingku"]
