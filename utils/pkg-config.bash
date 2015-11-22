: ${CHECKOUT_ROOT:=${TRAVIS_BUILD_DIR:-/code}}
export CHECKOUT_ROOT

export DATE=$(date -u +%m-%d-%Y_%H-%M-%S)
export PC_USER='travisci'
export PC_REPO='worker'

export VERSION=$(cat $CHECKOUT_ROOT/VERSION)
export VERSION_SHA1=$(cat $CHECKOUT_ROOT/VERSION_SHA1)
export CURRENT_SHA1=$(cat $CHECKOUT_ROOT/CURRENT_SHA1)

declare -a PKG_PLATFORMS=('ubuntu:trusty' 'ubuntu:precise' 'centos:7')
export PKG_PLATFORMS
