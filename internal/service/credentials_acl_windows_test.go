//go:build windows

package service

import (
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWriteCredentialsUsesProtectedOwnerACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	credentials := Credentials{
		Superuser: "errkb_admin",
		Password:  "test-password-not-a-secret",
		Port:      5433,
		Database:  "errkb",
	}
	if err := writeCredentials(path, credentials); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("read credential ACL: %v", err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatalf("open current process token: %v", err)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("read current user: %v", err)
	}

	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("read credential ACL control flags: %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("credential ACL is not protected: %q", descriptor.String())
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("read credential DACL: err=%v", err)
	}
	if dacl.AceCount != 3 {
		t.Fatalf("credential DACL has %d entries, want 3: %q", dacl.AceCount, descriptor.String())
	}
	trustees := make(map[string]bool, dacl.AceCount)
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			t.Fatalf("read credential ACE %d: %v", i, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("credential ACE %d has type %d, want allow", i, ace.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		trustees[sid.String()] = true
	}
	for _, required := range []string{user.User.Sid.String(), "S-1-5-18", "S-1-5-32-544"} {
		if !trustees[required] {
			t.Fatalf("credential DACL %q is missing trustee %s", descriptor.String(), required)
		}
	}
}
