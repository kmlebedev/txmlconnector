#!/bin/sh

isArgPassed() {
  arg="$1"
  argWithEqualSign="$1="
  shift
  while [ $# -gt 0 ]; do
    passedArg="$1"
    shift
    case $passedArg in
    $arg)
      return 0
      ;;
    $argWithEqualSign*)
      return 0
      ;;
    esac
  done
  return 1
}

case "$1" in
	'server')
	  shift
	  exec /usr/bin/wine64 /usr/bin/txmlconnector-server.exe $@
	;;
	'client')
	  shift
	  exec /usr/bin/txmlconnector-client $@
	;;
esac