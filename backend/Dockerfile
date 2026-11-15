FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bms ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.20
RUN adduser -D -u 65532 -h /app app \
 && mkdir -p /app/data && chown app:app /app/data
WORKDIR /app
COPY --from=build /out/bms /out/migrate /app/
USER app
EXPOSE 8080
CMD ["/app/bms"]
