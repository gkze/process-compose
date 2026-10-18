//go:build !windows && !linux && !darwin

package command

import "errors"

var errTreeUnsupported = errors.New("process tree enumeration is not supported on this platform")

func listProcs() (map[int]procInfo, error) {
	return nil, errTreeUnsupported
}

func readProc(int) (procInfo, error) {
	return procInfo{}, errTreeUnsupported
}

func procEnvContains(int, string) bool {
	return false
}
