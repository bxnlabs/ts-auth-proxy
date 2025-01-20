FROM --platform=${BUILDPLATFORM} golang:1.23-bookworm AS builder

ARG TARGETARCH
ENV GOARCH "${TARGETARCH}"
ENV GOOS linux

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY pkg ./pkg
COPY main.go ./
RUN go build -ldflags="-w -s" -o dist/ts-auth-server main.go


FROM scratch

COPY --from=builder /build/dist/ts-auth-server /ts-auth-server
ENTRYPOINT [ "/ts-auth-server" ]
