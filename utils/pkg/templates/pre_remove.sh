# pre-install script for travis-worker

if getent passwd travis >/dev/null; then
  userdel travis -r
fi
