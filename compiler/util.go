package compiler

import (
	"fmt"
	"go/ast"
	"math/rand"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gokit/errors"
)

//******************************************************************************
//  Utilities
//******************************************************************************

var (
	varSignatureRegExp = regexp.MustCompile(`var\s([a-zA-Z0-9_]+)\s([a-zA-Z0-9_\.\/\$]+)`)
	alphanums          = []rune("bcdfghjklmnpqrstvwxz0123456789")
)

// String generates a random alphanumeric string, without vowels, which is n
// characters long.  This will panic if n is less than zero.
func String(length int) string {
	b := make([]rune, length)
	for i := range b {
		nr := rand.Intn(len(alphanums))
		b[i] = alphanums[nr]
	}
	return string(b)
}

type varSignature struct {
	Name    string
	Package string
}

func getVarSignature(m string) (varSignature, error) {
	var sig varSignature
	parts := varSignatureRegExp.FindAllString(m, -1)
	if len(parts) < 3 {
		return sig, errors.New("no signature or invalid signature found: %q", m)
	}
	sig.Name = parts[1]
	sig.Package = parts[2]
	return sig, nil
}

var (
	moreSpaces = regexp.MustCompile(`\s+`)
	itag       = regexp.MustCompile(`(\w+):"(\w+|[\w,?\s+\w]+)"`)
	annotation = regexp.MustCompile("@(\\w+(:\\w+)?)(\\([.\\s\\S]+\\))?")
)

// getTags returns all associated tag within provided string.
func getTags(content string) []Tag {
	var tags []Tag
	cleaned := moreSpaces.ReplaceAllString(content, " ")
	for _, tag := range strings.Split(cleaned, " ") {
		if !itag.MatchString(tag) {
			continue
		}

		res := itag.FindStringSubmatch(tag)
		resValue := strings.Split(res[2], ",")

		tags = append(tags, Tag{
			Text:  res[0],
			Name:  res[1],
			Value: resValue[0],
			Meta:  resValue[1:],
		})
	}
	return tags
}

const (
	goosList   = "android darwin dragonfly freebsd js linux nacl netbsd openbsd plan9 solaris windows zos"
	goarchList = "386 amd64 amd64p32 arm armbe arm64 arm64be ppc64 ppc64le mips mipsle mips64 mips64le mips64p32 mips64p32le ppc riscv riscv64 s390 s390x sparc sparc64 wasm"
)

var (
	archLists = []string{
		"386",
		"amd64",
		"amd64p32",
		"arm",
		"armbe",
		"arm64",
		"arm64be",
		"ppc",
		"ppc64",
		"ppc64le",
		"mips",
		"mipsle",
		"mips64",
		"mipsx",
		"mipsx64",
		"mips64le",
		"mips64p32",
		"mips64p32le",
		"riscv",
		"riscv64",
		"s390",
		"s390x",
		"sparc",
		"sparc64",
		"wasm",
	}

	basicArchLists = []string{
		"386",
		"amd64",
		"arm",
		"arm64",
		"wasm",
	}

	nativelySupportedArchLists = []string{
		"386",
		"amd64",
		"amd64p32",
		"arm",
		"arm64",
		"ppc64",
		"ppc64le",
		"mips",
		"mipsle",
		"mips64",
		"mips64le",
		"s390x",
		"wasm",
	}

	archs = map[string]bool{
		"386":         true,
		"amd64":       true,
		"amd64p32":    true,
		"arm":         true,
		"armbe":       true,
		"arm64":       true,
		"arm64be":     true,
		"ppc":         true,
		"ppc64":       true,
		"ppc64le":     true,
		"mips":        true,
		"mipsle":      true,
		"mips64":      true,
		"mipsx":       true,
		"mipsx64":     true,
		"mips64le":    true,
		"mips64p32":   true,
		"mips64p32le": true,
		"riscv":       true,
		"riscv64":     true,
		"s390":        true,
		"s390x":       true,
		"sparc":       true,
		"sparc64":     true,
		"wasm":        true,
	}

	basicPlatformLists = []string{
		"linux",
		"darwin",
		"windows",
	}

	platformLists = []string{
		"zos",
		"nacl",
		"linux",
		"plan9",
		"netbsd",
		"darwin",
		"openbsd",
		"solaris",
		"windows",
		"android",
		"freebsd",
		"dragonfly",
	}

	nativelySupportedPlatformLists = []string{
		"nacl",
		"linux",
		"plan9",
		"netbsd",
		"darwin",
		"openbsd",
		"solaris",
		"windows",
		"freebsd",
		"dragonfly",
	}

	platforms = map[string]bool{
		"js":        true,
		"zos":       true,
		"nacl":      true,
		"linux":     true,
		"plan9":     true,
		"netbsd":    true,
		"darwin":    true,
		"openbsd":   true,
		"solaris":   true,
		"windows":   true,
		"android":   true,
		"freebsd":   true,
		"dragonfly": true,
	}
)

func getPlatformArchitecture(v string) (platform string, arch string) {
	v = strings.Replace(v, filepath.Ext(v), "", -1)

	parts := strings.Split(v, "_")
	total := len(parts)

	if total == 0 {
		return
	}

	if total == 1 {
		return
	}

	var area []string

	if total > 3 {
		area = parts[total-3:]
	}

	if total == 3 {
		area = parts[total-2:]
	}

	if total == 2 {
		area = parts[total-1:]
	}

	if len(area) > 1 {
		if platforms[area[0]] {
			platform = area[0]
		}

		if archs[area[1]] {
			arch = area[1]
		}

		return
	}

	if platforms[area[0]] {
		platform = area[0]
		return
	}

	if archs[area[0]] {
		arch = area[0]
		return
	}

	return
}

func getExprName(n interface{}) (string, error) {
	switch t := n.(type) {
	case *ast.Ident:
		return t.Name, nil
	case *ast.BasicLit:
		return t.Value, nil
	case *ast.SelectorExpr:
		return getExprName(t.X)
	default:
		return "", errors.New("unable to get name")
	}
}

// GetExprCaller gets the expression caller
// e.g. x.Cool() => x
func GetExprCaller(n ast.Node) (ast.Expr, error) {
	switch t := n.(type) {
	case *ast.SelectorExpr:
		return t.X, nil
	case *ast.Ident:
		return nil, nil
	default:
		return nil, fmt.Errorf("util/GetExprCaller: unhandled %T", t)
	}
}

// GetIdentifier gets rightmost identifier if there is one
func GetIdentifier(n ast.Node) (*ast.Ident, error) {
	switch t := n.(type) {
	case *ast.Ident:
		return t, nil
	case *ast.StarExpr:
		return GetIdentifier(t.X)
	case *ast.UnaryExpr:
		return GetIdentifier(t.X)
	case *ast.SelectorExpr:
		return GetIdentifier(t.Sel)
	case *ast.IndexExpr:
		return GetIdentifier(t.X)
	case *ast.CallExpr:
		return GetIdentifier(t.Fun)
	case *ast.CompositeLit:
		return GetIdentifier(t.Type)
	case *ast.FuncDecl:
		return t.Name, nil
	case *ast.ParenExpr:
		return GetIdentifier(t.X)
	case *ast.ArrayType, *ast.MapType, *ast.StructType,
		*ast.ChanType, *ast.FuncType, *ast.InterfaceType,
		*ast.FuncLit, *ast.BinaryExpr:
		return nil, nil
	case *ast.SliceExpr:
		return GetIdentifier(t.X)
	default:
		return nil, fmt.Errorf("GetIdentifier: unhandled %T", n)
	}
}

// ExprToString fn
func ExprToString(n ast.Node) (string, error) {
	switch t := n.(type) {
	case *ast.BasicLit:
		return t.Value, nil
	case *ast.Ident:
		return t.Name, nil
	case *ast.StarExpr:
		return ExprToString(t.X)
	case *ast.UnaryExpr:
		return ExprToString(t.X)
	case *ast.SelectorExpr:
		s, e := ExprToString(t.X)
		if e != nil {
			return "", e
		}
		return s + "." + t.Sel.Name, nil
	case *ast.IndexExpr:
		s, e := ExprToString(t.X)
		if e != nil {
			return "", e
		}
		i, e := ExprToString(t.Index)
		if e != nil {
			return "", e
		}
		return s + "[" + i + "]", nil
	case *ast.CallExpr:
		c, e := ExprToString(t.Fun)
		if e != nil {
			return "", e
		}
		var args []string
		for _, arg := range t.Args {
			a, e := ExprToString(arg)
			if e != nil {
				return "", e
			}
			args = append(args, a)
		}
		return c + "(" + strings.Join(args, ", ") + ")", nil
	case *ast.CompositeLit:
		c, e := ExprToString(t.Type)
		if e != nil {
			return "", e
		}
		var args []string
		for _, arg := range t.Elts {
			a, e := ExprToString(arg)
			if e != nil {
				return "", e
			}
			args = append(args, a)
		}
		return c + "{" + strings.Join(args, ", ") + "}", nil
	case *ast.ArrayType:
		c, e := ExprToString(t.Elt)
		if e != nil {
			return "", e
		}
		return `[]` + c, nil
	case *ast.FuncLit:
		return "func(){}()", nil
	case *ast.ParenExpr:
		x, e := ExprToString(t.X)
		if e != nil {
			return "", e
		}
		return "(" + x + ")", nil
	case *ast.KeyValueExpr:
		k, e := ExprToString(t.Key)
		if e != nil {
			return "", e
		}
		v, e := ExprToString(t.Value)
		if e != nil {
			return "", e
		}
		return "{" + k + ":" + v + "}", nil
	case *ast.MapType:
		k, e := ExprToString(t.Key)
		if e != nil {
			return "", e
		}
		v, e := ExprToString(t.Value)
		if e != nil {
			return "", e
		}
		return "{" + k + ":" + v + "}", nil
	case *ast.BinaryExpr:
		x, e := ExprToString(t.X)
		if e != nil {
			return "", e
		}
		y, e := ExprToString(t.Y)
		if e != nil {
			return "", e
		}
		return x + " " + t.Op.String() + " " + y, nil
	case *ast.InterfaceType:
		return "interface{}", nil
	default:
		return "", fmt.Errorf("util/ExprToString: unhandled %T", n)
	}
}
