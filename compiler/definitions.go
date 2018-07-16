package compiler

import "golang.org/x/tools/go/loader"

// DocText embodies a file level text not
// associated with any declaration exiting
// within a file.
type DocText struct {
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
	End       int
	Begin     int
	Length    int
	Text      string
}

// Doc represents the associated documentation for
// a giving package or type declaration. It contains
// the main text which is the first paragraph of the
// commentary and the extra commentary which are seperated
// by 2 newline spacing.
type Doc struct {
	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
	Text      string
	Parts     []DocText
}

// Import embodies an import declaration within a package
// file. It contains the path, the filesystem directory
// location and alias used.
type Import struct {
	Runtime   bool
	Alias     string
	Path      string
	Dir       string
	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

// PackageFile defines a giving package file with it's associated
// definitions, constructs and declarations. It provides the
// one-to-one relation of a parsed package.
type PackageFile struct {
	Commentaries []Doc
	Name         string
	File         string
	Dir          string
	Imports      []Import
}

// Package embodies a parsed Golang/Go based package,
// with it's information and package files, which embody
// it's declarations, types and constructs.
type Package struct {
	Meta *loader.PackageInfo

	Doc        *Doc
	Name       string
	Blanks     []Variable
	Constants  []Constant
	Variables  []Variable
	Types      []DefinedType
	Structs    []Struct
	Interfaces []Interface
	Functions  []Function
	Depends    map[string]*Package
	Files      map[string]*PackageFile
}

// HasDependency returns true/false if giving path
// has already being added into dependency map.
func (p *Package) HasDependency(path string) bool {
	_, ok := p.Depends[path]
	return ok
}

type Interface struct {
	Name string

	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

type Struct struct {
	Name      string
	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

type Method struct {
	Name string
	Body []Expression

	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

type Variable struct {
	Name string

	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

type Constant struct {
	Name string

	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

type DefinedType struct {
	Name string

	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

type Function struct {
	Name string
	Body []Expression

	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

type Operator struct {
	Type string

	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

type Expression struct {
	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

type ExpressionGroup struct {
	End       int
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}
