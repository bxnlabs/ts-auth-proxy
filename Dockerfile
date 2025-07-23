FROM --platform=${BUILDPLATFORM} golang:1.24-bookworm@sha256:f9b4203ec00053382d7daf93afa0bd6afe31473edd60f8ae9838906bb844fdb3 AS builder

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
