FROM golang:1.9 as builder
MAINTAINER Travis CI GmbH <support+travis-worker-docker-image@travis-ci.org>

RUN go get -u github.com/FiloSottile/gvt

COPY . /go/src/github.com/travis-ci/worker
WORKDIR /go/src/github.com/travis-ci/worker
RUN make deps
ENV CGO_ENABLED 0
RUN make build

#################################
### linux/amd64/travis-worker ###
#################################

FROM alpine:latest
RUN apk --no-cache add ca-certificates

COPY --from=builder /go/bin/travis-worker /usr/local/bin/travis-worker
COPY --from=builder /go/src/github.com/travis-ci/worker/.docker-entrypoint.sh /docker-entrypoint.sh

VOLUME ["/var/tmp"]
STOPSIGNAL SIGINT

ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["/usr/local/bin/travis-worker"]
