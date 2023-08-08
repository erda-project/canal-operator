FROM registry.erda.cloud/erda-x/golang:1 AS builder

WORKDIR /workspace

COPY api/ api/
COPY controllers/ controllers/
COPY main.go main.go
COPY go.mod go.mod
COPY go.sum go.sum
RUN CGO_ENABLED=0 go build -a -o canal-operator main.go

FROM registry.erda.cloud/erda-x/debian:11

WORKDIR /
COPY --from=builder /workspace/canal-operator .
USER dice:dice
ENTRYPOINT []
CMD ["/canal-operator"]
