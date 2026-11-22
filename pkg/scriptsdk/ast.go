package scriptsdk

import (
	"reflect"

	"github.com/dop251/goja/ast"
)

// walkAST visits a node and its AST children. The goja AST has no general
// visitor, so reflection handles new expression and statement shapes without a
// second exhaustive type switch. Non-AST fields, including program file metadata,
// are not traversed.
func walkAST(root ast.Node, visit func(ast.Node)) {
	var walk func(reflect.Value)
	walk = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		switch value.Kind() {
		case reflect.Interface:
			if value.IsNil() {
				return
			}
			walk(value.Elem())
		case reflect.Pointer:
			if value.IsNil() || !value.CanInterface() {
				return
			}
			node, ok := value.Interface().(ast.Node)
			if !ok {
				return
			}
			visit(node)
			walk(value.Elem())
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				field := value.Field(i)
				if field.CanInterface() {
					walk(field)
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				walk(value.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(root))
}
