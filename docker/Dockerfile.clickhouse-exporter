FROM golang:1.17-rc-alpine as builder

COPY ./client /go/src/github.com/kmlebedev/txmlconnector/client
COPY ./examples/clickhouse-exporter /go/src/github.com/kmlebedev/txmlconnector/examples/clickhouse-exporter
COPY ./proto /go/src/github.com/kmlebedev/txmlconnector/proto
COPY ./go.mod /go/src/github.com/kmlebedev/txmlconnector/go.mod
COPY ./go.sum /go/src/github.com/kmlebedev/txmlconnector/go.sum

WORKDIR /go/src/github.com/kmlebedev/txmlconnector/examples/clickhouse-exporter

RUN go mod download && \
    CGO_ENABLED=0 go build -ldflags "-extldflags -static" -o /go/bin/clickhouse-exporter github.com/kmlebedev/txmlconnector/examples/clickhouse-exporter

FROM alpine
COPY --from=builder /go/bin/clickhouse-exporter /usr/bin/clickhouse-exporter

ENTRYPOINT ["/usr/bin/clickhouse-exporter"]