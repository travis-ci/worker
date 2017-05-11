#!/usr/bin/env bash
set -o errexit

main() {
  set -o xtrace

  export DEBIAN_FRONTEND=noninteractive

  sudo apt update -y
  sudo apt install -y \
    build-essential \
    git \
    redis-tools

  if ! gimme version; then
    curl -sSL \
      -o /usr/local/bin/gimme \
      https://raw.githubusercontent.com/travis-ci/gimme/master/gimme
    chmod +x /usr/local/bin/gimme
  fi

  sudo -u vagrant HOME=/home/vagrant bash -c 'gimme 1.8.1'

  if ! docker version; then
    curl -sSL https://get.docker.io | sudo bash
  fi

  docker run -d -p 5672:5672 --name rabbitmq rabbitmq:3-management
  docker run -d -p 6379:6379 --name redis redis

  cat >/home/vagrant/.bash_profile <<EOF
export PATH="\$HOME/bin:\$HOME/go/bin:\$PATH"
export GOPATH="\$HOME/go"
eval "\$(gimme 1.8.1)"
set -o vi
EOF

  chown vagrant:vagrant /home/vagrant/.bash_profile
  chown -R vagrant:vagrant /home/vagrant/go

  sudo -u vagrant HOME=/home/vagrant bash <<EOBASH
go get github.com/FiloSottile/gvt
go get -u github.com/alecthomas/gometalinter
gometalinter --install
EOBASH
}

main "$@"
