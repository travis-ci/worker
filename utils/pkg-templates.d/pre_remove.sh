# pre-install script for travis-worker

if getent passwd travis >/dev/null ; then
  userdel travis -r 2>/dev/null || {
    echo "Failed to remove travis user (exit $?)"
  }
fi
