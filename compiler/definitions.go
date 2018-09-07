package compiler

import (
	"strings"

	"github.com/gokit/errors"
	"golang.org/x/tools/go/loader"
)

// ExprType defines a int type used to represent giving
// type of expressions such as assignment, multiplication,
// division, bracket closing, ...etc.
type ExprType int

// Resolvable defines an interface which exposes a method for
// resolution of internal operations.
type Resolvable interface {
	Resolve(map[string]*Package) error
}

// Identity defines an interface that exposes a single method
// to retrieve name of giving implementer.
type Identity interface {
	ID() string
}

// GeoCoordinates defines a interface which returns a Location object
// representing area (i.e declared location, line, column, etc)
// of implementing type.
type GeoCoordinates interface {
	Coordinates() *Location
}

type cloneLocation interface {
	Clone(Location)
}

// Address defines interface with exposed method to get
// Address of giving declared type.
type Address interface {
	Addr() string
}

// Expr defines a interface exposing a giving method.
type Expr interface {
	Resolvable
	GeoCoordinates
}

// ResolverFn defines a function type representing
// a function taking in a map of packages, returning
// an error.
type ResolverFn func(map[string]*Package) error

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
	Source    string
}

// Clone saves value of incoming Location as itself.
func (l *Location) Clone(m Location) {
	*l = m
}

// Coordinates implements the Expr interface.
func (l *Location) Coordinates() *Location {
	return l
}

// Pathway stores giving PathwayName value for  type.
type Pathway struct {
	Path string
}

// Addr implements the Expr interface and returns the vale of Pathway.Path field.
func (f Pathway) Addr() string {
	return f.Path
}

// Tag embodies a field tag and it's value declared
// for a struct field.
type Tag struct {
	Name  string
	Value string
	Text  string
	Meta  []string
}

// Annotation embodies an annotation declaration made
// in regards to a declared struct, type or interface
// declaration either within declaration commentary or
// within source files.
type Annotation struct {
	Name     string
	Template string
	After    []string
	Flags    []string
	Params   map[string]string
}

// ID implements Identity.
func (p Annotation) ID() string {
	return p.Name
}

// Resolve implements Resolvable interface.
func (p *Annotation) Resolve(indexed map[string]*Package) error {
	return nil
}

// DocText embodies a file level text not
// associated with any declaration exiting
// within a file.
type DocText struct {
	Location *Location
	Text     string
}

// Doc represents the associated documentation for
// a giving package or type declaration. It contains
// the main text which is the first paragraph of the
// commentary and the extra commentary which are seperated
// by 2 newline spacing.
type Doc struct {
	Location *Location

	Text  string
	Parts []DocText
}

// Meta defines the basic package related information
// attached to a giving structure declaration or variable
// pointing to it's origin of declaration.
type Meta struct {
	Name string
	Path string
}

type commentaryDocs interface {
	SetDoc(Doc)
	AddDoc(Doc)
}

// Commentaries defines a struct which embodies all comments and annotations
// associated with a declaration.
type Commentaries struct {
	Doc         Doc
	Docs        []Doc
	Annotations []Annotation
}

// SetDoc sets giving doc as commentaries main Doc.
func (c *Commentaries) SetDoc(doc Doc) {
	c.Doc = doc
}

// AddDoc adds new document into list of Docs.
func (c *Commentaries) AddDoc(doc Doc) {
	c.Docs = append(c.Docs, doc)
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
	Docs    []Doc
}

// Expression provides a generic structure used to represent
// statements, special characters, symbols and operations generated
// from source code.
type Expression struct {
	Location
	Commentaries

	Value    string
	Type     ExprType
	Children []Expression
}

// Ref returns an empty string, has Expression
// has no giving reference string.
func (e Expression) Ref() string {
	return ""
}

// PackageFile defines a giving package file with it's associated
// definitions, constructs and declarations. It provides the
// one-to-one relation of a parsed package.
type PackageFile struct {
	Name    string
	File    string
	Dir     string
	Docs    []Doc
	Imports map[string]Import
}

// Package embodies a parsed Golang/Go based package,
// with it's information and package files, which embody
// it's declarations, types and constructs.
type Package struct {
	Meta *loader.PackageInfo `json:"-"`

	Name       string
	Docs       []Doc
	Blanks     []Variable
	BadDeclrs  []BadExpr
	Variables  map[string]*Variable
	Constants  map[string]*Variable
	Types      map[string]*Type
	Structs    map[string]*Struct
	Interfaces map[string]*Interface
	Functions  map[string]*Function
	Methods    map[string]*Function
	Depends    map[string]*Package
	Files      map[string]*PackageFile
}

// GetConstant attempts to return Constant reference declared in Package.
func (p *Package) GetConstant(methodName string) (*Variable, error) {
	points := []string{p.Name, methodName}
	addr := strings.Join(points, ".")
	if method, ok := p.Constants[addr]; ok {
		return method, nil
	}
	return nil, errors.Wrap(ErrNotFound, "Constant with addrs %q not found", addr)
}

// GetVariable attempts to return Variable reference declared in Package.
func (p *Package) GetVariable(methodName string) (*Variable, error) {
	points := []string{p.Name, methodName}
	addr := strings.Join(points, ".")
	if method, ok := p.Variables[addr]; ok {
		return method, nil
	}
	return nil, errors.Wrap(ErrNotFound, "Variable with addrs %q not found", addr)
}

// GetType attempts to return Type reference declared in Package.
func (p *Package) GetType(methodName string) (*Type, error) {
	points := []string{p.Name, methodName}
	addr := strings.Join(points, ".")
	if method, ok := p.Types[addr]; ok {
		return method, nil
	}
	return nil, errors.Wrap(ErrNotFound, "Type with addrs %q not found", addr)
}

// GetStruct attempts to return Struct reference declared in Package.
func (p *Package) GetStruct(methodName string) (*Struct, error) {
	points := []string{p.Name, methodName}
	addr := strings.Join(points, ".")
	if method, ok := p.Structs[addr]; ok {
		return method, nil
	}
	return nil, errors.Wrap(ErrNotFound, "Struct with addrs %q not found", addr)
}

// GetInterface attempts to return interface reference declared in Package.
func (p *Package) GetInterface(methodName string) (*Interface, error) {
	points := []string{p.Name, methodName}
	addr := strings.Join(points, ".")
	if method, ok := p.Interfaces[addr]; ok {
		return method, nil
	}
	return nil, errors.Wrap(ErrNotFound, "Interface with addrs %q not found", addr)
}

// GetFunctionFor attempts to return Function reference for giving package function declared
// in Package, from Package.Functions dictionary.
func (p *Package) GetFunctionFor(methodName string) (*Function, error) {
	points := []string{p.Name, methodName}
	addr := strings.Join(points, ".")
	if method, ok := p.Functions[addr]; ok {
		return method, nil
	}
	return nil, errors.Wrap(ErrNotFound, "function with addrs %q not found", addr)
}

// GetMethodFor attempts to return Function reference for giving method associated
// with type from Package.Methods dictionary.
func (p *Package) GetMethodFor(typeName string, methodName string) (*Function, error) {
	points := []string{p.Name, typeName, methodName}
	addr := strings.Join(points, ".")
	if method, ok := p.Methods[addr]; ok {
		return method, nil
	}
	return nil, errors.Wrap(ErrNotFound, "method with addrs %q not found", addr)
}

// Add adds giving declaration into package declaration
// types according to it's class.
func (p *Package) Add(obj interface{}) error {
	switch elem := obj.(type) {
	case BadExpr:
		p.BadDeclrs = append(p.BadDeclrs, elem)
	case Doc:
		p.Docs = append(p.Docs, elem)
	case *Package:
		p.Depends[elem.Name] = elem
	case Type:
		p.Types[elem.Addr()] = &elem
	case Interface:
		p.Interfaces[elem.Addr()] = &elem
	case Struct:
		p.Structs[elem.Addr()] = &elem
	case Function:
		if elem.IsMethod {
			p.Methods[elem.Addr()] = &elem
		} else {
			p.Functions[elem.Addr()] = &elem
		}
	case Variable:
		if elem.Blank {
			p.Blanks = append(p.Blanks, elem)
			return nil
		}

		if elem.Constant {
			p.Constants[elem.Name] = &elem
			return nil
		}

		p.Variables[elem.Name] = &elem
	case *Type:
		p.Types[elem.Addr()] = elem
	case *Interface:
		p.Interfaces[elem.Addr()] = elem
	case *Struct:
		p.Structs[elem.Addr()] = elem
	case *Function:
		if elem.IsMethod {
			p.Methods[elem.Addr()] = elem
		} else {
			p.Functions[elem.Addr()] = elem
		}
	case *Variable:
		if elem.Blank {
			p.Blanks = append(p.Blanks, *elem)
			return nil
		}

		p.Variables[elem.Name] = elem
	}
	return nil
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathway. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Package) Resolve(indexed map[string]*Package) error {
	for _, blank := range p.Blanks {
		if err := blank.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, vars := range p.Variables {
		if err := vars.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, tp := range p.Types {
		if err := tp.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, str := range p.Structs {
		if err := str.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, itr := range p.Interfaces {
		if err := itr.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, fn := range p.Functions {
		if err := fn.Resolve(indexed); err != nil {
			return err
		}
	}
	return nil
}

// ReturnsExpr represents giving Returns loop.
type ReturnsExpr struct {
	Commentaries
	Location
}

// ID implements Identity.
func (p ReturnsExpr) ID() string {
	return "Returns"
}

// Resolve implements Resolvable interface.
func (p *ReturnsExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// AssignExpr represents giving Assign loop.
type AssignExpr struct {
	Commentaries
	Location
}

// ID implements Identity.
func (p AssignExpr) ID() string {
	return "Assign"
}

// Resolve implements Resolvable interface.
func (p *AssignExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// CallExpr represents giving Call loop.
type CallExpr struct {
	Commentaries
	Location
}

// ID implements Identity.
func (p CallExpr) ID() string {
	return "Call"
}

// Resolve implements Resolvable interface.
func (p *CallExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// RangeExpr represents giving Range loop.
type RangeExpr struct {
	Commentaries
	Location
}

// ID implements Identity.
func (p RangeExpr) ID() string {
	return "Range"
}

// Resolve implements Resolvable interface.
func (p *RangeExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// ForExpr represents giving for loop.
type ForExpr struct {
	Commentaries
	Location
}

// ID implements Identity.
func (p ForExpr) ID() string {
	return "for"
}

// Resolve implements Resolvable interface.
func (p *ForExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// SymbolExpr represents giving char expression like Bracket, + , -
// symbols used in code.
type SymbolExpr struct {
	Commentaries
	Location

	// Symbol contains symbol expression rune which is represented by
	// giving Symbol.
	Symbol rune
}

// ID implements Identity.
func (p SymbolExpr) ID() string {
	return string(p.Symbol)
}

// Resolve implements Resolvable interface.
func (p *SymbolExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// PropertyCallExpr represents giving char expression like Bracket, + , -
// PropertyCalls used in code.
type PropertyCallExpr struct {
	Commentaries
	Location
}

// ID implements Identity.
func (p PropertyCallExpr) ID() string {
	return "PropertyCall"
}

// Resolve implements Resolvable interface.
func (p *PropertyCallExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// IfExpr represents giving char expression like Bracket, + , -
// Ifs used in code.
type IfExpr struct {
	Commentaries
	Location
}

// ID implements Identity.
func (p IfExpr) ID() string {
	return "if"
}

// Resolve implements Resolvable interface.
func (p *IfExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// BadExpr represents a bad declaration or expression error
// found within a declared source file. It is used to represent
// any syntax error within a declaration like a struct, interface
// or the body of a function.
type BadExpr struct {
	Commentaries
	Location
}

// ID implements Identity.
func (p BadExpr) ID() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *BadExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// KeyPair represents a key-value pair declaration.
type KeyPair struct {
	Location
	Commentaries

	// Key defines the key name used for giving key pair.
	Key string

	// Value represents type and value associated with key pair.
	Value Identity
}

// ID implements Identity.
func (p KeyPair) ID() string {
	return "KeyPair"
}

// Resolve implements Resolvable interface.
func (p *KeyPair) Resolve(indexed map[string]*Package) error {
	return nil
}

// Map embodies a giving map type with
// an associated name, value and key type.
type Map struct {
	Location
	Commentaries

	// IsPointer indicates if giving variable type is a pointer.
	IsPointer bool

	// KeyPairs contains possible associated key-value elements provided
	// to type for declarations where type has provided values.
	KeyPairs []KeyPair

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// KeyType sets the key type for giving map type.
	KeyType Identity

	// ValueType sets the value type for giving map type.
	ValueType Identity

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// ID implements Identity interface.
func (p Map) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Map) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// List embodies a giving slice or array type with
// an associated name and type.
type List struct {
	Location
	Commentaries

	// IsPointer indicates if giving variable type is a pointer.
	IsPointer bool

	// IsSlice indicates if giving type is a slice or array type.
	IsSlice bool

	// Name represents the name of giving interface.
	Name string

	// Length defines giving length associated with slice or array.
	Length int64

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Values contains possible associated value elements provided
	// to type for declarations where type has provided values.
	Values []Identity

	// Type sets the value object/declared type.
	Type Identity

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// ID implements Identity interface.
func (p List) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *List) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// ValueField represents a field declared with giving value
// within a instantiated struct.
type ValueField struct {
	Commentaries
	Location

	// IsPointer indicates if giving variable type is a pointer.
	IsPointer bool

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Field sets the field which has giving value..
	Field *Field

	// Type sets the value object/declared type.
	Value Identity

	// Type sets the value object/declared type.
	Type Identity

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Channel embodies a channel type declared
// with a golang package.
type Channel struct {
	Commentaries
	Location

	// IsPointer indicates if giving variable type is a pointer.
	IsPointer bool

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Type sets the value object/declared type.
	Type Identity

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// ID implements Identity interface.
func (p Channel) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Channel) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Base represents a golang base types which include
// strings, int types, floats, complex, etc, which are
// atomic indivisible types.
type Base struct {
	Location
	Commentaries

	// IsPointer indicates if giving variable type is a pointer.
	IsPointer bool

	// Name represents the name of giving type.
	Name string

	// Value contains associated value of giving base type if
	// is a variable.
	Value string
}

// BaseFor returns a new instance of Base using provided Name.
func BaseFor(baseName string) *Base {
	return &Base{
		Name: baseName,
	}
}

// BaseWith returns a new instance of Base using provided Name and value.
func BaseWith(baseName string, value string) *Base {
	return &Base{
		Value: value,
		Name:  baseName,
	}
}

// ID implements the Identity interface.
func (p Base) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Base) Resolve(indexed map[string]*Package) error {
	return nil
}

// Variable embodies data related to declared
// variable.
type Variable struct {
	Pathway
	Location
	Commentaries

	// Type sets the value object/declared type.
	Type Identity

	// IsPointer indicates if giving variable type is a pointer.
	IsPointer bool

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Constant is used to flag giving variable as a go const
	// i.e preceded with a const keyword or part of a const block.
	Constant bool

	// Blank is used to indicate if variable name is blank.
	Blank bool

	// IsShortHand is used to indicate if variable is declared
	// in golang short hand or normal format.
	IsShortHand bool

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Variable) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Field represents field types and names
// declared as part of a types's properties.
type Field struct {
	Pathway
	Location
	Commentaries

	// Exported holds giving flag where field is an
	// exported field or not.
	Exported bool

	// Name represents the name of giving interface.
	Name string

	// Tags contains a all declared tags for giving fields.
	Tags []Tag

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// Type sets the value object/declared type.
	Type Identity

	// Import contains import details for giving field type used in Pathwayerence
	// within declaration of struct.
	Import *Import

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// ID implements Identity interface.
func (p Field) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Field) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Parameter represents argument and return types
// provided to a function, method or function type
// declaration.
type Parameter struct {
	Pathway
	Location
	Commentaries

	// Name represents the name of giving interface.
	Name string

	// Type sets the value object/declared type.
	Type Identity

	// IsVariadic indicates if giving parameter is variadic.
	IsVariadic bool

	// Import contains import details for giving field type used in Pathwayerence
	// within declaration of argument.
	Import *Import

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// ID implements Identity interface.
func (p Parameter) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Parameter) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Type defines a struct holding information about
// a defined custom type based on an existing type.
type Type struct {
	Pathway
	Location
	Commentaries

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// IsFunction sets if giving type declaration is a function type declaration.
	IsFunction bool

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// Points sets the real type which giving type declaration points to.
	Points Identity

	// Methods contains all function defined as methods attached to
	// type instance.
	Methods map[string]Function

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// ID implements Identity interface.
func (p Type) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Type) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Interface embodies necessary data related to declared
// interface types within a package.
type Interface struct {
	Pathway
	Location
	Commentaries

	// Meta provides associated package and commentary information related to
	// giving type.
	Meta Meta

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Composes contains all other interface types composed by
	// given interface type.
	Composes map[string]*Interface

	// Methods contains all method definitions/rules provided
	// as contract for interface implementors.
	Methods map[string]Function

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// ID implements Identity interface.
func (p Interface) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Interface) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	for _, method := range p.Methods {
		if err := method.Resolve(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Struct embodies necessary data related to declared
// struct types within a package.
type Struct struct {
	Pathway
	Location
	Commentaries

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Composes contains all interface types composed by
	// given struct type.
	Composes map[string]*Interface

	// Embeds contains all struct types composed by
	// given struct type.
	Embeds map[string]*Struct

	// Fields contains all fields and associated types declared
	// as members of struct.
	Fields map[string]Field

	// Methods contains all function defined as methods attached to
	// struct instance.
	Methods map[string]Function

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// ID implements Identity interface.
func (p Struct) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Struct) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	for _, field := range p.Fields {
		if err := field.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, method := range p.Methods {
		if err := method.Resolve(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Function embodies data related to declared
// package functions, struct methods, interface
// methods or function closures.
type Function struct {
	Pathway
	Location
	Commentaries

	// Body contains contents of Function containing
	// all statement declared within as it's body and
	// operation.
	Body []Expr

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// IsMethod indicates if giving function is a method of a struct or a method
	// contract for an interface.
	IsMethod bool

	// IsAsync indicates whether function is called
	// asynchronously in goroutine.
	IsAsync bool

	// Owner sets the struct or interface which this function is attached
	// to has a method.
	Owner Identity

	// Arguments provides the argument list for giving function.
	Arguments []Parameter

	// Arguments provides the argument list for giving function.
	Returns []Parameter

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
}

// ID implements Identity interface.
func (p Function) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Function) Resolve(indexed map[string]*Package) error {
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	for _, param := range p.Returns {
		if err := param.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, param := range p.Arguments {
		if err := param.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, body := range p.Body {
		if err := body.Resolve(indexed); err != nil {
			return err
		}
	}
	return nil
}
