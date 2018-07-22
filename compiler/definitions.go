package compiler

import "golang.org/x/tools/go/loader"

// Location embodies important data relating to
// the location, column and length of declared
// statement or expression within a source file.
type Location struct {
	End       int
	File      string
	Begin     int
	Length    int
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int
}

// DocText embodies a file level text not
// associated with any declaration exiting
// within a file.
type DocText struct {
	Location
	Text string
}

// Doc represents the associated documentation for
// a giving package or type declaration. It contains
// the main text which is the first paragraph of the
// commentary and the extra commentary which are seperated
// by 2 newline spacing.
type Doc struct {
	Location

	Text  string
	Parts []DocText
}

// Import embodies an import declaration within a package
// file. It contains the path, the filesystem directory
// location and alias used.
type Import struct {
	Location

	Runtime bool
	Alias   string
	Path    string
	Dir     string
}

// PackageFile defines a giving package file with it's associated
// definitions, constructs and declarations. It provides the
// one-to-one relation of a parsed package.
type PackageFile struct {
	Name    string
	File    string
	Dir     string
	Docs    []Doc
	Imports []Import
}

// Package embodies a parsed Golang/Go based package,
// with it's information and package files, which embody
// it's declarations, types and constructs.
type Package struct {
	Meta *loader.PackageInfo `json:"-"`

	Name       string
	Docs       []Doc
	Blanks     []*Variable
	Constants  []*Constant
	Variables  []*Variable
	Types      map[string]*Type
	Structs    map[string]*Struct
	Interfaces map[string]*Interface
	Functions  map[string]*Function
	Depends    map[string]*Package
	Files      map[string]*PackageFile
}

// Add adds giving declaration into package declaration
// types according to it's class.
func (p *Package) Add(declr interface{}) error {
	switch rdeclr := declr.(type) {
	case *Struct:

	case *Function:
	case *Map:
	case *Slice:
	case *Variable:
		p.Variables = append(p.Variables, rdeclr)
	case *Constant:
		p.Constants = append(p.Constants, rdeclr)
	}
	return nil
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Package) Resolve(indexed map[string]*Package) error {

	return nil
}

// Interface embodies necessary data related to declared
// interface types within a package.
type Interface struct {
	Location

	Name    string
	Methods []Function
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Interface) Resolve(indexed map[string]*Package) error {

	return nil
}

// Struct embodies necessary data related to declared
// struct types within a package.
type Struct struct {
	Location

	Name    string
	Methods []Function
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Struct) Resolve(indexed map[string]*Package) error {

	return nil
}

// Function embodies data related to declared
// package functions, struct methods, interface
// methods or function closures.
type Function struct {
	Location

	IsAsync   bool
	Name      string
	Struct    *Struct
	Interface *Interface
	Body      []Expression
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Function) Resolve(indexed map[string]*Package) error {

	return nil
}

// Field represents field types and names
// declared as part of a struct's properties.
type Field struct {
	Location

	Name      string
	Basic     *Base
	Struct    *Struct
	Func      *Function
	Type      *Type
	Interface *Interface
	Chan      *Channel
	Map       *Map
	Slice     *Slice
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Field) Resolve(indexed map[string]*Package) error {

	return nil
}

// Map embodies a giving map type with
// an associated name, value and key type.
type Map struct {
	Location

	Name      string
	KeyType   interface{}
	ValueType interface{}
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Map) Resolve(indexed map[string]*Package) error {

	return nil
}

// Slice embodies a giving slice type with
// an associated name and type.
type Slice struct {
	Location

	Name string
	Type interface{}
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Slice) Resolve(indexed map[string]*Package) error {

	return nil
}

// Channel embodies a channel type declared
// with a golang package.
type Channel struct {
	Location

	Name      string
	Basic     *Base
	Chan      *Channel
	Struct    *Struct
	Type      *Type
	Interface *Interface
	Func      *Function
	Map       *Map
	Slice     *Slice
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Channel) Resolve(indexed map[string]*Package) error {

	return nil
}

// Variable embodies data related to declared
// variable.
type Variable struct {
	Location

	IsShortHand bool
	Name        string
	Basic       *Base
	Chan        *Channel
	Struct      *Struct
	Type        *Type
	Interface   *Interface
	Func        *Function
	Map         *Map
	Slice       *Slice
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Variable) Resolve(indexed map[string]*Package) error {

	return nil
}

// Constant holds related data related to information
// pertaining to declared constants.
type Constant struct {
	Location

	Name  string
	Basic *Base
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Constant) Resolve(indexed map[string]*Package) error {

	return nil
}

// Base represents a golang base types which include
// strings, ints, floats, complex, which are atomic
// indivisible types.
type Base struct {
	Name string
	Type string
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Base) Resolve(indexed map[string]*Package) error {

	return nil
}

// Type defines a struct holding information about
// a defined custom type based on an existing type.
type Type struct {
	Location

	Name       string
	IsFunction bool
	Chan       *Channel
	Struct     *Struct
	Basic      *Base
	Type       *Type
	Interface  *Interface
	Func       *Function
	Map        *Map
	Slice      *Slice
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Type) Resolve(indexed map[string]*Package) error {

	return nil
}

// ExpressionType defines a int type used to represent giving
// type of expressions such as assignment, multiplication,
// division, bracket closing, ...etc.
type ExpressionType int

// Expression provides a generic structure used to represent
// statements, special characters, symbols and operations generated
// from source code.
type Expression struct {
	Location

	Value    string
	Type     ExpressionType
	Children []Expression
}

// Resolve provides the list of indexed packages to internal structures
// to resolve imported or internal types that they reference. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Expression) Resolve(indexed map[string]*Package) error {

	return nil
}
