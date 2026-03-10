# BUILDING STAGE
FROM golang:alpine AS builder
WORKDIR /app

ARG GIT_BRANCH=unknown
ARG GIT_COMMIT=unknown
ARG GIT_REMOTE=idkmaybedeveloper/nickel
ARG VERSION=11.5

RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN GOEXPERIMENT=greenteagc CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-w -s \
    -X github.com/idkmaybedeveloper/nickel/internal/api.Version=${VERSION} \
    -X github.com/idkmaybedeveloper/nickel/internal/api.GitBranch=${GIT_BRANCH} \
    -X github.com/idkmaybedeveloper/nickel/internal/api.GitCommit=${GIT_COMMIT} \
    -X github.com/idkmaybedeveloper/nickel/internal/api.GitRemote=${GIT_REMOTE}" \
    -o /nickel ./cmd/nickel

# RUNNING STAGE
FROM alpine:edge
RUN apk add --no-cache ca-certificates
COPY --from=builder /nickel /nickel
EXPOSE 8080
ENTRYPOINT ["/nickel"]
