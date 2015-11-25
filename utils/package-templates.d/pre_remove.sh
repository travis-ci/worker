# travis-worker pre-remove script

__stop_travis_worker() {
  if ! status travis-worker >/dev/null 2>&1 ; then
    return
  fi

  if stop travis-worker ; then
    return
  fi

  echo "Failed to stop travis-worker (exit $?)"
  echo 'Sending SIGKILL'
  killall -9 travis-worker

  if stop travis-worker ; then
    return
  fi

  echo "Failed to stop travis-worker after sending SIGKILL (exit $?)"
}

__remove_travis_user() {
  if ! getent passwd travis >/dev/null 2>&1 ; then
    return
  fi

  if userdel travis -r 2>/dev/null ; then
    return
  fi

  echo "Failed to remove travis user (exit $?)"
}

__remove_travis_user
__stop_travis_worker
