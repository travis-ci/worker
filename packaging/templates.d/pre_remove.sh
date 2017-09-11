#!/bin/bash
# travis-worker pre-remove script

__stop_travis_worker() {
  if ! status travis-worker >/dev/null 2>&1; then
    return
  fi

  stop travis-worker
  exit_code="$?"

  if [ $exit_code -eq 0 ]; then
    return
  fi

  echo "Failed to stop travis-worker (exit $exit_code)"
  echo 'Sending SIGKILL'

  killall -9 travis-worker

  stop travis-worker
  exit_code="$?"

  if [ $exit_code -eq 0 ]; then
    return
  fi

  echo "Failed to stop travis-worker after sending SIGKILL (exit $exit_code)"
}

__remove_travis_user() {
  if ! getent passwd travis >/dev/null 2>&1; then
    return
  fi

  userdel travis -r
  exit_code="$?"

  if [ $exit_code -eq 0 ]; then
    return
  fi

  echo "Failed to remove travis user (exit $exit_code)"
}

set +e
__remove_travis_user
__stop_travis_worker
