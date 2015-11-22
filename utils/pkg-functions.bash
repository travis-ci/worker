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
