#!/usr/bin/env bash

## Some environment setup
set -e
export DEBIAN_FRONTEND=noninteractive

##

## Handle Arguments

if [[ ! -n $1 ]]; then
  echo "No arguments provided, installing with"
  echo "default configuration values."
fi

while [ $# -gt 0 ]; do
  case "$1" in
    --travis_worker_version=*)
      TRAVIS_WORKER_VERSION="${1#*=}"
      ;;
    --docker_version=*)
      DOCKER_VERSION="${1#*=}"
      ;;
    --aws=*)
      AWS="${1#*=}"
      ;;
    --travis_enterprise_host=*)
      TRAVIS_ENTERPRISE_HOST="${1#*=}"
      ;;
    --travis_enterprise_security_token=*)
      TRAVIS_ENTERPRISE_SECURITY_TOKEN="${1#*=}"
      ;;
    --travis_enterprise_build_endpoint=*)
      TRAVIS_ENTERPRISE_BUILD_ENDPOINT="${1#*=}"
      ;;
    --travis_queue_name=*)
      TRAVIS_QUEUE_NAME="${1#*=}"
      ;;
    --skip_docker_populate=*)
      SKIP_DOCKER_POPULATE="${1#*=}"
      ;;
    *)
      printf "*************************************************************\n"
      printf "* Error: Invalid argument.                                  *\n"
      printf "* Valid Arguments are:                                      *\n"
      printf "*  --travis_worker_version=x.x.x                            *\n"
      printf "*  --docker_version=x.x.x                                   *\n"
      printf "*  --aws=true                                               *\n"
      printf "*  --travis_enterprise_host="demo.enterprise.travis-ci.com" *\n"
      printf "*  --travis_enterprise_security_token="token123"            *\n"
      printf "*  --travis_enterprise_build_endpoint="build-api"           *\n"
      printf "*  --travis_queue_name="builds.linux"                       *\n"
      printf "*  --skip_docker_populate=true                              *\n"
      printf "*************************************************************\n"
      exit 1
  esac
  shift
done

if [[ ! -n $DOCKER_VERSION ]]; then
  export DOCKER_VERSION="1.6.2"
else
  export DOCKER_VERSION
fi

if [[ ! -n $AWS ]]; then
  export AWS=false
else
  export AWS=true
fi

##

## We only want to run on 14.04
trusty_check() {
  if [[ !  $(cat /etc/issue) =~ 14.04 ]]; then
    echo "This should only be run on Ubuntu 14.04"
    exit 1
  fi
}

trusty_check
##


## We only want to run as root
root_check() {
  if [[ $(whoami) != "root" ]]; then
    echo "This should only be run as root"
    exit 1
  fi
}

root_check
##

## Install and setup Docker
docker_setup() {

  DOCKER_APT_FILE="/etc/apt/sources.list.d/docker.list"
  DOCKER_CONFIG_FILE="/etc/default/docker"

  if [[ ! -f $DOCKER_APT_FILE ]]; then
    wget -qO- https://get.docker.io/gpg | apt-key add -
    echo deb https://get.docker.io/ubuntu docker main > $DOCKER_APT_FILE
    apt-get update
    apt-get install -y linux-image-extra-`uname -r` lxc lxc-docker-$DOCKER_VERSION

  fi

  if [[ $AWS == true ]]; then
    DOCKER_MOUNT_POINT="--graph=/mnt/docker"
  fi

  # use LXC, and disable inter-container communication
  if [[ ! $(grep icc $DOCKER_CONFIG_FILE) ]]; then
    echo 'DOCKER_OPTS="-H tcp://0.0.0.0:4243 --storage-driver=aufs --icc=false --exec-driver=lxc '$DOCKER_MOUNT_POINT'"' >> $DOCKER_CONFIG_FILE
    service docker restart
    sleep 2 # a short pause to ensure the docker daemon starts
  fi
}

docker_setup
##

## Pull down all the Travis Docker images
docker_populate_images() {
  DOCKER_CMD="docker -H tcp://0.0.0.0:4243"

  # pick the languages you are interested in
  langs='android erlang go haskell jvm node-js perl php python ruby'
  declare -a lang_mappings=('clojure:jvm' 'scala:jvm' 'groovy:jvm' 'java:jvm' 'elixir:erlang' 'node_js:node-js')
  tag=latest
  for lang in $langs; do
    $DOCKER_CMD pull quay.io/travisci/travis-$lang:$tag
    $DOCKER_CMD tag -f quay.io/travisci/travis-$lang:$tag travis:$lang
  done

  # tag travis:ruby as travis:default
  $DOCKER_CMD tag -f travis:ruby travis:default

  for lang_map in "${lang_mappings[@]}"; do
    map=$(echo $lang_map|cut -d':' -f 1)
    lang=$(echo $lang_map|cut -d':' -f 2)

    $DOCKER_CMD tag -f quay.io/travisci/travis-$lang:$tag travis:$map
  done
}
if [[ ! -n $SKIP_DOCKER_POPULATE ]]; then
  docker_populate_images
fi
##

## Install travis-worker from packagecloud
install_travis_worker() {
  if [[ ! -f /etc/apt/sources.list.d/travisci_worker.list ]]; then
    # add packagecloud apt repo for travis-worker
    curl -s https://packagecloud.io/install/repositories/travisci/worker/script.deb.sh | bash
    if [[ -n $TRAVIS_WORKER_VERSION ]]; then
      apt-get install travis-worker=$TRAVIS_WORKER_VERSION
    else
      apt-get install travis-worker
    fi
  fi
}

install_travis_worker
##

## Configure travis-worker
configure_travis_worker() {
  TRAVIS_ENTERPRISE_CONFIG="/etc/default/travis-enterprise"
  TRAVIS_WORKER_CONFIG="/etc/default/travis-worker"

  if [[ -n $TRAVIS_ENTERPRISE_HOST ]]; then
    sed -i \
      "s/\# export TRAVIS_ENTERPRISE_HOST=\"enterprise.yourhostname.corp\"/export TRAVIS_ENTERPRISE_HOST=\"$TRAVIS_ENTERPRISE_HOST\"/" \
      $TRAVIS_ENTERPRISE_CONFIG
  fi

  if [[ -n $TRAVIS_ENTERPRISE_SECURITY_TOKEN ]]; then
    sed -i \
      "s/\# export TRAVIS_ENTERPRISE_SECURITY_TOKEN=\"abcd1234\"/export TRAVIS_ENTERPRISE_SECURITY_TOKEN=\"$TRAVIS_ENTERPRISE_SECURITY_TOKEN\"/" \
      $TRAVIS_ENTERPRISE_CONFIG
  fi

  if [[ -n $TRAVIS_ENTERPRISE_BUILD_ENDPOINT ]]; then
    sed -i \
      "s/export TRAVIS_ENTERPRISE_BUILD_ENDPOINT=\"__build__\"/export TRAVIS_ENTERPRISE_BUILD_ENDPOINT=\"$TRAVIS_ENTERPRISE_BUILD_ENDPOINT\"/" \
      $TRAVIS_ENTERPRISE_CONFIG
  fi

  if [[ -n $TRAVIS_QUEUE_NAME ]]; then
    sed -i \
      "s/export QUEUE_NAME='builds.linux'/export QUEUE_NAME=\'$TRAVIS_QUEUE_NAME\'/" \
      $TRAVIS_WORKER_CONFIG
  fi
}

configure_travis_worker
##

## Host Setup
host_setup() {
  # enable memory and swap accounting, disable apparmor (optional, but recommended)
  GRUB_CMDLINE_LINUX='cgroup_enable=memory swapaccount=1 apparmor=0'

  if [[ -d /etc/default/grub.d ]] ; then
    cat > "/etc/default/grub.d/99-travis-worker-settings.cfg" <<EOF
GRUB_CMDLINE_LINUX="$GRUB_CMDLINE_LINUX"
EOF
    update-grub
    return
  fi

  GRUB_CFG="/etc/default/grub"
  touch $GRUB_CFG

  if [[ ! $(grep cgroup_enabled $GRUB_CFG) ]]; then
    sed -i \
      "s/GRUB_CMDLINE_LINUX=\"\"/GRUB_CMDLINE_LINUX=\"$GRUB_CMDLINE_LINUX\"/" \
      $GRUB_CFG
  fi

  update-grub
}

host_setup
##

## Give travis-worker a kick to ensure the
## latest config is picked up
if [[ $(pgrep travis-worker) ]]; then
  stop travis-worker
fi
start travis-worker
##

echo 'Installation complete.'
echo 'It is recommended that this host is restarted before running jobs through it'
