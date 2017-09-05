#!/usr/bin/env bash
set -o errexit

__indent() {
  c="${1:+"2,999"} s/^/       /"
  case $(uname) in
  Darwin) sed -l "$c" ;;
  *) sed -u "$c" ;;
  esac
}

__announce() {
  echo "-----> $*"
}

__log() {
  echo "$@"
}

__error() {
  __announce ERROR: "$@"
}

__define_shell_flags() {
  set -o errexit
  set -o pipefail
  if [[ $BUILD_DEBUG ]]; then
    set -o xtrace
  fi
}

__undef_platform() {
  unset PLATFORM PLATFORM_FAMILY PLATFORM_RELEASE
  unset PLATFORM_PACKAGE_TYPE PLATFORM_ARCH PACKAGECLOUD_OS
}

__define_platform_centos() {
  export PLATFORM_PACKAGE_TYPE='rpm'
  export PLATFORM_PACKAGE_ARCH='x86_64'
  export PACKAGECLOUD_OS='el'
}

__define_platform_ubuntu() {
  export PLATFORM_PACKAGE_TYPE='deb'
  export PLATFORM_PACKAGE_ARCH='amd64'
  export PACKAGECLOUD_OS='ubuntu'
}

__define_platform() {
  : "${PLATFORM:=${1}}"
  : "${PLATFORM_FAMILY:=${PLATFORM%%:*}}"
  : "${PLATFORM_RELEASE:=${PLATFORM##${PLATFORM_FAMILY}:}}"
  "__define_platform_${PLATFORM_FAMILY}"
  export PLATFORM PLATFORM_FAMILY PLATFORM_RELEASE
}

__define_with_file_cache() {
  local filename="${1}"
  shift

  if [[ ! -f ${filename} ]]; then
    eval "$*" >"${filename}"
  fi

  eval "export ${filename}=\"\$(cat ${filename})\""
}

__define_version() {
  __define_with_file_cache VERSION_TAG "git tag -l --sort=v:refname | tail -1"
  __define_with_file_cache VERSION "echo \${VERSION_TAG##v}"
  __define_with_file_cache VERSION_SHA1 \
    "git rev-parse --short --no-abbrev-ref \"\${VERSION_TAG}\""
  __define_with_file_cache CURRENT_SHA1 \
    "git rev-parse --short --no-abbrev-ref HEAD"
  __define_with_file_cache GIT_DESCRIPTION \
    "git describe --always --dirty --tags 2>/dev/null"

  if [[ "${CURRENT_SHA1}" != "${VERSION_SHA1}" ]]; then
    orig_ifs="${IFS}"
    IFS=-
    git_version_parts=(${GIT_DESCRIPTION})
    IFS="${orig_ifs}"
    if [[ "${git_version_parts[1]}" ]]; then
      export VERSION="${VERSION}.dev.${git_version_parts[1]}-${CURRENT_SHA1}"
    fi
  fi
}
