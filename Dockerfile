FROM --platform=${BUILDPLATFORM} golang:1.24-bookworm@sha256:e617461712dbebf8768e10c1a5deab2833d67d2b692894cb8f4f0a3c19a8efb5 AS builder

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
