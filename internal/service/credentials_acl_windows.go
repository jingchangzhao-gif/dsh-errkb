//go:build windows

package service

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// restrictCredentialFile replaces inherited permissions before the temporary
// file is renamed into place. Windows ignores Unix mode bits on os.WriteFile,
// so relying on 0600 would leave BUILTIN\Users able to read the database
// password through the parent directory's inherited ACL.
func restrictCredentialFile(path string) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return fmt.Errorf("open current process token: %w", err)
	}
	defer func() { _ = token.Close() }()

	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current user: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
		"D:P(A;;FA;;;%s)(A;;FA;;;SY)(A;;FA;;;BA)", user.User.Sid.String(),
	))
	if err != nil {
		return fmt.Errorf("build protected DACL: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read protected DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("apply protected DACL: %w", err)
	}
	return nil
}
