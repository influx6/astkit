package compiler

import (
	"strings"

	"github.com/gokit/errors"
)

// ExprType defines a int type used to represent giving
// type of expressions such as assignment, multiplication,
// division, bracket closing, ...etc.
type ExprType int

// constant set of express types represented by ExprType.
const (
	FunctionBody ExprType = iota + 1
	StatementBlock
	LabelBlock
)

// Resolvable defines an interface which exposes a method for
// resolution of internal operations.
type Resolvable interface {
	Resolve(map[string]*Package) error
}

// Address defines interface with exposed method to get
// Address of giving declared type.
type Address interface {
	Addr() string
}

// Identity defines an interface that exposes a single method
// to retrieve name of giving implementer.
type Identity interface {
	ID() string
}

// Expr defines a type which composes Identity interface and exposes a method to return
// string version of an expression.
type Expr interface {
	Identity
	Expr() string
}

// SetIdentity defines an interface which exposes a method to set
// the id of it's implementer.
type SetIdentity interface {
	SetID(string)
}

// SourcePoint defines a interface which returns a Location object
// representing area (i.e declared location, line, column, etc)
// of implementing type.
type SourcePoint interface {
	Coordinates() *Location
}

// ExprSymbol defines an interface that exposes two methods
// to retrieve the starting and ending symbol of an expression.
type ExprSymbol interface {
	End() string
	Begin() string
}

// ResolverFn defines a function type representing
// a function taking in a map of packages, returning
// an error.
type ResolverFn func(map[string]*Package) error

// PackageFile defines a giving package file with it's associated
// definitions, constructs and declarations. It provides the
// one-to-one relation of a parsed package.
type PackageFile struct {
	Name      string
	File      string
	Dir       string
	Docs      []Doc
	Cgo       bool
	Archs     map[string]bool
	Platforms map[string]bool
	Imports   map[string]Import
}

// HasPlatform returns true/false if giving platform is supported.
func (l *PackageFile) HasPlatform(plat string) bool {
	return l.Platforms[plat]
}

// HasArch returns true/false if giving arch is supported.
func (l *PackageFile) HasArch(arch string) bool {
	return l.Archs[arch]
}

// IsCgoSpecific returns true/false if giving location data is cgo based.
func (l *PackageFile) IsCgoSpecific() bool {
	return l.Cgo
}

// IsArchSpecific returns true/false if giving location data is architecture constraints.
func (l *PackageFile) IsArchSpecific() bool {
	return len(l.Archs) != 0
}

// IsPlatformSpecific returns true/false if giving location data is platform specific.
func (l *PackageFile) IsPlatformSpecific() bool {
	return len(l.Platforms) != 0
}

// PlatformPackage provides a package-grouping around a giving class/group value.
// It allows grouping types specific to a platform within a package.
type PlatformPackage struct {
	Name     string
	Platform string

	NoNameStructs    []*Struct
	Variables        map[string]*Variable
	Constants        map[string]*Variable
	Types            map[string]*Type
	Structs          map[string]*Struct
	Interfaces       map[string]*Interface
	Functions        map[string]*Function
	Methods          map[string]*Function
	MethodByReceiver map[string]*Function
	Scopes           map[string]FunctionScope
}

// Platform returns a new instance of PlatformPackage.
func Platform(platform string, pkg string) *PlatformPackage {
	return &PlatformPackage{
		Name:             pkg,
		Platform:         platform,
		Types:            map[string]*Type{},
		Structs:          map[string]*Struct{},
		Variables:        map[string]*Variable{},
		Constants:        map[string]*Variable{},
		Functions:        map[string]*Function{},
		Methods:          map[string]*Function{},
		MethodByReceiver: map[string]*Function{},
		Interfaces:       map[string]*Interface{},
		Scopes:           map[string]FunctionScope{},
	}
}

// GetConstant attempts to return Constant reference declared in Package.
func (p *PlatformPackage) GetConstant(addr string) (*Variable, error) {
	if target, ok := p.Constants[addr]; ok {
		return target, nil
	}
	return nil, errors.New("Constant with addrs %q not found", addr)
}

// GetReference returns giving Expr which matches giving reference.
func (p *PlatformPackage) GetReference(ref string) (Expr, error) {
	if target, ok := p.Structs[ref]; ok {
		return target, nil
	}

	if target, ok := p.Types[ref]; ok {
		return target, nil
	}

	if target, ok := p.Interfaces[ref]; ok {
		return target, nil
	}

	if target, ok := p.Functions[ref]; ok {
		return target, nil
	}

	if target, ok := p.Variables[ref]; ok {
		return target, nil
	}

	if target, ok := p.Constants[ref]; ok {
		return target, nil
	}

	if target, ok := p.Methods[ref]; ok {
		return target, nil
	}

	return nil, errors.New("reference %q not found in %q", ref, p.Name)
}

// GetVariable attempts to return Variable reference declared in Package.
func (p *PlatformPackage) GetVariable(addr string) (*Variable, error) {
	if target, ok := p.Variables[addr]; ok {
		return target, nil
	}

	return nil, errors.New("Variable with addrs %q not found", addr)
}

// GetType attempts to return Type reference declared in Package.
func (p *PlatformPackage) GetType(addr string) (*Type, error) {
	if target, ok := p.Types[addr]; ok {
		return target, nil
	}
	return nil, errors.New("Type with addrs %q not found", addr)
}

// GetStruct attempts to return Struct reference declared in Package.
func (p *PlatformPackage) GetStruct(addr string) (*Struct, error) {
	if target, ok := p.Structs[addr]; ok {
		return target, nil
	}
	return nil, errors.New("Struct with addrs %q not found", addr)
}

// GetInterface attempts to return interface reference declared in Package.
func (p *PlatformPackage) GetInterface(addr string) (*Interface, error) {
	if target, ok := p.Interfaces[addr]; ok {
		return target, nil
	}
	return nil, errors.New("Interface with addrs %q not found", addr)
}

// GetFunctionFor attempts to return Function reference for giving package function declared
// in Package, from Package.Functions dictionary.
func (p *PlatformPackage) GetFunctionFor(addr string) (*Function, error) {
	if target, ok := p.Functions[addr]; ok {
		return target, nil
	}
	return nil, errors.New("function with addrs %q not found", addr)
}

// GetMethodFor attempts to return Function reference for giving method associated
// with type from Package.Methods dictionary.
func (p *PlatformPackage) GetMethodFor(addr string) (*Function, error) {
	if target, ok := p.Methods[addr]; ok {
		return target, nil
	}
	return nil, errors.New("method with addrs %q not found", addr)
}

func (p *PlatformPackage) addFunction(elem *Function) error {
	if elem.IsMethod {
		p.Methods[elem.Addr()] = elem

		// If it has a receiver instance name then add to methods with receivers.
		if elem.ReceiverName != "" {
			p.MethodByReceiver[elem.ReceiverAddr] = elem
		}

		return nil
	}

	p.Functions[elem.Addr()] = elem
	return nil
}

func (p *PlatformPackage) addType(elem *Type) error {
	p.Types[elem.Addr()] = elem
	return nil
}

func (p *PlatformPackage) addStruct(elem *Struct) error {
	if elem.Name == "" {
		p.NoNameStructs = append(p.NoNameStructs, elem)
		return nil
	}
	p.Structs[elem.Addr()] = elem
	return nil
}

func (p *PlatformPackage) addInterface(elem *Interface) error {
	p.Interfaces[elem.Addr()] = elem
	return nil
}

func (p *PlatformPackage) addVariable(elem *Variable) error {
	if elem.Constant {
		p.Constants[elem.Addr()] = elem
		return nil
	}

	p.Variables[elem.Addr()] = elem
	return nil
}

// Arch contains a map for PlatformPackages organized
// for access.
type Arch struct {
	Name      string
	Archs     map[string]*PlatformPackage
	Platforms map[string]*PlatformPackage
}

// NewArch returns a new instance of Arch.
func NewArch(name string) *Arch {
	return &Arch{
		Name:      name,
		Archs:     map[string]*PlatformPackage{},
		Platforms: map[string]*PlatformPackage{},
	}
}

// GetConstant attempts to return Constant reference declared in Package.
// *arch argument can be used to specify a architecture or platform grouping.
func (p *Arch) GetConstant(targetName string, arch string) (*Variable, error) {
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")

	if targetMap, ok := p.Archs[arch]; ok {
		return targetMap.GetConstant(addr)
	}

	if targetMap, ok := p.Platforms[arch]; ok {
		return targetMap.GetConstant(addr)
	}

	return nil, errors.New("not found")
}

// GetVariable attempts to return Variable reference declared in Package.
// *arch argument can be used to specify a architecture or platform grouping.
func (p *Arch) GetVariable(targetName string, arch string) (*Variable, error) {
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")

	if targetMap, ok := p.Archs[arch]; ok {
		return targetMap.GetConstant(addr)
	}

	if targetMap, ok := p.Platforms[arch]; ok {
		return targetMap.GetConstant(addr)
	}

	return nil, errors.New("not found")
}

// GetType attempts to return Type reference declared in Package.
// *arch argument can be used to specify a architecture or platform grouping.
func (p *Arch) GetType(targetName string, arch string) (*Type, error) {
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")

	if targetMap, ok := p.Archs[arch]; ok {
		return targetMap.GetType(addr)
	}

	if targetMap, ok := p.Platforms[arch]; ok {
		return targetMap.GetType(addr)
	}

	return nil, errors.New("not found")
}

// GetStruct attempts to return Struct reference declared in Package.
// *arch argument can be used to specify a architecture or platform grouping.
func (p *Arch) GetStruct(targetName string, arch string) (*Struct, error) {
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")

	if targetMap, ok := p.Archs[arch]; ok {
		return targetMap.GetStruct(addr)
	}

	if targetMap, ok := p.Platforms[arch]; ok {
		return targetMap.GetStruct(addr)
	}

	return nil, errors.New("not found")
}

// GetInterface attempts to return interface reference declared in Package.
// *arch argument can be used to specify a architecture or platform grouping.
func (p *Arch) GetInterface(targetName string, arch string) (*Interface, error) {
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")

	if targetMap, ok := p.Archs[arch]; ok {
		return targetMap.GetInterface(addr)
	}

	if targetMap, ok := p.Platforms[arch]; ok {
		return targetMap.GetInterface(addr)
	}

	return nil, errors.New("not found")
}

// GetFunctionFor attempts to return Function reference for giving package function declared
// in Package, from Package.Functions dictionary.
// *arch argument can be used to specify a architecture or platform grouping.
func (p *Arch) GetFunctionFor(targetName string, arch string) (*Function, error) {
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")

	if targetMap, ok := p.Archs[arch]; ok {
		return targetMap.GetFunctionFor(addr)
	}

	if targetMap, ok := p.Platforms[arch]; ok {
		return targetMap.GetFunctionFor(addr)
	}

	return nil, errors.New("not found")
}

// GetMethodFor attempts to return Function reference for giving method associated
// with type from Package.Methods dictionary.
// *arch argument can be used to specify a architecture or platform grouping.
func (p *Arch) GetMethodFor(typeName string, targetName string, arch string) (*Function, error) {
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")

	if targetMap, ok := p.Archs[arch]; ok {
		return targetMap.GetMethodFor(addr)
	}

	if targetMap, ok := p.Platforms[arch]; ok {
		return targetMap.GetMethodFor(addr)
	}

	return nil, errors.New("not found")
}

// GetPlatform returns a new Platform package for a giving platform.
func (p *Arch) GetPlatform(platform string) *PlatformPackage {
	if archPackage, ok := p.Archs[platform]; ok {
		return archPackage
	}

	platfm := Platform(platform, p.Name)
	p.Platforms[platform] = platfm
	return platfm
}

// GetArch returns a new Platform package for a giving architecture.
func (p *Arch) GetArch(arch string) *PlatformPackage {
	if archPackage, ok := p.Archs[arch]; ok {
		return archPackage
	}
	platfm := Platform(arch, p.Name)
	p.Archs[arch] = platfm
	return platfm
}

// GetReferenceByArch returns giving Expr which matches giving reference for a set of architectures.
// If non is found, then we check the platforms else return errors.New("not found").
func (p *Arch) GetReferenceByArch(ref string, archs map[string]bool) (Expr, error) {
	for k := range archs {
		if plat, ok := p.Archs[k]; ok {
			if pm, err := plat.GetReference(ref); err == nil {
				return pm, nil
			}
		}
	}

	for _, pkg := range p.Platforms {
		if pm, err := pkg.GetReference(ref); err == nil {
			return pm, nil
		}
	}

	return nil, errors.New("not found")
}

// GetReference returns giving Expr which matches giving reference.
// If non is found, then we check the platforms else return errors.New("not found").
func (p *Arch) GetReference(ref string) (Expr, error) {
	for _, pkg := range p.Archs {
		if pm, err := pkg.GetReference(ref); err == nil {
			return pm, nil
		}
	}

	for _, pkg := range p.Platforms {
		if pm, err := pkg.GetReference(ref); err == nil {
			return pm, nil
		}
	}

	return nil, errors.New("not found")
}

// Package embodies a parsed Golang/Go based package,
// with it's information and package files, which embody
// it's declarations, types and constructs.
type Package struct {
	Name      string
	Docs      []Doc
	BadDeclrs []*BadExpr

	// CgoPackages holds architecture and platform specific types
	// and declarations for cgo based types.
	CgoPackages *Arch

	// NormalPackages holds architecture and platform specific types
	// and declarations for non-cgo based types.
	NormalPackages *Arch

	// All architecture allowed types and variables.
	Blanks           []*Variable
	NoNameStructs    []*Struct
	Annotations      []Annotation
	Depends          map[string]*Package
	Variables        map[string]*Variable
	Constants        map[string]*Variable
	Types            map[string]*Type
	Structs          map[string]*Struct
	Interfaces       map[string]*Interface
	Functions        map[string]*Function
	Methods          map[string]*Function
	MethodByReceiver map[string]*Function
	Files            map[string]*PackageFile
	Scopes           map[string]FunctionScope

	// set of resolvers which are layered out in
	// order of calling. Block resolvers must be resolved
	// last as they could use variables and variables resolvers
	// must be resolved as they could refer to a struct
	// field type.
	baseResolvers  []Resolvable
	namedResolvers []Resolvable
	varResolvers   []Resolvable
	blockResolvers []Resolvable
	postResolvers  []ResolverFn
	resolved       bool
}

// GetConstant attempts to return Constant reference declared in Package.
// *arch argument can be used to specify a architecture or platform grouping.
func (p *Package) GetConstant(targetName string, arch string) (*Variable, error) {
	if arch != "" {
		if target, err := p.NormalPackages.GetConstant(targetName, arch); err == nil {
			return target, nil
		}

		if target, err := p.CgoPackages.GetConstant(targetName, arch); err == nil {
			return target, nil
		}
	}

	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")
	if target, ok := p.Constants[addr]; ok {
		return target, nil
	}
	return nil, errors.New("Constant with addrs %q not found", addr)
}

// GetVariable attempts to return Variable reference declared in Package.
func (p *Package) GetVariable(targetName string, arch string) (*Variable, error) {
	if arch != "" {
		if target, err := p.NormalPackages.GetVariable(targetName, arch); err == nil {
			return target, nil
		}

		if target, err := p.CgoPackages.GetVariable(targetName, arch); err == nil {
			return target, nil
		}
	}

	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")
	if target, ok := p.Variables[addr]; ok {
		return target, nil
	}
	return nil, errors.New("Variable with addrs %q not found", addr)
}

// GetType attempts to return Type reference declared in Package.
func (p *Package) GetType(targetName string, arch string) (*Type, error) {
	if arch != "" {
		if target, err := p.NormalPackages.GetType(targetName, arch); err == nil {
			return target, nil
		}

		if target, err := p.CgoPackages.GetType(targetName, arch); err == nil {
			return target, nil
		}
	}
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")
	if target, ok := p.Types[addr]; ok {
		return target, nil
	}
	return nil, errors.New("Type with addrs %q not found", addr)
}

// GetStruct attempts to return Struct reference declared in Package.
func (p *Package) GetStruct(targetName string, arch string) (*Struct, error) {
	if arch != "" {
		if target, err := p.NormalPackages.GetStruct(targetName, arch); err == nil {
			return target, nil
		}

		if target, err := p.CgoPackages.GetStruct(targetName, arch); err == nil {
			return target, nil
		}
	}
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")
	if target, ok := p.Structs[addr]; ok {
		return target, nil
	}
	return nil, errors.New("Struct with addrs %q not found", addr)
}

// GetInterface attempts to return interface reference declared in Package.
func (p *Package) GetInterface(targetName string, arch string) (*Interface, error) {
	if arch != "" {
		if target, err := p.NormalPackages.GetInterface(targetName, arch); err == nil {
			return target, nil
		}

		if target, err := p.CgoPackages.GetInterface(targetName, arch); err == nil {
			return target, nil
		}
	}
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")
	if target, ok := p.Interfaces[addr]; ok {
		return target, nil
	}
	return nil, errors.New("Interface with addrs %q not found", addr)
}

// GetFunctionFor attempts to return Function reference for giving package function declared
// in Package, from Package.Functions dictionary.
func (p *Package) GetFunctionFor(targetName string, arch string) (*Function, error) {
	if arch != "" {
		if target, err := p.NormalPackages.GetFunctionFor(targetName, arch); err == nil {
			return target, nil
		}

		if target, err := p.CgoPackages.GetFunctionFor(targetName, arch); err == nil {
			return target, nil
		}
	}
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")
	if target, ok := p.Functions[addr]; ok {
		return target, nil
	}
	return nil, errors.New("function with addrs %q not found", addr)
}

// GetMethodFor attempts to return Function reference for giving method associated
// with type from Package.Methods dictionary.
func (p *Package) GetMethodFor(typeName string, targetName string, arch string) (*Function, error) {
	if arch != "" {
		if target, err := p.NormalPackages.GetMethodFor(typeName, targetName, arch); err == nil {
			return target, nil
		}

		if target, err := p.CgoPackages.GetMethodFor(typeName, targetName, arch); err == nil {
			return target, nil
		}
	}

	points := []string{p.Name, typeName, targetName}
	addr := strings.Join(points, ".")
	if target, ok := p.Methods[addr]; ok {
		return target, nil
	}
	return nil, errors.New("method with addrs %q not found", addr)
}

// GetReferenceByArchs returns a giving reference by targeting by map of architecture.
func (p *Package) GetReferenceByArchs(ref string, archs map[string]bool, cgo bool) (Expr, error) {
	for k := range archs {
		if cgo {
			if pm, err := p.CgoPackages.GetArch(k).GetReference(ref); err == nil {
				return pm, nil
			}
			continue
		}

		if pm, err := p.NormalPackages.GetArch(k).GetReference(ref); err == nil {
			return pm, nil
		}
	}
	return p.GetReference(ref)
}

// GetReferenceByArch returns a giving reference by targeting by platform.
func (p *Package) GetReferenceByArch(ref string, arch string, cgo bool) (Expr, error) {
	if cgo {
		if pm, err := p.CgoPackages.GetArch(arch).GetReference(ref); err == nil {
			return pm, nil
		}
	}
	if pm, err := p.NormalPackages.GetArch(arch).GetReference(ref); err == nil {
		return pm, nil
	}
	return p.GetReference(ref)
}

// GetReferenceByPlatform returns a giving reference by targeting by platform.
func (p *Package) GetReferenceByPlatform(ref string, platform string, cgo bool) (Expr, error) {
	if cgo {
		if pm, err := p.CgoPackages.GetPlatform(platform).GetReference(ref); err == nil {
			return pm, nil
		}
	}
	if pm, err := p.NormalPackages.GetPlatform(platform).GetReference(ref); err == nil {
		return pm, nil
	}
	return p.GetReference(ref)
}

// GetAnyTypeFromArchs will attempt to retrieve giving type from map any of provided architecture.
func (p *Package) GetAnyTypeFromArchs(targetName string, archs map[string]bool, cgo bool) (Expr, error) {
	points := []string{p.Name, targetName}
	ref := strings.Join(points, ".")
	for k := range archs {
		if cgo {
			if pm, err := p.CgoPackages.GetArch(k).GetReference(ref); err == nil {
				return pm, nil
			}
			continue
		}

		if pm, err := p.NormalPackages.GetArch(k).GetReference(ref); err == nil {
			return pm, nil
		}
	}
	return nil, errors.New("not found")
}

// GetAnyType will attempt to retrieve giving type from package regardless of type, architecture
// and platform. It returns the first found.
func (p *Package) GetAnyType(targetName string) (Expr, error) {
	points := []string{p.Name, targetName}
	addr := strings.Join(points, ".")
	return p.GetReference(addr)
}

// GetReference returns giving Expr which matches giving reference.
func (p *Package) GetReference(ref string) (Expr, error) {
	if target, ok := p.Interfaces[ref]; ok {
		return target, nil
	}

	if target, ok := p.Structs[ref]; ok {
		return target, nil
	}

	if target, ok := p.Types[ref]; ok {
		return target, nil
	}

	if target, ok := p.Functions[ref]; ok {
		return target, nil
	}

	if target, ok := p.Variables[ref]; ok {
		return target, nil
	}

	if target, ok := p.Constants[ref]; ok {
		return target, nil
	}

	if target, ok := p.Methods[ref]; ok {
		return target, nil
	}

	if pm, err := p.NormalPackages.GetReference(ref); err == nil {
		return pm, nil
	}

	if pm, err := p.CgoPackages.GetReference(ref); err == nil {
		return pm, nil
	}

	return nil, errors.New("reference %q not found in %q", ref, p.Name)
}

// Add adds giving declaration into package declaration
// types according to it's class.
func (p *Package) Add(obj interface{}) error {
	switch elem := obj.(type) {
	case Doc:
		p.Docs = append(p.Docs, elem)
		return nil
	case *Package:
		p.Depends[elem.Name] = elem
		return nil
	case *Type:
		return p.addType(elem)
	case *Interface:
		return p.addInterface(elem)
	case *Struct:
		return p.addStruct(elem)
	case *Function:
		return p.addFunction(elem)
	case *Variable:
		return p.addVariable(elem)
	}

	return errors.New("unable to add type %T", obj)
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathway. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Package) Resolve(indexed map[string]*Package) error {
	for _, dependent := range p.Depends {
		if err := dependent.Resolve(indexed); err != nil {
			return err
		}
	}

	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true

	for _, str := range p.baseResolvers {
		if err := str.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, str := range p.namedResolvers {
		if err := str.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, str := range p.varResolvers {
		if err := str.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, str := range p.blockResolvers {
		if err := str.Resolve(indexed); err != nil {
			return err
		}
	}
	for _, str := range p.postResolvers {
		if err := str(indexed); err != nil {
			return err
		}
	}
	return nil
}

func (p *Package) addFunction(elem *Function) error {
	p.blockResolvers = append(p.blockResolvers, elem)

	// If it has cgo architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.CgoPackages.GetArch(k).addFunction(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.NormalPackages.GetArch(k).addFunction(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has cgo platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.CgoPackages.GetPlatform(k).addFunction(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.NormalPackages.GetPlatform(k).addFunction(elem); err != nil {
				return err
			}
		}
		return nil
	}

	if elem.IsMethod {
		p.Methods[elem.Addr()] = elem

		// If it has a receiver instance name then add to methods with receivers.
		if elem.ReceiverName != "" {
			p.MethodByReceiver[elem.ReceiverAddr] = elem
		}

		return nil
	}

	p.Functions[elem.Addr()] = elem
	return nil
}

func (p *Package) addType(elem *Type) error {
	p.namedResolvers = append(p.namedResolvers, elem)

	// If it has cgo architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.CgoPackages.GetArch(k).addType(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.NormalPackages.GetArch(k).addType(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has cgo platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.CgoPackages.GetPlatform(k).addType(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.NormalPackages.GetPlatform(k).addType(elem); err != nil {
				return err
			}
		}
		return nil
	}

	p.Types[elem.Addr()] = elem
	return nil
}

func (p *Package) addStruct(elem *Struct) error {
	p.baseResolvers = append(p.baseResolvers, elem)

	// If it has cgo architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.CgoPackages.GetArch(k).addStruct(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.NormalPackages.GetArch(k).addStruct(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has cgo platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.CgoPackages.GetPlatform(k).addStruct(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.NormalPackages.GetPlatform(k).addStruct(elem); err != nil {
				return err
			}
		}
		return nil
	}

	if elem.Name == "" {
		p.NoNameStructs = append(p.NoNameStructs, elem)
		return nil
	}

	p.Structs[elem.Addr()] = elem
	return nil
}

func (p *Package) addInterface(elem *Interface) error {
	p.baseResolvers = append(p.baseResolvers, elem)

	// If it has cgo architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.CgoPackages.GetArch(k).addInterface(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.NormalPackages.GetArch(k).addInterface(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has cgo platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.CgoPackages.GetPlatform(k).addInterface(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.NormalPackages.GetPlatform(k).addInterface(elem); err != nil {
				return err
			}
		}
		return nil
	}

	p.Interfaces[elem.Addr()] = elem
	return nil
}

func (p *Package) addVariable(elem *Variable) error {
	p.varResolvers = append(p.varResolvers, elem)

	if elem.Blank {
		p.Blanks = append(p.Blanks, elem)
		return nil
	}

	// If it has cgo architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.CgoPackages.GetArch(k).addVariable(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has architecture constraints, then add into individual
	// architecture map.
	if elem.IsArchSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Archs {
			if err := p.NormalPackages.GetArch(k).addVariable(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has cgo platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.CgoPackages.GetPlatform(k).addVariable(elem); err != nil {
				return err
			}
		}
		return nil
	}

	// If it has platform constraints, then add into individual
	// architecture map.
	if elem.IsPlatformSpecific() && !elem.IsCgoSpecific() {
		for k := range elem.Platforms {
			if err := p.NormalPackages.GetPlatform(k).addVariable(elem); err != nil {
				return err
			}
		}
		return nil
	}

	if elem.Constant {
		p.Constants[elem.Addr()] = elem
		return nil
	}

	p.Variables[elem.Addr()] = elem
	return nil
}

func (p *Package) addProc(res ResolverFn) {
	p.postResolvers = append(p.postResolvers, res)
}

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
	Cgo       bool
	Archs     map[string]bool
	Platforms map[string]bool
}

// HasPlatform returns true/false if giving platform is supported.
func (l *Location) HasPlatform(plat string) bool {
	return l.Platforms[plat]
}

// HasArch returns true/false if giving arch is supported.
func (l *Location) HasArch(arch string) bool {
	return l.Archs[arch]
}

// IsCgoSpecific returns true/false if giving location data is cgo based.
func (l *Location) IsCgoSpecific() bool {
	return l.Cgo
}

// IsArchSpecific returns true/false if giving location data is architecture constraints.
func (l *Location) IsArchSpecific() bool {
	return len(l.Archs) != 0
}

// IsPlatformSpecific returns true/false if giving location data is platform specific.
func (l *Location) IsPlatformSpecific() bool {
	return len(l.Platforms) != 0
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

// SetID sets n to Name field value.
func (p *Annotation) SetID(n string) {
	p.Name = n
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

// GroupStmt provides a generic structure used to represent
// statements, special characters, symbols and operations generated
// from source code.
type GroupStmt struct {
	Location
	Commentaries

	BeginSymbol string
	EndSymbol   string
	Type        ExprType
	Children    []Expr
}

// ID returns the assigned string id of giving type.
// It implements the Identity interface.
func (g GroupStmt) ID() string {
	return "GroupStmt"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (g GroupStmt) Expr() string {
	return ""
}

// ExprType returns type value of GroupStmt.
func (g GroupStmt) ExprType() ExprType {
	return g.Type
}

// End returns the symbol used by GroupStmt at end of block.
func (g GroupStmt) End() string {
	return g.EndSymbol
}

// Begin returns the symbol used by GroupStmt at start of block.
func (g GroupStmt) Begin() string {
	return g.BeginSymbol
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathway. This is
// used to ensure all package structures have direct link to parsed
// type.
func (g *GroupStmt) Resolve(indexed map[string]*Package) error {
	for _, child := range g.Children {
		if rs, ok := child.(Resolvable); ok {
			if err := rs.Resolve(indexed); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReturnsExpr represents a giving return statement.
type ReturnsExpr struct {
	Commentaries
	Location

	Results []Expr
}

// ID implements Expr.
func (p ReturnsExpr) ID() string {
	return "Returns"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p ReturnsExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *ReturnsExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// EmptyExpr represents giving function passed into a goroutine using the "go" keyword.
type EmptyExpr struct {
	Location
	Implicit bool
}

// ID implements Expr.
func (p EmptyExpr) ID() string {
	return "go"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p EmptyExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *EmptyExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// GoExpr represents giving function passed into a goroutine using the "go" keyword.
type GoExpr struct {
	Commentaries
	Location

	Fn *CallExpr
}

// ID implements Expr.
func (p GoExpr) ID() string {
	return "go"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p GoExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *GoExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// ChanDir defines direction type of giving declared
// or called channel.
type ChanDir int

// types of channel direction.
const (
	Receiving ChanDir = iota + 1
	Sending
)

// ChanDirExpr represents giving Assign expression.
type ChanDirExpr struct {
	Commentaries
	Location

	Dir      ChanDir
	Receiver Expr
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p ChanDirExpr) Expr() string {
	return ""
}

// ID implements Expr.
func (p ChanDirExpr) ID() string {
	return "Assign"
}

// Resolve implements Resolvable interface.
func (p *ChanDirExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// AssignExpr represents giving Assign expression.
type AssignExpr struct {
	Commentaries
	Location

	Pairs []VariableValuePair
}

// ID implements Expr.
func (p AssignExpr) ID() string {
	return "Assign"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p AssignExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *AssignExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// IndexedProperty represents giving property retrieved from a indexed expression.
type IndexedProperty struct {
	Index    *IndexExpr
	Property Expr
}

// ID implements Expr.
func (p IndexedProperty) ID() string {
	return "IndexedProperty"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p IndexedProperty) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *IndexedProperty) Resolve(indexed map[string]*Package) error {
	return nil
}

// SliceExpr represents giving Call expression.
type SliceExpr struct {
	Commentaries
	Location

	Target  Expr
	Max     Expr
	Lowest  Expr
	Highest Expr
}

// ID implements Expr.
func (p SliceExpr) ID() string {
	return "SliceExpr"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p SliceExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *SliceExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// IndexExpr represents giving Call expression.
type IndexExpr struct {
	Commentaries
	Location

	Elem  Expr
	Index Expr
}

// ID implements Expr.
func (p IndexExpr) ID() string {
	return "IndexExpr"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p IndexExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *IndexExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// TypeAssert represents giving Call expression.
type TypeAssert struct {
	Commentaries
	Location

	X    Expr
	Type Expr
}

// ID implements Expr.
func (p TypeAssert) ID() string {
	return "TypeAssert"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p TypeAssert) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *TypeAssert) Resolve(indexed map[string]*Package) error {
	return nil
}

// StmtExpr represents giving char expression like Bracket, + , -
// Stmts used in code.
type StmtExpr struct {
	Commentaries
	Location

	X Expr
}

// ID implements Expr.
func (p StmtExpr) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p StmtExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *StmtExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// IncDecExpr represents giving char expression like Bracket, + , -
// IncDecs used in code.
type IncDecExpr struct {
	Commentaries
	Location

	Target Expr
	Inc    bool
	Dec    bool
}

// ID implements Expr.
func (p IncDecExpr) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p IncDecExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *IncDecExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// LabeledExpr represents giving char expression like Bracket, + , -
// Labeleds used in code.
type LabeledExpr struct {
	Commentaries
	Location

	Label string
	Stmt  Expr
}

// ID implements Expr.
func (p LabeledExpr) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p LabeledExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *LabeledExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// SelectExpr represents giving char expression like Bracket, + , -
// Selects used in code.
type SelectExpr struct {
	Commentaries
	Location
	Body *GroupStmt
}

// ID implements Expr.
func (p SelectExpr) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p SelectExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *SelectExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// SendExpr represents giving char expression like Bracket, + , -
// Sends used in code.
type SendExpr struct {
	Commentaries
	Location

	Chan  Expr
	Value Expr
}

// ID implements Expr.
func (p SendExpr) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p SendExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *SendExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// TypeSwitchExpr represents giving char expression like Bracket, + , -
// TypeSwitchs used in code.
type TypeSwitchExpr struct {
	Commentaries
	Location

	Body   *GroupStmt
	Init   Expr
	Assign Expr
}

// ID implements Expr.
func (p TypeSwitchExpr) ID() string {
	return "TypeSwitchExpr"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p TypeSwitchExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *TypeSwitchExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// DeclrExpr represents giving char expression like Bracket, + , -
// Declrs used in code.
type DeclrExpr struct {
	Commentaries
	Location
	Declr []Expr
}

// ID implements Expr.
func (p DeclrExpr) ID() string {
	return "DeclrExpr"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p DeclrExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *DeclrExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// BranchExpr represents giving char expression like Bracket, + , -
// Branchs used in code.
type BranchExpr struct {
	Commentaries
	Location

	Label Expr
}

// ID implements Expr.
func (p BranchExpr) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p BranchExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *BranchExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// DeferExpr represents giving char expression like Bracket, + , -
// Defers used in code.
type DeferExpr struct {
	Commentaries
	Location
	Fn *CallExpr
}

// ID implements Expr.
func (p DeferExpr) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p DeferExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *DeferExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// CallExpr represents giving Call expression.
type CallExpr struct {
	Commentaries
	Location

	Func      Expr
	Arguments []Expr
}

// ID implements Expr.
func (p CallExpr) ID() string {
	return p.Func.ID()
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p CallExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *CallExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// RangeExpr represents giving Range expression.
type RangeExpr struct {
	Commentaries
	Location

	X     Expr
	Key   Expr
	Value Expr
	Body  *GroupStmt
}

// ID implements Expr.
func (p RangeExpr) ID() string {
	return "Range"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p RangeExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *RangeExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// ForExpr represents giving for expression.
type ForExpr struct {
	Commentaries
	Location

	Body *GroupStmt
	Cond Expr
	Init Expr
	Post Expr
}

// ID implements Expr.
func (p ForExpr) ID() string {
	return "for"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p ForExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *ForExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// UnknownExpr represents giving Range expression.
type UnknownExpr struct {
	Location
	Error error
	File  *PackageFile
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p UnknownExpr) Expr() string {
	return ""
}

// ID implements Expr.
func (p UnknownExpr) ID() string {
	return "UnknownExpr"
}

// Resolve implements Resolvable interface.
func (p *UnknownExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// ParenExpr represents giving Range expression.
type ParenExpr struct {
	Commentaries
	Location

	Elem Expr
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p ParenExpr) Expr() string {
	return ""
}

// ID implements Expr.
func (p ParenExpr) ID() string {
	return "ParenExpr"
}

// Resolve implements Resolvable interface.
func (p *ParenExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// BinaryExpr represents giving Range expression.
type BinaryExpr struct {
	Commentaries
	Location

	Op    string
	Left  Expr
	Right Expr
}

// ID implements Expr.
func (p BinaryExpr) ID() string {
	return p.Op
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p BinaryExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *BinaryExpr) Resolve(indexed map[string]*Package) error {
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

// ID implements Expr.
func (p SymbolExpr) ID() string {
	return string(p.Symbol)
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p SymbolExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *SymbolExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// PropertyMethodGetExpr represents the calling of a giving property field from a parent.
type PropertyMethodGetExpr struct {
	Commentaries
	Location

	Method *Function
}

// ID implements Expr.
func (p PropertyMethodGetExpr) ID() string {
	return "PropertyMethodGet"
}

// Resolve implements Resolvable interface.
func (p *PropertyMethodGetExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// PropertyGetExpr represents the calling of a giving property field from a parent.
type PropertyGetExpr struct {
	Commentaries
	Location

	Property *Field
}

// ID implements Expr.
func (p PropertyGetExpr) ID() string {
	return "PropertyGet"
}

// Resolve implements Resolvable interface.
func (p *PropertyGetExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// SelectCaseExpr represents giving char expression like Bracket, + , -
// SelectCases used in code.
type SelectCaseExpr struct {
	Commentaries
	Location

	Comm Expr
	Body []Expr
}

// ID implements Expr.
func (p SelectCaseExpr) ID() string {
	return "SelectCaseExpr"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p SelectCaseExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *SelectCaseExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// SwitchCaseExpr represents giving char expression like Bracket, + , -
// SwitchCases used in code.
type SwitchCaseExpr struct {
	Commentaries
	Location

	Conditions []Expr
	Body       []Expr
}

// ID implements Expr.
func (p SwitchCaseExpr) ID() string {
	return "SwitchCaseExpr"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p SwitchCaseExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *SwitchCaseExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// SwitchExpr represents giving char expression like Bracket, + , -
// Switchs used in code.
type SwitchExpr struct {
	Commentaries
	Location

	Init Expr
	Body *GroupStmt
	Tag  Expr
}

// ID implements Expr.
func (p SwitchExpr) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p SwitchExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *SwitchExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// IfExpr represents giving char expression like Bracket, + , -
// Ifs used in code.
type IfExpr struct {
	Commentaries
	Location

	Body *GroupStmt
	Init Expr
	Else Expr
	Cond Expr
}

// ID implements Expr.
func (p IfExpr) ID() string {
	return "if"
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p IfExpr) Expr() string {
	return ""
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

// ID implements Expr.
func (p BadExpr) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p BadExpr) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *BadExpr) Resolve(indexed map[string]*Package) error {
	return nil
}

// VariableValuePair represents a variable-value pair declaration usually in a function body.
type VariableValuePair struct {
	Location

	// Key defines the key name used for giving key pair.
	Key Expr

	// Value represents type and value associated with key pair.
	Value Expr
}

// ID implements Expr.
func (p VariableValuePair) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p VariableValuePair) Expr() string {
	return ""
}

// Value represents a a declared variable without it's value.
type Value struct {
	Location
	Value Expr
}

// Expr returns string representation of giving type.
func (p Value) Expr() string {
	return ""
}

// ID implements Expr.
func (p Value) ID() string {
	return "Value"
}

// Resolve implements Resolvable interface.
func (p *Value) Resolve(indexed map[string]*Package) error {
	return nil
}

// Key represents a a declared variable without it's value.
type Key struct {
	Location

	Name string
	Type Expr
}

// Expr returns string representation of giving type.
func (p Key) Expr() string {
	return ""
}

// ID implements Expr.
func (p Key) ID() string {
	return "Key"
}

// Resolve implements Resolvable interface.
func (p *Key) Resolve(indexed map[string]*Package) error {
	return nil
}

// KeyValuePair represents a key-value pair declaration usually in
// a map.
type KeyValuePair struct {
	Location
	Commentaries

	// Key defines the key name used for giving key pair.
	Key Expr

	// Value represents type and value associated with key pair.
	Value Expr
}

// Expr returns string representation of giving type.
func (p KeyValuePair) Expr() string {
	return ""
}

// ID implements Expr.
func (p KeyValuePair) ID() string {
	return "KeyPair"
}

// Resolve implements Resolvable interface.
func (p *KeyValuePair) Resolve(indexed map[string]*Package) error {
	return nil
}

// DeclaredValue contains contents of declared type with
// associated value list.
type DeclaredValue struct {
	Commentaries
	Location

	// Fields holds all declared field and value of a declared expression.
	Values []Expr

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// ID implements Expr.
func (p DeclaredValue) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p DeclaredValue) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *DeclaredValue) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
	return p.resolver(indexed)
}

// DeclaredStructValue represent a giving declared struct where only values
// are declared in the order of it's field ordering. This is specifically
// for when a struct being initialized has no field name used.
type DeclaredStructValue struct {
	Commentaries
	Location

	// ValuesOnly sets a flag whether giving struct values
	// are set using field names and values or values only.
	// This will indicate to user that either DeclaredStructValue.Values
	// or DeclaredStructValue.Fields contains values for struct field.
	ValuesOnly bool

	// Values contains all values provided according to declaration
	// order when a struct field values are supplied without using
	// the field name.  {"Bob", "Juge"}
	Values []Expr

	// Fields contains all values and field names declared in the
	// format e.g {Name:"Bob", Addr:"Juge"}.
	Fields map[string]Expr

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// ID implements Expr.
func (p DeclaredStructValue) ID() string {
	return ""
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p DeclaredStructValue) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *DeclaredStructValue) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
	return p.resolver(indexed)
}

// DeclaredListValue contains associated location, commentary and value details
// of golang base types such as int, floats, strings, etc.
type DeclaredListValue struct {
	Commentaries
	Location

	// Length sets giving length of list.
	Length int64

	// Text contains the string version of base type.
	Text string

	// Fields holds all declared field and value of a declared expression.
	Values []Expr

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// ID implements Expr.
func (p DeclaredListValue) ID() string {
	return p.Text
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p DeclaredListValue) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *DeclaredListValue) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
	return p.resolver(indexed)
}

// DeclaredMapValue contains associated location, commentary and value details
// of golang base types such as int, floats, strings, etc.
type DeclaredMapValue struct {
	Commentaries
	Location

	// Text contains the string version of base type.
	Text string

	// Fields holds all declared field and value of a declared expression.
	KeyValues []KeyValuePair

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// ID implements Expr.
func (p DeclaredMapValue) ID() string {
	return p.Text
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p DeclaredMapValue) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *DeclaredMapValue) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
	return p.resolver(indexed)
}

// BaseValue contains associated location, commentary and value details
// of golang base types such as int, floats, strings, etc.
type BaseValue struct {
	Commentaries
	Location

	// Value contains associated value of Base type.
	Value string

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// ID implements Expr.
func (p BaseValue) ID() string {
	return p.Value
}

// Expr returns rendered string representation of giving type.
// It implements the Expr interface.
func (p BaseValue) Expr() string {
	return ""
}

// Resolve implements Resolvable interface.
func (p *BaseValue) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
	return p.resolver(indexed)
}

// Map embodies a giving map type with
// an associated name, value and key type.
type Map struct {
	Location
	Commentaries

	// IsPointer indicates if giving variable type is a pointer.
	IsPointer bool

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// KeyType sets the key type for giving map type.
	KeyType Expr

	// ValueType sets the value type for giving map type.
	ValueType Expr

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// ID implements Expr interface.
func (p Map) ID() string {
	return "Map"
}

// Expr returns string representation of giving type.
func (p Map) Expr() string {
	return ""
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Map) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
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

	// Length defines giving length associated with slice or array.
	Length int64

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Type sets the value object/declared type.
	Type Expr

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// ID implements Expr interface.
func (p List) ID() string {
	return "List"
}

// Expr returns string representation of giving type.
func (p List) Expr() string {
	return ""
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *List) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// Channel embodies a channel type declared
// with a golang package.
type Channel struct {
	Commentaries
	Location

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Type sets the value object/declared type.
	Type Expr

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// ID implements Expr interface.
func (p Channel) ID() string {
	return "Channel"
}

// Expr returns string representation of giving type.
func (p Channel) Expr() string {
	return ""
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Channel) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
	if p.resolver != nil {
		if err := p.resolver(indexed); err != nil {
			return err
		}
	}
	return nil
}

// OpOf represents a golang type which is being applied
// an operation using a operator prefix, which means to get pointer of type.
type OpOf struct {
	Location
	Commentaries

	// Op represent the giving Op being applied to giving entitiy.
	Op string

	// OpFlag represents the value used by token.Token type as a int.
	OpFlag int

	// Elem contains associated type the pointer represents.
	Elem Expr
}

// Expr returns string representation of giving type.
func (p OpOf) Expr() string {
	return ""
}

// ID implements the Expr interface.
func (p OpOf) ID() string {
	return p.Op + p.Elem.ID()
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *OpOf) Resolve(indexed map[string]*Package) error {
	return nil
}

// AddressOf represents a golang type which is being assigned
// using the & prefix, which means to get pointer of type.
type AddressOf struct {
	Location
	Commentaries

	// Name represents the name of giving type.
	Name string

	// Elem contains associated type the pointer represents.
	Elem Expr
}

// Expr returns string representation of giving type.
func (p AddressOf) Expr() string {
	return ""
}

// SetID sets n to Name field value.
func (p *AddressOf) SetID(n string) {
	p.Name = n
}

// ID implements the Expr interface.
func (p AddressOf) ID() string {
	return "AddressOf"
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *AddressOf) Resolve(indexed map[string]*Package) error {
	return nil
}

// Pointer represents a golang pointer types which include
// strings, int types, floats, complex, etc, which are
// atomic indivisible types.
type Pointer struct {
	Location
	Commentaries

	// Name represents the name of giving type.
	Name string

	// Elem contains associated type the pointer represents.
	Elem Expr
}

// Expr returns string representation of giving type.
func (p Pointer) Expr() string {
	return ""
}

// SetID sets n to Name field value.
func (p *Pointer) SetID(n string) {
	p.Name = n
}

// ID implements the Expr interface.
func (p Pointer) ID() string {
	return p.Elem.ID()
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Pointer) Resolve(indexed map[string]*Package) error {
	return nil
}

// Base represents a golang base types which include
// strings, int types, floats, complex, etc, which are
// atomic indivisible types.
type Base struct {
	Location
	Commentaries

	// Name represents the name of giving type.
	Name string
}

// BaseFor returns a new instance of Base using provided Name.
func BaseFor(baseName string) Base {
	return Base{
		Name: baseName,
	}
}

// SetID sets n to Name field value.
func (p *Base) SetID(n string) {
	p.Name = n
}

// Expr returns string representation of giving type.
func (p Base) Expr() string {
	return ""
}

// ID implements the Expr interface.
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
	Type Expr

	// Value defines giving value of variable.
	Value Expr

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
	resolved bool
}

// SetID sets n to Name field value.
func (p *Variable) SetID(n string) {
	p.Name = n
}

// Expr returns string representation of giving type.
func (p Variable) Expr() string {
	return ""
}

// ID implements Expr interface.
func (p Variable) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they can index. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Variable) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
	if p.resolver == nil {
		return nil
	}

	return p.resolver(indexed)
}

// Field represents field types and names
// declared as part of a types's properties.
type Field struct {
	Pathway
	Location
	Commentaries

	// Annotations holds a list of all annotations associated with giving
	// function parsed from comments.
	Annotations []Annotation

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
	Type Expr

	// Import contains import details for giving field type used in Pathwayerence
	// within declaration of struct.
	Import *Import

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// SetID sets n to Name field value.
func (p *Field) SetID(n string) {
	p.Name = n
}

// Expr returns string representation of giving type.
func (p Field) Expr() string {
	return ""
}

// ID implements Expr interface.
func (p Field) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Field) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
	if p.resolver == nil {
		return nil
	}

	return p.resolver(indexed)
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
	Type Expr

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
	resolved bool
}

// SetID sets n to Name field value.
func (p *Parameter) SetID(n string) {
	p.Name = n
}

// ID implements Expr interface.
func (p Parameter) ID() string {
	return p.Name
}

// Expr returns string representation of giving type.
func (p Parameter) Expr() string {
	return ""
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Parameter) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true

	if p.resolver == nil {
		return nil
	}

	return p.resolver(indexed)
}

// Type defines a struct holding information about
// a defined custom type based on an existing type.
type Type struct {
	Pathway
	Location
	Commentaries

	// Annotations holds a list of all annotations associated with giving
	// function parsed from comments.
	Annotations []Annotation

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
	Points Expr

	// Methods contains all function defined as methods attached to
	// type instance.
	Methods map[string]*Function

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// SetID sets n to Name field value.
func (p *Type) SetID(n string) {
	p.Name = n
}

// ID implements Expr interface.
func (p Type) ID() string {
	return p.Name
}

// Expr returns string representation of giving type.
func (p Type) Expr() string {
	return ""
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Type) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
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

// Interface embodies necessary data related to declared
// interface types within a package.
type Interface struct {
	Pathway
	Location
	Commentaries

	// Annotations holds a list of all annotations associated with giving
	// function parsed from comments.
	Annotations []Annotation

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
	Methods map[string]*Function

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// Expr returns string representation of giving type.
func (p Interface) Expr() string {
	return ""
}

// SetID sets n to Name field value.
func (p *Interface) SetID(n string) {
	p.Name = n
}

// ID implements Expr interface.
func (p Interface) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Interface) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true
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

	// Annotations holds a list of all annotations associated with giving
	// function parsed from comments.
	Annotations []Annotation

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// Composes contains all interface types composed by
	// given struct type.
	Composes []*Field

	// Embeds contains all struct types composed by
	// given struct type.
	Embeds map[string]*Struct

	// Fields contains all fields and associated types declared
	// as members of struct.
	Fields map[string]*Field

	// Methods contains all function defined as methods attached to
	// struct instance.
	Methods map[string]*Function

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// SetID sets n to Name field value.
func (p *Struct) SetID(n string) {
	p.Name = n
}

// ID implements Expr interface.
func (p Struct) ID() string {
	return p.Name
}

// Expr returns string representation of giving type.
func (p Struct) Expr() string {
	return ""
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Struct) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	// set resolution to true.
	p.resolved = true

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

	for _, field := range p.Composes {
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

	// Annotations holds a list of all annotations associated with giving
	// function parsed from comments.
	Annotations []Annotation

	// Body is a GroupStmt which embodies all
	// contents of giving function body if it has
	// one.
	Body *GroupStmt

	// ScopeName sets the giving random name or function name attached
	// to giving function during parsing of it's body. It's used
	// to manage overall scoping of values, variables called within.
	ScopeName string

	// ReceiverName represents the instance name giving to the
	// function receiver ie. (instanceName Type) func.
	ReceiverName string

	// ReceiverAddr represents the combination of the Pathway.Path and
	// ReceiverName of giving function.
	ReceiverAddr string

	// Name represents the name of giving interface.
	Name string

	// Exported is used to indicate if type is exported or not.
	Exported bool

	// IsMethod indicates if giving function is a method of a struct or a method
	// contract for an interface.
	IsMethod bool

	// IsPointerMethod returns true/false if giving receiver is a pointer type.
	IsPointerMethod bool

	// IsVariadic indicates if the last argument is variadic.
	IsVariadic bool

	// Owner sets the struct or interface which this function is attached
	// to has a method.
	Owner Expr

	// Arguments provides the argument list for giving function.
	Arguments []*Parameter

	// Arguments provides the argument list for giving function.
	Returns []*Parameter

	// Meta provides associated package  and commentary information related to
	// giving type.
	Meta Meta

	// Scope defines giving set of elements parsed out during processing of
	// underline function body. It contains links to all referenced, declared
	// types.
	Scope map[string]Expr

	// resolver provides a means of the indexer to provide a custom resolving
	// function which will run internal logic to set giving values
	// appropriately during resolution of types.
	resolver ResolverFn
	resolved bool
}

// SetID sets n to Name field value.
func (p *Function) SetID(n string) {
	p.Name = n
}

// Expr returns string representation of giving type.
func (p Function) Expr() string {
	return ""
}

// ID implements Expr interface.
func (p Function) ID() string {
	return p.Name
}

// Resolve takes the list of indexed packages to internal structures
// to resolve imported or internal types that they Pathwayerence. This is
// used to ensure all package structures have direct link to parsed
// type.
func (p *Function) Resolve(indexed map[string]*Package) error {
	// if we were previously resolved, then skip.
	if p.resolved {
		return nil
	}

	p.resolved = true
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

	if p.Body != nil {
		return p.Body.Resolve(indexed)
	}

	return nil
}

// FunctionScope embodies all declared types found within a function body.
type FunctionScope struct {
	Name  string
	Path  string
	Scope map[string]Expr
}
