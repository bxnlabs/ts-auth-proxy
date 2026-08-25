FROM --platform=${BUILDPLATFORM} golang:1.27-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452 AS builder

ARG TARGETARCH
ENV CGO_ENABLED=0
ENV GOARCH="${TARGETARCH}"
ENV GOOS=linux

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download
COPY server ./server
COPY main.go ./
RUN go build -ldflags="-w -s" -o dist/ts-auth-proxy main.go


FROM scratch

COPY --from=builder /build/dist/ts-auth-proxy /ts-auth-proxy
ENTRYPOINT [ "/ts-auth-proxy" ]
