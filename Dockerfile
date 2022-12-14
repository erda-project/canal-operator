FROM registry.erda.cloud/retag/golang:1.17-buster AS builder

WORKDIR /workspace

COPY api/ api/
COPY controllers/ controllers/
COPY main.go main.go
COPY go.mod go.mod
COPY go.sum go.sum
RUN GOPROXY=https://goproxy.cn,direct CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o canal-operator main.go

FROM registry.erda.cloud/retag/debian:buster

RUN rm -f /etc/localtime && \
  ln -s /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
  useradd -m cxr

RUN sed -i -r 's/(deb|security).debian.org/mirror.sjtu.edu.cn/g' /etc/apt/sources.list && \
  apt-get update && apt-get install -y curl && apt-get clean

WORKDIR /
COPY --from=builder /workspace/canal-operator .
USER cxr:cxr
ENTRYPOINT []
CMD ["/canal-operator"]
