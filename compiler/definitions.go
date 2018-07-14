package compiler

import "golang.org/x/tools/go/loader"

// DocText embodies a file level text not
// associated with any declaration exiting
// within a file.
type DocText struct {
	Line   int
	Column int
	Text   string
}

// Doc represents the associated documentation for
// a giving package or type declaration. It contains
// the main text which is the first paragraph of the
// commentary and the extra commentary which are seperated
// by 2 newline spacing.
type Doc struct {
	Line   int
	Column int
	Text   string
	Main   string
	More   []string
}

// Import embodies an import declaration within a package
// file. It contains the path, the filesystem directory
// location and alias used.
type Import struct {
	Runtime bool
	Alias   string
	Path    string
	Dir     string
}

// PackageFile defines a giving package file with it's associated
// definitions, constructs and declarations. It provides the
// one-to-one relation of a parsed package.
type PackageFile struct {
	File         string
	Dir          string
	Imports      []Import
	Commentaries []DocText
}

// Package embodies a parsed Golang/Go based package,
// with it's information and package files, which embody
// it's declarations, types and constructs.
type Package struct {
	Src loader.PackageInfo

	Doc        Doc
	Name       string
	Dir        string
	Blanks     []int
	Constants  []int
	Variables  []int
	Types      []int
	Structs    []int
	Interfaces []int
	Functions  []int
	Depends    map[string]Package
	Files      map[string]*PackageFile
}
