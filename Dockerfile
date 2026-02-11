FROM --platform=${BUILDPLATFORM} golang:1.26-bookworm@sha256:eae3cdfa040d0786510a5959d36a836978724d03b34a166ba2e0e198baac9196 AS builder

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
