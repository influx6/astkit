package runtime

import (
	"os"
	"path/filepath"
	"runtime"
)

var (
	goRoot        = runtime.GOROOT()
	goPath        = os.Getenv("GOPATH")
	goSrcPath     = filepath.Join(goPath, "src")
	goRootSrcPath = filepath.Join(goRoot, "src")
)

// RootPath returns current go src path.
func RootPath() string {
	return goRoot
}

// GoPath returns current GOPATH value.
func GoPath() string {
	return goPath
}

// SrcPath returns current GOPATH/src path.
func SrcPath() string {
	return goSrcPath
}

// FromRuntime returns the giving path as absolute from the GOROOT path
// where the internal packages are stored.
func FromRuntime(pr string) string {
	return filepath.Join(goRootSrcPath, pr)
}

// FromGoPath returns the giving path as absolute from the gosrc path.
func FromGoPath(pr string) string {
	return filepath.Join(goSrcPath, pr)
}

// WithinRuntime returns a path that is relative to GOROOT/src path else an
// error
func WithinRuntime(path string) (string, error) {
	return filepath.Rel(goRootSrcPath, path)
}

// WithinToGoPath returns a path that is relative to the go src path else an error.
func WithinToGoPath(path string) (string, error) {
	return filepath.Rel(goSrcPath, path)
}

// PathExist returns true/false if giving path exists.
func PathExist(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return true
}
