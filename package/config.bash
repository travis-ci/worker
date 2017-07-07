: ${CHECKOUT_ROOT:=${TRAVIS_BUILD_DIR:-/code}}
export CHECKOUT_ROOT
source "${CHECKOUT_ROOT}/package/functions.bash"

export DATE="$(date -u +%Y%m%dT%H%M%SZ)"
export PC_USER='travisci'
export PC_REPO='worker'

export PKG_PLATFORMS=('ubuntu:trusty' 'ubuntu:precise' 'centos:7')

__define_version
__define_shell_flags
