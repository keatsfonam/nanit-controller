# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-$(go env GOARCH)} \
    go build -trimpath -ldflags='-s -w' -o /out/nanit-controller ./cmd/nanit-controller

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/nanit-controller /nanit-controller
USER nonroot:nonroot
ENTRYPOINT ["/nanit-controller"]
