//go:build windows

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var statePrefix = []byte("OCW1")
var stateEntropy = []byte("OpenCompute protected state v1")

func userSID() (*windows.SID, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, e
	}
	return u.User.Sid, nil
}
func stateSecurity() (*windows.SecurityAttributes, error) {
	sid, e := userSID()
	if e != nil {
		return nil, e
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		return nil, e
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

// State belongs in a private local user profile. Ancestors must be trusted;
// reject junctions/reparse points instead of silently retargeting the store.
func checkedWindowsPath(path string) (string, error) {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	volume := filepath.VolumeName(absolute)
	if len(volume) != 2 || volume[1] != ':' {
		return "", errors.New("local drive state path required")
	}
	current := volume + `\`
	for _, part := range strings.Split(strings.TrimPrefix(absolute, current), `\`) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		name, e := windows.UTF16PtrFromString(current)
		if e != nil {
			return "", e
		}
		attrs, e := windows.GetFileAttributes(name)
		if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
			break
		}
		if e != nil {
			return "", e
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return "", errors.New("state path cannot contain reparse points")
		}
	}
	return absolute, nil
}
func validateWindowsDACL(sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) error {
	control, _, e := sd.Control()
	if e != nil {
		return e
	}
	acl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	if acl == nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("protected private state ACL required")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, i, &ace); e != nil {
			return e
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("unsupported state ACL")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(user) && sid.String() != "S-1-5-18" {
			return errors.New("state ACL permits another principal")
		}
	}
	return nil
}

// Open the object itself; never follow a final symlink/junction/reparse point.
func openWindowsState(path string, access, creation uint32, directory bool) (*os.File, error) {
	path, e := checkedWindowsPath(path)
	if e != nil {
		return nil, e
	}
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	var sa *windows.SecurityAttributes
	if creation != windows.OPEN_EXISTING {
		sa, e = stateSecurity()
		if e != nil {
			return nil, e
		}
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	if creation == windows.OPEN_EXISTING && !directory {
		share |= windows.FILE_SHARE_DELETE
	}
	handle, e := windows.CreateFile(name, access, share, sa, creation, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if e != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: e}
	}
	f := os.NewFile(uintptr(handle), path)
	var info windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(handle, &info); e != nil {
		f.Close()
		return nil, e
	}
	isDir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || isDir != directory {
		f.Close()
		return nil, errors.New("state object has unsupported type")
	}
	sd, e := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		f.Close()
		return nil, e
	}
	owner, _, e := sd.Owner()
	sid, se := userSID()
	if e != nil || se != nil || !owner.Equals(sid) {
		f.Close()
		return nil, errors.New("state object must belong to current user")
	}
	if e = validateWindowsDACL(sd, sid); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func prepareStateDirectory(dir string) error {
	dir, e := checkedWindowsPath(dir)
	if e != nil {
		return e
	}
	name, e := windows.UTF16PtrFromString(dir)
	if e != nil {
		return e
	}
	sa, e := stateSecurity()
	if e != nil {
		return e
	}
	e = windows.CreateDirectory(name, sa)
	if errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
		if e = os.MkdirAll(filepath.Dir(dir), 0700); e != nil {
			return e
		}
		e = windows.CreateDirectory(name, sa)
	}
	if e != nil && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		return e
	}
	f, e := openWindowsState(dir, windows.READ_CONTROL, windows.OPEN_EXISTING, true)
	if e != nil {
		return e
	}
	return f.Close()
}
func cryptState(data []byte, encrypt bool) ([]byte, error) {
	if len(data) == 0 || len(data) > 65536 {
		return nil, errors.New("invalid state size")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	entropy := windows.DataBlob{Size: uint32(len(stateEntropy)), Data: &stateEntropy[0]}
	var out windows.DataBlob
	var e error
	if encrypt {
		e = windows.CryptProtectData(&in, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		e = windows.CryptUnprotectData(&in, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if e != nil {
		return nil, errors.New("state protection failed")
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	if out.Size > 65536 {
		return nil, errors.New("invalid protected state size")
	}
	result := append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...)
	return result, nil
}
func readSecret(path string) ([]byte, error) {
	f, e := openWindowsState(path, windows.GENERIC_READ|windows.READ_CONTROL, windows.OPEN_EXISTING, false)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil {
		return nil, e
	}
	if len(data) > 65536 || !bytes.HasPrefix(data, statePrefix) {
		return nil, errors.New("protected Windows state required")
	}
	return cryptState(data[len(statePrefix):], false)
}
func writeSecret(path string, data []byte) error {
	// CLI entry validates the directory; library/test callers receive the same protection.
	if e := prepareStateDirectory(filepath.Dir(path)); e != nil {
		return e
	}
	if existing, e := openWindowsState(path, windows.READ_CONTROL, windows.OPEN_EXISTING, false); e == nil {
		existing.Close()
	} else if !os.IsNotExist(e) {
		return e
	}
	encrypted, e := cryptState(data, true)
	if e != nil {
		return e
	}
	temp := filepath.Join(filepath.Dir(path), ".state-"+randomString())
	name, e := windows.UTF16PtrFromString(temp)
	if e != nil {
		return e
	}
	sa, e := stateSecurity()
	if e != nil {
		return e
	}
	handle, e := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(handle), temp)
	defer os.Remove(temp)
	if _, e = f.Write(append(append([]byte(nil), statePrefix...), encrypted...)); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	from, e := windows.UTF16PtrFromString(temp)
	if e != nil {
		return e
	}
	to, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
