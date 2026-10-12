FROM golang:1.26.9-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
COPY web ./web
COPY relays ./relays
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/ariaatashin .
COPY scripts/patch-sing-box.py /tmp/patch-sing-box.py
RUN apk add --no-cache python3 && go mod download github.com/sagernet/sing-box@v1.14.3
RUN cp -R /go/pkg/mod/github.com/sagernet/sing-box@v1.14.3 /tmp/sing-box-source && python3 /tmp/patch-sing-box.py /tmp/sing-box-source
RUN cd /tmp/sing-box-source && CGO_ENABLED=0 go build -buildvcs=false -tags with_quic,with_utls,with_v2ray_api -ldflags="-s -w" -o /out/sing-box ./cmd/sing-box

FROM alpine:3.22
RUN apk add --no-cache ca-certificates curl tar gzip tzdata sqlite
WORKDIR /app
# Fixed official engine version; fail closed if the archive digest differs.
RUN curl -fL --retry 3 https://github.com/MHSanaei/3x-ui/releases/download/v3.8.5/x-ui-linux-amd64.tar.gz -o /tmp/engine.tar.gz \
    && echo '6a85c110a04a727613c933c54ae602b8d37dab8876c6e20a6d46623010dd9d3c  /tmp/engine.tar.gz' | sha256sum -c - \
    && tar --no-same-owner -xzf /tmp/engine.tar.gz -C /app \
    && chmod 0755 /app/x-ui /app/x-ui/bin /app/x-ui/x-ui /app/x-ui/bin/xray-linux-amd64 /app/x-ui/bin/tuic-server \
    && chmod 0644 /app/x-ui/bin/*.dat \
    && rm /tmp/engine.tar.gz
COPY --from=builder /out/ariaatashin /usr/local/bin/ariaatashin
COPY --from=builder /out/sing-box /app/sing-box
ENV PORT=8080 ARIA_ENGINE_DIR=/app/x-ui
# Executables stay in the image. Runtime config and SQLite go on /data.
# On hosts without a mounted /data, the launcher uses temporary storage.
RUN mkdir -p /data && chmod 1777 /data
EXPOSE 8080
VOLUME ["/data"]
CMD ["/usr/local/bin/ariaatashin"]
