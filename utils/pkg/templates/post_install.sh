# travis-worker post-install script

#!/bin/sh

# create travis user
if ! getent passwd travis >/dev/null; then
  useradd -m -r travis -c "travis for travis-worker"
fi
