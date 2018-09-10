FROM golang:1.10 as build
WORKDIR /go/src/github.com/m-lab/github-maintenance-exporter
ADD . ./
RUN CGO_ENABLED=0 go get -v github.com/m-lab/github-maintenance-exporter

FROM alpine
WORKDIR /
COPY --from=build /go/bin/github-maintenance-exporter ./
ENTRYPOINT ["/github-maintenance-exporter"]

