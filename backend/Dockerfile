FROM golang:1.27-alpine AS build
# go.mod pins the toolchain; allow fetching it if the image is older than the pin.
ENV GOTOOLCHAIN=auto
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bms ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.24.4
# tzdata: the API validates DEFAULT_TIMEZONE (Africa/Dar_es_Salaam) at start-up and refuses to run without it.
RUN apk upgrade --no-cache \
 && apk add --no-cache tzdata \
 && adduser -D -u 65532 -h /app app \
 && mkdir -p /app/data && chown app:app /app/data
WORKDIR /app
COPY --from=build /out/bms /out/migrate /app/
USER app
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
CMD ["/app/bms"]
