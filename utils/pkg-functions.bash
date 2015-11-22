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
  echo "      " $*
}

__error() {
  __announce ERROR: "$@"
}

__define_shell_flags() {
	if [[ -n $BUILD_DEBUG ]]; then
		set -ex
	else
		set -e
	fi
}

__define_platform() {
	: ${PLATFORM:=${1}}
	: ${PLATFORM_FAMILY:=${PLATFORM%%:*}}
	: ${PLATFORM_RELEASE:=${PLATFORM##${PLATFORM_FAMILY}:}}
  export PLATFORM PLATFORM_FAMILY PLATFORM_RELEASE
}

__define_version() {
  if [[ ! -f VERSION ]] ; then
    local latest_version_tag="$(git tag | tail -1)"

    export VERSION="${latest_version_tag##v}"
    echo $VERSION > VERSION
  else
    export VERSION="$(cat VERSION)"
  fi

  if [[ ! -f VERSION_SHA1 ]] ; then
    export VERSION_SHA1="$(
      git rev-parse --short --no-abbrev-ref "${latest_version_tag}"
    )"
    echo $VERSION_SHA1 > VERSION_SHA1
  else
    export VERSION_SHA1="$(cat VERSION_SHA1)"
  fi

  if [[ ! -f CURRENT_SHA1 ]] ; then
    export CURRENT_SHA1="$(
      git rev-parse --short --no-abbrev-ref HEAD
    )"
    echo $CURRENT_SHA1 > CURRENT_SHA1
  else
    export CURRENT_SHA1="$(cat CURRENT_SHA1)"
  fi

  if [[ ! -f GIT_DESCRIPTION ]] ; then
    export GIT_DESCRIPTION="$(git describe --always --dirty --tags 2>/dev/null)"
    echo $GIT_DESCRIPTION > GIT_DESCRIPTION
  else
    export GIT_DESCRIPTION="$(cat GIT_DESCRIPTION)"
  fi

  if [[ $CURRENT_SHA1 != $VERSION_SHA1 ]] ; then
    orig_ifs="$IFS"
    IFS=-
    git_version_parts=($GIT_DESCRIPTION)
    IFS="$orig_ifs"
    export VERSION="${latest_version_tag##v}.dev.${git_version_parts[1]}-${CURRENT_SHA1}"
  else
    export VERSION="${latest_version_tag##v}"
  fi
}
