# BUILDING STAGE
FROM golang:alpine AS builder
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOEXPERIMENT=greenteagc CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o /nickel ./cmd/nickel

# RUNNING STAGE
FROM alpine:edge
RUN apk add --no-cache ca-certificates
COPY --from=builder /nickel /nickel
EXPOSE 8080
ENTRYPOINT ["/nickel"]
