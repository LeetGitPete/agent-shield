# One Dockerfile for all services; SERVICE picks which binary to build.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE
RUN CGO_ENABLED=0 go build -o /bin/app ./cmd/${SERVICE}

# Minimal runtime image: just the static binary + TLS root certs.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /bin/app /app
ENTRYPOINT ["/app"]
