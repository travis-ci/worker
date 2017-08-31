FROM golang:1.9 as builder
MAINTAINER Travis CI GmbH <support+travis-worker-docker-image@travis-ci.org>

RUN go get -u github.com/FiloSottile/gvt

COPY . /go/src/github.com/travis-ci/worker
WORKDIR /go/src/github.com/travis-ci/worker
RUN make deps
RUN make build

#################################
### linux/amd64/travis-worker ###
#################################

FROM alpine:latest
RUN apk --no-cache add ca-certificates

COPY --from=builder /go/bin/travis-worker /usr/local/bin/travis-worker

CMD ["/usr/local/bin/travis-worker"]
