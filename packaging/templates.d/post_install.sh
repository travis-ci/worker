#!/bin/bash
# travis-worker post-install script

__add_travis_user() {
  if getent passwd travis >/dev/null; then
    return
  fi

  useradd -m -r travis -c "travis for travis-worker"
}

__create_travis_run_dir() {
  mkdir -p /var/tmp/travis-run.d
  chown -R travis:travis /var/tmp/travis-run.d
}

__add_travis_user
__create_travis_run_dir
