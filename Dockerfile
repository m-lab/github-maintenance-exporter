FROM golang:1.20 as build
WORKDIR /go/src/github.com/m-lab/github-maintenance-exporter
ADD . ./
RUN CGO_ENABLED=0 go install -v .

FROM alpine
WORKDIR /
COPY --from=build /go/bin/github-maintenance-exporter ./
ENTRYPOINT ["/github-maintenance-exporter"]
