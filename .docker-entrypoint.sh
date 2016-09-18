#!/bin/sh
set -e

if [ "${1:0:1}" = '-' ]; then
  set -- travis-worker "$@"
fi

exec "$@"
