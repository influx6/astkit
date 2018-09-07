package compiler

import (
	"fmt"
	"go/ast"
	"regexp"
	"strings"

	"github.com/gokit/errors"
)

//******************************************************************************
//  Utilities
//******************************************************************************

var (
	varSignatureRegExp = regexp.MustCompile(`var\s([a-zA-Z0-9_]+)\s([a-zA-Z0-9_\.\/\$]+)`)
)

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

// GetTags returns all associated tag within provided string.
func GetTags(content string) []Tag {
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

func getExprName(n interface{}) (string, error) {
	switch t := n.(type) {
	case *ast.Ident:
		return t.Name, nil
	case *ast.BasicLit:
		return t.Value, nil
	case *ast.SelectorExpr:
		return getExprName(t.X)
	default:
		return "", Error("unable to get name")
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
