//go:build !windows

package service

import "os"

func restrictCredentialFile(path string) error {
	return os.Chmod(path, 0o600)
}
