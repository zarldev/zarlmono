package filesystem

import "io/fs"

var filePermissionBits = [...]struct {
	mask int
	mode fs.FileMode
}{
	{mask: 0o001, mode: 0o001},
	{mask: 0o002, mode: 0o002},
	{mask: 0o004, mode: 0o004},
	{mask: 0o010, mode: 0o010},
	{mask: 0o020, mode: 0o020},
	{mask: 0o040, mode: 0o040},
	{mask: 0o100, mode: 0o100},
	{mask: 0o200, mode: 0o200},
	{mask: 0o400, mode: 0o400},
}

func fileModeFromPermissionBits(mode int) fs.FileMode {
	if mode <= 0 {
		return 0
	}

	var out fs.FileMode
	for _, bit := range filePermissionBits {
		if mode&bit.mask != 0 {
			out |= bit.mode
		}
	}
	return out
}
