package sets

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strconv"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/token"
)

func lookUp(node ast.Node, paths ...string) (ast.Node, error) {
	if len(paths) == 0 {
		return node, nil
	}
	key := paths[0]
	switch x := node.(type) {
	case *ast.File:
		for _, decl := range x.Decls {
			nnode := lookField(decl, key)
			if nnode != nil {
				return lookUp(nnode, paths[1:]...)
			}
		}
	case *ast.ListLit:
		for index, elt := range x.Elts {
			if strconv.Itoa(index) == key {
				return lookUp(elt, paths[1:]...)
			}
		}
	case *ast.StructLit:
		for _, elt := range x.Elts {
			nnode := lookField(elt, key)
			if nnode != nil {
				return lookUp(nnode, paths[1:]...)
			}
		}
	}
	return nil, notFoundErr
}

func lookField(node ast.Node, key string) ast.Node {
	if field, ok := node.(*ast.Field); ok {
		if labelStr(field.Label) == key {
			return field.Value
		}
	}
	return nil
}

func labelStr(label ast.Label) string {
	if ident, ok := label.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func print(v cue.Value) (string, error) {
	v = v.Eval()
	syopts := []cue.Option{cue.All(), cue.DisallowCycles(true), cue.ResolveReferences(true), cue.Docs(true)}

	var w bytes.Buffer
	useSep := false
	format := func(name string, n ast.Node) error {
		if name != "" {
			// TODO: make this relative to DIR
			fmt.Fprintf(&w, "// %s\n", filepath.Base(name))
		} else if useSep {
			fmt.Println("// ---")
		}
		useSep = true

		b, err := format.Node(toFile(n))
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}

	if err := format("", v.Syntax(syopts...)); err != nil {
		return "", err
	}
	instStr := w.String()
	return instStr, nil
}

func toFile(n ast.Node) *ast.File {
	switch x := n.(type) {
	case nil:
		return nil
	case *ast.StructLit:
		return &ast.File{Decls: x.Elts}
	case ast.Expr:
		ast.SetRelPos(x, token.NoSpace)
		return &ast.File{Decls: []ast.Decl{&ast.EmbedDecl{Expr: x}}}
	case *ast.File:
		return x
	default:
		panic(fmt.Sprintf("Unsupported node type %T", x))
	}
}

func convert2Node(value cue.Value) ast.Node {
	syopts := []cue.Option{cue.All(), cue.DisallowCycles(true), cue.ResolveReferences(true), cue.Docs(true)}
	return value.Syntax(syopts...)
}
