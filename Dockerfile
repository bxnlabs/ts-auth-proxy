FROM --platform=${BUILDPLATFORM} golang:1.25-bookworm@sha256:64db01bea9644f2608ac67d079dfa7d901930d6c7549cddf8e23377ab664c09c AS builder

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
