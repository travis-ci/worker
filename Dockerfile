FROM alpine:3.4
MAINTAINER Travis CI GmbH <support+travis-worker-docker-image@travis-ci.org>

ADD build/linux/amd64/travis-worker /usr/local/bin/travis-worker
ADD .docker-entrypoint.sh /docker-entrypoint.sh

RUN apk add --no-cache ca-certificates curl bash

VOLUME ["/var/tmp"]
ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["travis-worker"]
STOPSIGNAL SIGINT
