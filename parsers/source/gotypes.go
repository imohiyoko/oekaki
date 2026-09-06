package source

import (
	"go/ast"
	"go/token"
	"path/filepath"

	"github.com/imohiyoko/oekaki/core"
)

// goTypes reads the type declarations of one Go file.
//
// Go gets a real parser here where the other languages get regular
// expressions, and the difference shows in what can be said: a struct's fields
// are known by name and by type, embedding is distinguishable from an ordinary
// field, and neither is a guess. Everything this records is something the
// declaration says.
func goTypes(g *core.Graph, scan *typeScan, f *ast.File, fset *token.FileSet, fileID, rel string) {
	dir := filepath.ToSlash(filepath.Dir(rel))

	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			id := declareType(g, fileID, ts.Name.Name, goTypeKind(ts), "go", rel,
				fset.Position(ts.Pos()).Line)

			switch t := ts.Type.(type) {
			case *ast.StructType:
				goFields(scan, id, dir, t.Fields)
			case *ast.InterfaceType:
				// An interface's methods are its own; what an interface
				// *embeds* is another interface, and that is the relation
				// worth drawing.
				for _, field := range t.Methods.List {
					if len(field.Names) == 0 {
						if name := goTypeName(field.Type); name != "" {
							scan.relations = append(scan.relations, pendingRelation{
								from: id, relation: RelationEmbeds, target: name, dir: dir,
							})
						}
					}
				}
			}
		}
	}
}

// goFields records what a struct holds.
//
// A field with no name of its own is embedding, which is Go's way of saying
// "this is one of those" and the closest thing the language has to a base
// class. A named field whose type is another type here is composition, and
// both are relations a class diagram is made of.
func goFields(scan *typeScan, id, dir string, fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		name := goTypeName(field.Type)
		if name == "" {
			continue
		}
		if len(field.Names) == 0 {
			scan.relations = append(scan.relations, pendingRelation{
				from: id, relation: RelationEmbeds, target: name, dir: dir,
			})
			continue
		}
		for _, n := range field.Names {
			scan.relations = append(scan.relations, pendingRelation{
				from: id, relation: RelationHasField, target: name, dir: dir, field: n.Name,
			})
		}
	}
}

// goTypeKind is what the declaration made.
//
// The distinction earns its place: a class diagram draws an interface
// differently from a struct, and "type X = Y" is not a new thing at all.
func goTypeKind(ts *ast.TypeSpec) string {
	if ts.Assign.IsValid() {
		return "alias"
	}
	switch ts.Type.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	case *ast.FuncType:
		return "func"
	}
	return "defined"
}

// goTypeName is the type a field or an embedded element names, with the
// pointers, slices, arrays and type arguments taken off.
//
// A []*Order and an Order are the same type as far as "this holds one of
// those" goes; how many of them there are is a property of the field, and the
// field is not what is being drawn. A map is deliberately not followed: its
// key and its value are two types, and picking one would say something the
// declaration does not.
func goTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return goTypeName(t.X)
	case *ast.ArrayType:
		return goTypeName(t.Elt)
	case *ast.IndexExpr:
		return goTypeName(t.X)
	case *ast.IndexListExpr:
		return goTypeName(t.X)
	case *ast.SelectorExpr:
		// A type from another package. The name is kept qualified so the
		// resolution can try the last part and find nothing when the package
		// is one this parser never read — which is the common case, and the
		// right outcome.
		if pkg, ok := t.X.(*ast.Ident); ok && t.Sel != nil {
			return pkg.Name + "." + t.Sel.Name
		}
	}
	return ""
}
