//go:build windows

package config

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// writePrivate writes a file only its owner can read. Windows ignores mode
// bits, so the same promise is kept with an access control list: a
// protected DACL granting the owner alone, inheriting nothing from the
// directory, which is what a shared or roaming profile would otherwise add.
func writePrivate(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;OW)")
	if err != nil {
		return fmt.Errorf("config: could not build the file's permissions: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("config: could not read the file's permissions: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("config: could not restrict %s to its owner: %w", path, err)
	}
	return nil
}
