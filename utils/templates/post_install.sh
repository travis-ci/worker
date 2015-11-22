# travis-worker post-install script

if ! getent passwd travis >/dev/null; then
  useradd -m -r travis -c "travis for travis-worker"
fi

mkdir -p /var/tmp/travis-run.d
chown -R travis:travis /var/tmp/travis-run.d
