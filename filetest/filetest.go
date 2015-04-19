package filetest

import "os"

func IsDir(path string) (bool, error) {
	return is(path, checkIsDir)
}

func checkIsDir(fi os.FileInfo) bool {
	return fi.IsDir()
}

func IsFile(path string) (bool, error) {
	return is(path, checkIsFile)
}

func checkIsFile(fi os.FileInfo) bool {
	return !fi.IsDir()
}

func is(path string, check func(os.FileInfo) bool) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return check(fi), nil
}
