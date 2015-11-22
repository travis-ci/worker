__indent() {
  c="${1:+"2,999"} s/^/       /"
  case $(uname) in
    Darwin) sed -l "$c";;
    *) sed -u "$c";;
  esac
}

__announce() {
  echo "-----> $*"
}

__log() {
  echo $*
}

__error() {
  __announce ERROR: "$@"
}

__define_shell_flags() {
  set -o errexit
  set -o pipefail
	if [[ $BUILD_DEBUG ]] ; then
		set -o xtrace
  fi
}

__define_platform() {
	: ${PLATFORM:=${1}}
	: ${PLATFORM_FAMILY:=${PLATFORM%%:*}}
	: ${PLATFORM_RELEASE:=${PLATFORM##${PLATFORM_FAMILY}:}}

  PLATFORM_PACKAGE_TYPE='deb'
  PLATFORM_PACKAGE_ARCH='amd64'
  PACKAGECLOUD_OS='ubuntu'

  if [[ $PLATFORM_FAMILY = centos ]] ; then
    PLATFORM_PACKAGE_TYPE='rpm'
    PLATFORM_PACKAGE_ARCH='x86_64'
    PACKAGECLOUD_OS='el'
  fi

  export PLATFORM PLATFORM_FAMILY PLATFORM_RELEASE PLATFORM_PACKAGE_TYPE
  export PLATFORM_PACKAGE_ARCH PACKAGECLOUD_OS
}

__define_version() {
  if [[ ! -f VERSION ]] ; then
    local latest_version_tag="$(git tag | tail -1)"
    echo "${latest_version_tag##v}" > VERSION
  fi

  export VERSION="$(cat VERSION)"

  if [[ ! -f VERSION_SHA1 ]] ; then
    local version_sha1="$(
      git rev-parse --short --no-abbrev-ref "${latest_version_tag}"
    )"
    echo $version_sha1 > VERSION_SHA1
  fi

  export VERSION_SHA1="$(cat VERSION_SHA1)"

  if [[ ! -f CURRENT_SHA1 ]] ; then
    local current_sha1="$(
      git rev-parse --short --no-abbrev-ref HEAD
    )"
    echo $current_sha1 > CURRENT_SHA1
  fi

  export CURRENT_SHA1="$(cat CURRENT_SHA1)"

  if [[ ! -f GIT_DESCRIPTION ]] ; then
    local git_description="$(git describe --always --dirty --tags 2>/dev/null)"
    echo $git_description > GIT_DESCRIPTION
  fi

  export GIT_DESCRIPTION="$(cat GIT_DESCRIPTION)"

  if [[ $CURRENT_SHA1 != $VERSION_SHA1 ]] ; then
    orig_ifs="$IFS"
    IFS=-
    git_version_parts=($GIT_DESCRIPTION)
    IFS="$orig_ifs"
    export VERSION="${VERSION}.dev.${git_version_parts[1]}-${CURRENT_SHA1}"
  fi
}
