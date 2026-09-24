// Command bindgen generates Go and TypeScript bindings from a Go API type.
package main

import (
	"bytes"
	"crypto/md5"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type methodSpec struct {
	GoName     string
	JSName     string
	HasContext bool
	Params     []paramSpec
	Result     string
	ReturnKind returnKind
	ResultGo   string
	RecvPtr    bool
	CallMode   callMode
}

type callMode int

const (
	callModeNotify callMode = iota
	callModeRequest
)

type returnKind int

const (
	returnKindVoidNoError returnKind = iota
	returnKindVoidError
	returnKindValueOnly
	returnKindValueAndError
)

type paramSpec struct {
	Name   string
	GoType string
	TSType string
}

type tsExprKind int

const (
	tsExprString tsExprKind = iota
	tsExprNumber
	tsExprBoolean
	tsExprUnknown
	tsExprArray
	tsExprMap
	tsExprNamedRef
)

type tsExpr struct {
	Kind  tsExprKind
	Name  string
	Elem  *tsExpr
	Key   *tsExpr
	Value *tsExpr
}

type tsField struct {
	Name     string
	Optional bool
	Type     *tsExpr
}

type tsDeclKind int

const (
	tsDeclInterface tsDeclKind = iota
	tsDeclAlias
)

type tsDecl struct {
	Name   string
	Kind   tsDeclKind
	Fields []tsField
	Target *tsExpr
}

type parserState struct {
	fset                   *token.FileSet
	pkgName                string
	files                  map[string]*ast.File
	typeName               string
	ffiTypeName            string
	requestBindingName     string
	deliverySlotName       string
	targetSourceFile       string
	goBindingType          string
	tsClassName            string
	typeSpecs              map[string]*ast.TypeSpec
	methods                []methodSpec
	tsDecls                map[string]*tsDecl
	tsDeclSeq              []string
	resolvingTypes         map[string]bool
	importPathsByAlias     map[string]string
	goCallContextQualifier string
	ffiNeedsPointer        bool
	externalTypeCache      map[string]map[string]*ast.TypeSpec
}

func main() {
	var apiType string
	var dtoType string
	var dtoTSName string
	var goOut string
	var tsOut string
	var goBindingType string
	var tsClassName string
	var ffiType string
	flag.StringVar(&apiType, "type", "", "API type name")
	flag.StringVar(&dtoType, "dto", "", "DTO type name for TS declaration generation")
	flag.StringVar(&dtoTSName, "name", "", "override TS type name for -dto (defaults to Go type name)")
	flag.StringVar(&goOut, "go", "", "generated Go file")
	flag.StringVar(&tsOut, "ts", "", "generated TS file")
	flag.StringVar(&goBindingType, "go-binding", "GeneratedGoCallBinding", "generated Go binding type name")
	flag.StringVar(&tsClassName, "ts-class", "GeneratedGoCall", "generated TS class name")
	flag.StringVar(&ffiType, "ffi-type", "", "runtime FFI struct type name; defaults to -type")
	flag.Parse()

	if dtoType != "" {
		if tsOut == "" {
			fail("-ts is required with -dto")
		}
		if dtoTSName == "" {
			dtoTSName = dtoType
		}
		runDTO(dtoType, dtoTSName, tsOut)
		return
	}

	if apiType == "" || goOut == "" || tsOut == "" {
		fail("missing required flags")
	}
	if !token.IsIdentifier(goBindingType) {
		fail("invalid -go-binding identifier")
	}
	if !token.IsIdentifier(tsClassName) {
		fail("invalid -ts-class identifier")
	}
	if ffiType != "" && !token.IsIdentifier(ffiType) {
		fail("invalid -ffi-type identifier")
	}
	if ffiType == "" {
		ffiType = apiType
	}

	wd, err := os.Getwd()
	if err != nil {
		fail(err.Error())
	}
	state, err := loadPackage(wd, apiType, ffiType)
	if err != nil {
		fail(err.Error())
	}
	stableID, err := stableBindingID(state.targetSourceFile)
	if err != nil {
		fail(err.Error())
	}
	state.requestBindingName = "__cdp_ffi_binding_" + stableID
	state.deliverySlotName = "__cdp_ffi_slot_" + stableID
	state.goBindingType = goBindingType
	state.tsClassName = tsClassName

	if err := os.WriteFile(filepath.Join(wd, goOut), state.renderGo(), 0o644); err != nil {
		fail(err.Error())
	}
	if err := os.WriteFile(filepath.Join(wd, tsOut), state.renderTS(), 0o644); err != nil {
		fail(err.Error())
	}
}

func runDTO(dtoType, tsName, tsOut string) {
	wd, err := os.Getwd()
	if err != nil {
		fail(err.Error())
	}
	state, err := newParserState(wd, dtoType, "")
	if err != nil {
		fail(err.Error())
	}
	state.tsClassName = tsName
	spec, ok := state.typeSpecs[dtoType]
	if !ok {
		fail(fmt.Sprintf("type %s not found", dtoType))
	}
	if err := state.ensureTSDecl(dtoType, spec); err != nil {
		fail(err.Error())
	}
	if err := os.WriteFile(filepath.Join(wd, tsOut), state.renderDTO(), 0o644); err != nil {
		fail(err.Error())
	}
}

func loadPackage(dir, apiType, ffiType string) (*parserState, error) {
	state, err := newParserState(dir, apiType, ffiType)
	if err != nil {
		return nil, err
	}

	target, ok := state.typeSpecs[apiType]
	if !ok {
		return nil, fmt.Errorf("type %s not found", apiType)
	}
	if _, ok := target.Type.(*ast.StructType); !ok {
		return nil, fmt.Errorf("type %s must be a struct", apiType)
	}
	state.targetSourceFile = state.fset.Position(target.Pos()).Filename
	if state.targetSourceFile == "" {
		return nil, fmt.Errorf("target source file for type %s not found", apiType)
	}

	ffiTarget, ok := state.typeSpecs[ffiType]
	if !ok {
		return nil, fmt.Errorf("ffi type %s not found", ffiType)
	}
	if _, ok := ffiTarget.Type.(*ast.StructType); !ok {
		return nil, fmt.Errorf("ffi type %s must be a struct", ffiType)
	}

	state.methods, err = state.collectMethods(apiType)
	if err != nil {
		return nil, err
	}
	if len(state.methods) == 0 {
		return nil, fmt.Errorf("no methods found for %s", apiType)
	}
	sort.Slice(state.methods, func(i, j int) bool {
		return state.methods[i].JSName < state.methods[j].JSName
	})
	if err := state.validateFFIType(); err != nil {
		return nil, err
	}
	return state, nil
}

func newParserState(dir, typeName, ffiTypeName string) (*parserState, error) {
	pkgName, files, fset, err := parsePackageFiles(dir, func(name string) bool {
		return !strings.HasSuffix(name, "_gen.go")
	})
	if err != nil {
		return nil, err
	}

	state := &parserState{
		fset:               fset,
		pkgName:            pkgName,
		files:              files,
		typeName:           typeName,
		ffiTypeName:        ffiTypeName,
		typeSpecs:          make(map[string]*ast.TypeSpec),
		tsDecls:            make(map[string]*tsDecl),
		importPathsByAlias: make(map[string]string),
		resolvingTypes:     make(map[string]bool),
		externalTypeCache:  make(map[string]map[string]*ast.TypeSpec),
	}

	for _, file := range files {
		for _, imp := range file.Imports {
			pathValue, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("invalid import path %s", imp.Path.Value)
			}
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			} else {
				parts := strings.Split(pathValue, "/")
				alias = parts[len(parts)-1]
			}
			if alias != "_" && alias != "." {
				state.importPathsByAlias[alias] = pathValue
			}
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if ok {
					state.typeSpecs[typeSpec.Name.Name] = typeSpec
				}
			}
		}
	}

	return state, nil
}

func parsePackageFiles(dir string, include func(string) bool) (string, map[string]*ast.File, *token.FileSet, error) {
	pkg, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		return "", nil, nil, err
	}
	names := make([]string, 0, len(pkg.GoFiles)+len(pkg.CgoFiles))
	names = append(names, pkg.GoFiles...)
	names = append(names, pkg.CgoFiles...)
	sort.Strings(names)
	fset := token.NewFileSet()
	files := make(map[string]*ast.File, len(names))
	for _, name := range names {
		if include != nil && !include(name) {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return "", nil, nil, err
		}
		files[path] = file
	}
	if len(files) == 0 {
		return "", nil, nil, fmt.Errorf("no Go package found in %s", dir)
	}
	return pkg.Name, files, fset, nil
}

func (p *parserState) collectMethods(typeName string) ([]methodSpec, error) {
	methods := make([]methodSpec, 0)
	for _, file := range p.files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			if receiverTypeName(fn.Recv.List[0].Type) != typeName || !hasGoCallDirective(fn.Doc) {
				continue
			}
			spec, err := p.parseMethodDecl(fn)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fn.Name.Name, err)
			}
			methods = append(methods, spec)
		}
	}
	return methods, nil
}

func (p *parserState) validateFFIType() error {
	if p.ffiTypeName == p.typeName {
		for _, method := range p.methods {
			if method.RecvPtr {
				p.ffiNeedsPointer = true
				break
			}
		}
		return nil
	}

	collected, err := p.collectMethods(p.ffiTypeName)
	if err != nil {
		return fmt.Errorf("ffi type %s: %w", p.ffiTypeName, err)
	}
	ffiMethods := make(map[string]methodSpec, len(collected))
	for _, spec := range collected {
		ffiMethods[spec.GoName] = spec
	}

	for _, method := range p.methods {
		ffiMethod, ok := ffiMethods[method.GoName]
		if !ok {
			return fmt.Errorf("ffi type %s must implement method %s", p.ffiTypeName, method.GoName)
		}
		if !sameMethodSignature(method, ffiMethod) {
			return fmt.Errorf("ffi type %s method %s signature mismatch", p.ffiTypeName, method.GoName)
		}
		if ffiMethod.RecvPtr {
			p.ffiNeedsPointer = true
		}
	}
	return nil
}

func sameMethodSignature(left, right methodSpec) bool {
	if left.HasContext != right.HasContext || left.ReturnKind != right.ReturnKind || left.Result != right.Result || len(left.Params) != len(right.Params) {
		return false
	}
	for i := range left.Params {
		if left.Params[i].GoType != right.Params[i].GoType {
			return false
		}
	}
	return true
}

func (p *parserState) parseMethodDecl(fn *ast.FuncDecl) (methodSpec, error) {
	spec := methodSpec{
		GoName:   fn.Name.Name,
		JSName:   jsName(fn.Name.Name, fn.Doc),
		ResultGo: renderResultGoType(p.fset, fn.Type.Results),
		RecvPtr:  isPointerReceiver(fn),
	}

	paramStart := 0
	if fn.Type.Params != nil && len(fn.Type.Params.List) > 0 && isGoCallContextType(fn.Type.Params.List[0].Type) {
		spec.HasContext = true
		paramStart = 1
		if qualifier, ok := goCallContextQualifier(fn.Type.Params.List[0].Type); ok {
			if p.goCallContextQualifier == "" {
				p.goCallContextQualifier = qualifier
			} else if p.goCallContextQualifier != qualifier {
				return methodSpec{}, fmt.Errorf("mixed GoCallContext qualifiers are not supported")
			}
		}
	}

	if fn.Type.Params != nil {
		for _, paramField := range fn.Type.Params.List[paramStart:] {
			tsType, err := p.renderTypeExpr(paramField.Type)
			if err != nil {
				return methodSpec{}, fmt.Errorf("param %s: %w", fieldNames(paramField.Names), err)
			}
			for _, name := range paramField.Names {
				spec.Params = append(spec.Params, paramSpec{
					Name:   name.Name,
					GoType: renderExpr(p.fset, paramField.Type),
					TSType: tsType,
				})
			}
		}
	}

	resultType, kind, err := p.parseResult(fn.Type.Results)
	if err != nil {
		return methodSpec{}, err
	}
	spec.Result = resultType
	spec.ReturnKind = kind
	spec.CallMode = classifyCallMode(kind)
	return spec, nil
}

func (p *parserState) parseResult(results *ast.FieldList) (string, returnKind, error) {
	if results == nil || len(results.List) == 0 {
		return "void", returnKindVoidNoError, nil
	}
	if len(results.List) == 1 {
		if renderExpr(p.fset, results.List[0].Type) == "error" {
			return "void", returnKindVoidError, nil
		}
		tsType, err := p.renderTypeExpr(results.List[0].Type)
		return tsType, returnKindValueOnly, err
	}
	if len(results.List) == 2 && renderExpr(p.fset, results.List[1].Type) == "error" {
		tsType, err := p.renderTypeExpr(results.List[0].Type)
		return tsType, returnKindValueAndError, err
	}
	return "", returnKindVoidNoError, fmt.Errorf("unsupported result signature")
}

func classifyCallMode(kind returnKind) callMode {
	switch kind {
	case returnKindValueOnly, returnKindValueAndError:
		return callModeRequest
	default:
		return callModeNotify
	}
}

func isPointerReceiver(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	_, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	return ok
}

func (p *parserState) ffiStorageType() string {
	if p.ffiNeedsPointer {
		return "*" + p.ffiTypeName
	}
	return p.ffiTypeName
}

func (p *parserState) ffiZeroValueExpr() string {
	if p.ffiNeedsPointer {
		return "&" + p.ffiTypeName + "{}"
	}
	return p.ffiTypeName + "{}"
}

func renderResultGoType(fset *token.FileSet, results *ast.FieldList) string {
	if results == nil || len(results.List) == 0 {
		return ""
	}
	return renderExpr(fset, results.List[0].Type)
}

func isGoCallContextType(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name == "GoCallContext"
	case *ast.SelectorExpr:
		return v.Sel != nil && v.Sel.Name == "GoCallContext"
	default:
		return false
	}
}

func goCallContextQualifier(expr ast.Expr) (string, bool) {
	switch v := expr.(type) {
	case *ast.Ident:
		if v.Name == "GoCallContext" {
			return "", true
		}
	case *ast.SelectorExpr:
		if v.Sel != nil && v.Sel.Name == "GoCallContext" {
			if ident, ok := v.X.(*ast.Ident); ok {
				return ident.Name, true
			}
		}
	}
	return "", false
}

func (p *parserState) renderTypeExpr(expr ast.Expr) (string, error) {
	tsExpr, err := p.tsExprFromGo(expr)
	if err != nil {
		return "", err
	}
	return renderTSExpr(tsExpr), nil
}

func (p *parserState) tsExprFromGo(expr ast.Expr) (*tsExpr, error) {
	switch v := expr.(type) {
	case *ast.Ident:
		switch v.Name {
		case "string":
			return &tsExpr{Kind: tsExprString}, nil
		case "bool":
			return &tsExpr{Kind: tsExprBoolean}, nil
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
			return &tsExpr{Kind: tsExprNumber}, nil
		case "any":
			return &tsExpr{Kind: tsExprUnknown}, nil
		}
		spec, ok := p.typeSpecs[v.Name]
		if !ok {
			return nil, fmt.Errorf("unsupported named type %q", v.Name)
		}
		if err := p.ensureTSDecl(v.Name, spec); err != nil {
			return nil, err
		}
		return &tsExpr{Kind: tsExprNamedRef, Name: v.Name}, nil
	case *ast.ArrayType:
		elem, err := p.tsExprFromGo(v.Elt)
		if err != nil {
			return nil, err
		}
		return &tsExpr{Kind: tsExprArray, Elem: elem}, nil
	case *ast.StarExpr:
		return p.tsExprFromGo(v.X)
	case *ast.StructType:
		return nil, fmt.Errorf("anonymous struct is not supported; define a named type")
	case *ast.MapType:
		key, err := p.tsExprFromGo(v.Key)
		if err != nil {
			return nil, err
		}
		value, err := p.tsExprFromGo(v.Value)
		if err != nil {
			return nil, err
		}
		return &tsExpr{Kind: tsExprMap, Key: key, Value: value}, nil
	case *ast.InterfaceType:
		return nil, fmt.Errorf("interface type is not supported in generated API; use any or a named DTO")
	case *ast.SelectorExpr:
		pkgIdent, ok := v.X.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("unsupported external type reference %q", renderExpr(p.fset, expr))
		}
		spec, err := p.resolveCrossPackageType(pkgIdent.Name, v.Sel.Name)
		if err != nil {
			return nil, err
		}
		if err := p.ensureTSDecl(v.Sel.Name, spec); err != nil {
			return nil, err
		}
		return &tsExpr{Kind: tsExprNamedRef, Name: v.Sel.Name}, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", renderExpr(p.fset, expr))
	}
}

func (p *parserState) ensureTSDecl(name string, spec *ast.TypeSpec) error {
	if _, ok := p.tsDecls[name]; ok {
		return nil
	}
	if p.resolvingTypes[name] {
		return fmt.Errorf("cyclic named type %s is not supported", name)
	}

	p.resolvingTypes[name] = true
	p.tsDecls[name] = &tsDecl{Name: name}
	p.tsDeclSeq = append(p.tsDeclSeq, name)
	defer delete(p.resolvingTypes, name)

	decl, err := p.buildTSDecl(name, spec.Type)
	if err != nil {
		delete(p.tsDecls, name)
		p.tsDeclSeq = p.tsDeclSeq[:len(p.tsDeclSeq)-1]
		return fmt.Errorf("%s: %w", name, err)
	}
	decl.Name = name
	p.tsDecls[name] = decl
	return nil
}

func (p *parserState) buildTSDecl(name string, expr ast.Expr) (*tsDecl, error) {
	switch v := expr.(type) {
	case *ast.StructType:
		fields := make([]tsField, 0, len(v.Fields.List))
		for _, field := range v.Fields.List {
			if len(field.Names) == 0 {
				return nil, fmt.Errorf("embedded fields are not supported")
			}
			fieldType, err := p.tsExprFromGo(field.Type)
			if err != nil {
				return nil, err
			}
			for _, fieldName := range field.Names {
				name := fieldName.Name
				optional := false
				if tag, omitempty := jsonTag(field.Tag); tag != "" {
					name = tag
					optional = omitempty
				}
				if name == "-" || name == "" {
					continue
				}
				fields = append(fields, tsField{
					Name:     name,
					Optional: optional,
					Type:     fieldType,
				})
			}
		}
		return &tsDecl{Kind: tsDeclInterface, Fields: fields}, nil
	case *ast.Ident:
		if isBuiltinScalar(v.Name) {
			target, err := p.tsExprFromGo(v)
			if err != nil {
				return nil, err
			}
			return &tsDecl{Kind: tsDeclAlias, Target: target}, nil
		}
		spec, ok := p.typeSpecs[v.Name]
		if !ok {
			return nil, fmt.Errorf("underlying type %q not found in package", v.Name)
		}
		if v.Name == name {
			return nil, fmt.Errorf("self-referential type alias is not supported")
		}
		if err := p.ensureTSDecl(v.Name, spec); err != nil {
			return nil, err
		}
		return &tsDecl{Kind: tsDeclAlias, Target: &tsExpr{Kind: tsExprNamedRef, Name: v.Name}}, nil
	case *ast.SelectorExpr:
		pkgIdent, ok := v.X.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("unsupported external type reference")
		}
		spec, err := p.resolveCrossPackageType(pkgIdent.Name, v.Sel.Name)
		if err != nil {
			return nil, err
		}
		if err := p.ensureTSDecl(v.Sel.Name, spec); err != nil {
			return nil, err
		}
		if v.Sel.Name == name {
			return p.buildTSDecl(name, spec.Type)
		}
		return &tsDecl{Kind: tsDeclAlias, Target: &tsExpr{Kind: tsExprNamedRef, Name: v.Sel.Name}}, nil
	default:
		target, err := p.tsExprFromGo(expr)
		if err != nil {
			return nil, err
		}
		if target.Kind == tsExprNamedRef && target.Name == name {
			return nil, fmt.Errorf("self-referential type alias is not supported")
		}
		return &tsDecl{Kind: tsDeclAlias, Target: target}, nil
	}
}

func isBuiltinScalar(name string) bool {
	switch name {
	case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64", "any":
		return true
	default:
		return false
	}
}

func renderTSExpr(expr *tsExpr) string {
	switch expr.Kind {
	case tsExprString:
		return "string"
	case tsExprNumber:
		return "number"
	case tsExprBoolean:
		return "boolean"
	case tsExprUnknown:
		return "unknown"
	case tsExprArray:
		return renderTSExpr(expr.Elem) + "[]"
	case tsExprMap:
		return fmt.Sprintf("Record<%s, %s>", renderTSExpr(expr.Key), renderTSExpr(expr.Value))
	case tsExprNamedRef:
		return expr.Name
	default:
		panic("unreachable ts expr kind")
	}
}

func renderTSDecl(decl *tsDecl, exportName string) string {
	if decl.Kind == tsDeclAlias {
		return fmt.Sprintf("export type %s = %s;\n\n", exportName, renderTSExpr(decl.Target))
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "export interface %s {\n", exportName)
	for _, field := range decl.Fields {
		marker := ""
		if field.Optional {
			marker = "?"
		}
		fmt.Fprintf(&buf, "    %s%s: %s;\n", field.Name, marker, renderTSExpr(field.Type))
	}
	buf.WriteString("}\n\n")
	return buf.String()
}

func stableBindingID(goFile string) (string, error) {
	data, err := os.ReadFile(goFile)
	if err != nil {
		return "", fmt.Errorf("read target go file for binding id: %w", err)
	}
	sum := md5.Sum(data)
	return "bind_" + base62Encode(sum[:]), nil
}

func base62Encode(data []byte) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

	value := new(big.Int).SetBytes(data)
	if value.Sign() == 0 {
		return "0"
	}

	base := big.NewInt(int64(len(alphabet)))
	quotient := new(big.Int)
	remainder := new(big.Int)
	encoded := make([]byte, 0)
	for value.Sign() > 0 {
		quotient.DivMod(value, base, remainder)
		encoded = append(encoded, alphabet[remainder.Int64()])
		value.Set(quotient)
	}
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}

func (p *parserState) renderDTO() []byte {
	var buf bytes.Buffer
	buf.WriteString("/**\n")
	buf.WriteString(" * @generated by bindgen -dto. DO NOT EDIT.\n")
	fmt.Fprintf(&buf, " * Source: Go type %s\n", p.typeName)
	buf.WriteString(" */\n\n")
	for _, name := range p.tsDeclSeq {
		exportName := name
		if name == p.typeName && p.tsClassName != "" {
			exportName = p.tsClassName
		}
		buf.WriteString(renderTSDecl(p.tsDecls[name], exportName))
	}
	return buf.Bytes()
}

func (p *parserState) renderGo() []byte {
	var buf bytes.Buffer
	corePrefix := p.goQualifierPrefix()
	goCallAPIType := "goCallAPI"

	buf.WriteString("// Code generated by gocallgen. DO NOT EDIT.\n")
	buf.WriteString("package " + p.pkgName + "\n\n")
	buf.WriteString("import (\n")
	buf.WriteString("\t\"context\"\n")
	buf.WriteString("\t\"encoding/json\"\n")
	buf.WriteString("\t\"fmt\"\n")
	buf.WriteString("\t\"sync\"\n")
	buf.WriteString("\t\"time\"\n\n")
	buf.WriteString("\t\"gopkg.d7z.net/cdp/internal/binding\"\n")
	if importPath := p.goCallContextImportPath(); importPath != "" {
		fmt.Fprintf(&buf, "\t%s %q\n", p.goCallContextQualifier, importPath)
	}
	buf.WriteString(")\n\n")
	fmt.Fprintf(&buf, "const goCallBindingName = %q\n", p.requestBindingName)
	fmt.Fprintf(&buf, "const goCallDeliverySlotName = %q\n\n", p.deliverySlotName)
	buf.WriteString("type goCallPayload struct {\n")
	buf.WriteString("\tKind string `json:\"kind\"`\n")
	buf.WriteString("\tSessionKey string `json:\"sessionKey\"`\n")
	buf.WriteString("\tCallID string `json:\"callId\"`\n")
	buf.WriteString("\tFuncName string `json:\"funcName\"`\n")
	buf.WriteString("\tTimeoutMS int `json:\"timeoutMs\"`\n")
	buf.WriteString("\tArgs []string `json:\"args\"`\n")
	buf.WriteString("}\n\n")
	buf.WriteString("type goCallResult struct {\n")
	buf.WriteString("\tSessionKey string `json:\"sessionKey\"`\n")
	buf.WriteString("\tCallID string `json:\"callId\"`\n")
	buf.WriteString("\tOK bool `json:\"ok\"`\n")
	buf.WriteString("\tResult any `json:\"result,omitempty\"`\n")
	buf.WriteString("\tError string `json:\"error,omitempty\"`\n")
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "type %s interface {\n", goCallAPIType)
	for _, method := range p.methods {
		params := make([]string, 0, len(method.Params)+1)
		if method.HasContext {
			params = append(params, corePrefix+"GoCallContext")
		}
		for _, param := range method.Params {
			params = append(params, param.GoType)
		}
		switch method.ReturnKind {
		case returnKindVoidNoError:
			fmt.Fprintf(&buf, "\t%s(%s)\n", method.GoName, strings.Join(params, ", "))
		case returnKindVoidError:
			fmt.Fprintf(&buf, "\t%s(%s) error\n", method.GoName, strings.Join(params, ", "))
		case returnKindValueOnly:
			fmt.Fprintf(&buf, "\t%s(%s) %s\n", method.GoName, strings.Join(params, ", "), method.ResultGo)
		case returnKindValueAndError:
			fmt.Fprintf(&buf, "\t%s(%s) (%s, error)\n", method.GoName, strings.Join(params, ", "), method.ResultGo)
		}
	}
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "type %s struct {\n\tFFI %s\n\trequests *sync.Map\n}\n\n", p.goBindingType, p.ffiStorageType())
	fmt.Fprintf(&buf, "func New%s() %s {\n", p.goBindingType, p.goBindingType)
	fmt.Fprintf(&buf, "\treturn %s{FFI: %s, requests: &sync.Map{}}\n", p.goBindingType, p.ffiZeroValueExpr())
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "func (%s) Name() string {\n", p.goBindingType)
	buf.WriteString("\treturn goCallBindingName\n")
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "func handleGoCallPayload(api %s, requests *sync.Map, ctx %sBindingContext, rawPayload string) error {\n", goCallAPIType, corePrefix)
	buf.WriteString("\tp := ctx.Page\n")
	buf.WriteString("\texecutionContextID := ctx.ExecutionContextID\n")
	buf.WriteString("\tvar payload goCallPayload\n")
	buf.WriteString("\tif err := json.Unmarshal([]byte(rawPayload), &payload); err != nil {\n")
	buf.WriteString("\t\treturn fmt.Errorf(\"parse go call payload: %w\", err)\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\trequestKey := payload.SessionKey + \"\\x00\" + payload.CallID\n")
	buf.WriteString("\tif payload.Kind == \"cancel\" {\n")
	buf.WriteString("\t\tif cancelValue, ok := requests.LoadAndDelete(requestKey); ok { cancelValue.(context.CancelFunc)() }\n")
	buf.WriteString("\t\treturn nil\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\tif ctx.Context == nil { ctx.Context = p }\n")
	buf.WriteString("\tif payload.Kind == \"request\" {\n")
	buf.WriteString("\t\ttimeout := time.Duration(payload.TimeoutMS) * time.Millisecond\n")
	buf.WriteString("\t\tif timeout <= 0 { timeout = 30 * time.Second }\n")
	buf.WriteString("\t\trequestCtx, cancel := context.WithTimeout(ctx.Context, timeout)\n")
	buf.WriteString("\t\tctx.Context = requestCtx\n")
	buf.WriteString("\t\trequests.Store(requestKey, context.CancelFunc(cancel))\n")
	buf.WriteString("\t\tdefer func() { requests.Delete(requestKey); cancel() }()\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\tvalue, err := dispatchGoCall(api, ctx, payload)\n")
	buf.WriteString("\tif payload.Kind != \"request\" {\n")
	buf.WriteString("\t\treturn err\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\tif payload.SessionKey == \"\" || payload.CallID == \"\" {\n")
	buf.WriteString("\t\treturn fmt.Errorf(\"request %s missing session key or call id\", payload.FuncName)\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\tresultPayload := goCallResult{SessionKey: payload.SessionKey, CallID: payload.CallID, OK: err == nil}\n")
	buf.WriteString("\tif err != nil {\n")
	buf.WriteString("\t\tresultPayload.Error = err.Error()\n")
	buf.WriteString("\t} else {\n")
	buf.WriteString("\t\tresultPayload.Result = value\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\tmarshal, marshalErr := json.Marshal(resultPayload)\n")
	buf.WriteString("\tif marshalErr != nil {\n")
	buf.WriteString("\t\treturn fmt.Errorf(\"marshal go call result %s: %w\", payload.FuncName, marshalErr)\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\thandled, deliverErr := p.DeliverBindingResultInSessionContext(ctx.Context, ctx.SessionID, executionContextID, goCallDeliverySlotName, payload.SessionKey, string(marshal))\n")
	buf.WriteString("\tif deliverErr != nil {\n")
	buf.WriteString("\t\treturn fmt.Errorf(\"deliver go call result func=%s call_id=%s: %w\", payload.FuncName, payload.CallID, deliverErr)\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\tif !handled {\n")
	buf.WriteString("\t\treturn nil\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\treturn nil\n")
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "func (b %s) Handle(ctx %sBindingContext, data *binding.BindingCalledEvent) error {\n", p.goBindingType, corePrefix)
	buf.WriteString("\tif b.requests == nil { b.requests = &sync.Map{} }\n")
	buf.WriteString("\treturn handleGoCallPayload(b.FFI, b.requests, ctx, data.Payload)\n")
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "func dispatchGoCall(api %s, ctx %sBindingContext, payload goCallPayload) (any, error) {\n", goCallAPIType, corePrefix)
	buf.WriteString("\tp := ctx.Page\n")
	buf.WriteString("\tmanager := ctx.Manager\n")
	buf.WriteString("\tswitch payload.FuncName {\n")
	for _, method := range p.methods {
		expectedKind := "notify"
		if method.CallMode == callModeRequest {
			expectedKind = "request"
		}
		fmt.Fprintf(&buf, "\tcase %q:\n", method.JSName)
		fmt.Fprintf(&buf, "\t\tif payload.Kind != %q {\n", expectedKind)
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s requires %s call mode, got %%s\", payload.Kind)\n", method.JSName, expectedKind)
		buf.WriteString("\t\t}\n")
		fmt.Fprintf(&buf, "\t\tif len(payload.Args) != %d {\n", len(method.Params))
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s expects %d args, got %%d\", len(payload.Args))\n", method.JSName, len(method.Params))
		buf.WriteString("\t\t}\n")
		for i, param := range method.Params {
			fmt.Fprintf(&buf, "\t\tvar arg%d %s\n", i, param.GoType)
			fmt.Fprintf(&buf, "\t\tif err := json.Unmarshal([]byte(payload.Args[%d]), &arg%d); err != nil {\n", i, i)
			fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"decode %s arg %s: %%w\", err)\n", method.JSName, param.Name)
			buf.WriteString("\t\t}\n")
		}
		callArgs := make([]string, 0, len(method.Params)+1)
		if method.HasContext {
			callArgs = append(callArgs, fmt.Sprintf("%sGoCallContext{Context: ctx.Context, Page: p, Manager: manager, SessionID: ctx.SessionID, TargetID: ctx.TargetID, TargetType: ctx.TargetType, ExecutionContextID: ctx.ExecutionContextID}", corePrefix))
		}
		for i := range method.Params {
			callArgs = append(callArgs, fmt.Sprintf("arg%d", i))
		}
		switch method.ReturnKind {
		case returnKindVoidNoError:
			fmt.Fprintf(&buf, "\t\tapi.%s(%s)\n", method.GoName, strings.Join(callArgs, ", "))
			buf.WriteString("\t\treturn nil, nil\n")
		case returnKindVoidError:
			fmt.Fprintf(&buf, "\t\treturn nil, api.%s(%s)\n", method.GoName, strings.Join(callArgs, ", "))
		case returnKindValueAndError:
			fmt.Fprintf(&buf, "\t\treturn api.%s(%s)\n", method.GoName, strings.Join(callArgs, ", "))
		case returnKindValueOnly:
			fmt.Fprintf(&buf, "\t\tvalue := api.%s(%s)\n", method.GoName, strings.Join(callArgs, ", "))
			buf.WriteString("\t\treturn value, nil\n")
		}
	}
	buf.WriteString("\tdefault:\n")
	buf.WriteString("\t\treturn nil, fmt.Errorf(\"unknown go call func: %s\", payload.FuncName)\n")
	buf.WriteString("\t}\n")
	buf.WriteString("}\n")
	return mustGoFormat(buf.Bytes())
}

func (p *parserState) goCallContextImportPath() string {
	if p.goCallContextQualifier == "" {
		return ""
	}
	return p.importPathsByAlias[p.goCallContextQualifier]
}

func (p *parserState) goQualifierPrefix() string {
	if p.goCallContextQualifier == "" {
		return ""
	}
	return p.goCallContextQualifier + "."
}

func (p *parserState) renderTS() []byte {
	var buf bytes.Buffer
	buf.WriteString("/**\n")
	buf.WriteString(" * @generated by gocallgen. DO NOT EDIT.\n")
	buf.WriteString(" */\n\n")
	for _, name := range p.tsDeclSeq {
		buf.WriteString(renderTSDecl(p.tsDecls[name], name))
	}
	fmt.Fprintf(&buf, "const goCallBindingName = %q;\n", p.requestBindingName)
	fmt.Fprintf(&buf, "const goCallDeliverySlotName = %q;\n\n", p.deliverySlotName)
	buf.WriteString("type PendingCall = {\n")
	buf.WriteString("    resolve: (value: any) => void;\n")
	buf.WriteString("    reject: (error: Error) => void;\n")
	buf.WriteString("    timeoutId: ReturnType<typeof setTimeout>;\n")
	buf.WriteString("};\n\n")
	buf.WriteString("type GoCallResultPayload = {\n")
	buf.WriteString("    sessionKey: string;\n")
	buf.WriteString("    callId: string;\n")
	buf.WriteString("    ok: boolean;\n")
	buf.WriteString("    result?: unknown;\n")
	buf.WriteString("    error?: string;\n")
	buf.WriteString("};\n\n")
	buf.WriteString("type GoCallChannel = {\n")
	buf.WriteString("    sessionKey: string;\n")
	buf.WriteString("    pending: Map<string, PendingCall>;\n")
	buf.WriteString("    destroyed: boolean;\n")
	buf.WriteString("    deliver: (rawPayload: string) => boolean;\n")
	buf.WriteString("    invalidate: (reason?: string) => void;\n")
	buf.WriteString("};\n\n")
	buf.WriteString("function randomKey(prefix: string): string {\n")
	buf.WriteString("    return `${prefix}${Date.now().toString(36)}_${Math.random().toString(36).slice(2)}`;\n")
	buf.WriteString("}\n\n")
	buf.WriteString("function globalStore(): Record<string, unknown> {\n")
	buf.WriteString("    return globalThis as unknown as Record<string, unknown>;\n")
	buf.WriteString("}\n\n")
	buf.WriteString("function setHiddenGlobal(name: string, value: unknown): void {\n")
	buf.WriteString("    Object.defineProperty(globalThis, name, {\n")
	buf.WriteString("        value,\n")
	buf.WriteString("        writable: true,\n")
	buf.WriteString("        configurable: true,\n")
	buf.WriteString("        enumerable: false,\n")
	buf.WriteString("    });\n")
	buf.WriteString("}\n\n")
	buf.WriteString("function channelError(message: string): Error {\n")
	buf.WriteString("    return new Error(`[CDP] ${message}`);\n")
	buf.WriteString("}\n\n")
	buf.WriteString("function isChannel(value: unknown): value is GoCallChannel {\n")
	buf.WriteString("    if (!value || typeof value !== \"object\") {\n")
	buf.WriteString("        return false;\n")
	buf.WriteString("    }\n")
	buf.WriteString("    const candidate = value as Partial<GoCallChannel>;\n")
	buf.WriteString("    return typeof candidate.sessionKey === \"string\" && candidate.pending instanceof Map && typeof candidate.deliver === \"function\" && typeof candidate.invalidate === \"function\";\n")
	buf.WriteString("}\n\n")
	buf.WriteString("function createChannel(): GoCallChannel {\n")
	buf.WriteString("    const store = globalStore();\n")
	buf.WriteString("    const previous = store[goCallDeliverySlotName];\n")
	buf.WriteString("    if (isChannel(previous)) {\n")
	buf.WriteString("        previous.invalidate(\"session replaced\");\n")
	buf.WriteString("    }\n")
	buf.WriteString("    const sessionKey = randomKey(`${goCallDeliverySlotName}__session_`);\n")
	buf.WriteString("    const pending = new Map<string, PendingCall>();\n")
	buf.WriteString("    const channel: GoCallChannel = {\n")
	buf.WriteString("        sessionKey,\n")
	buf.WriteString("        pending,\n")
	buf.WriteString("        destroyed: false,\n")
	buf.WriteString("        deliver: (rawPayload: string) => {\n")
	buf.WriteString("            let payload: GoCallResultPayload;\n")
	buf.WriteString("            try {\n")
	buf.WriteString("                payload = JSON.parse(rawPayload) as GoCallResultPayload;\n")
	buf.WriteString("            } catch (_err) {\n")
	buf.WriteString("                return false;\n")
	buf.WriteString("            }\n")
	buf.WriteString("            if (payload.sessionKey !== sessionKey) {\n")
	buf.WriteString("                return false;\n")
	buf.WriteString("            }\n")
	buf.WriteString("            const current = pending.get(payload.callId);\n")
	buf.WriteString("            if (!current) {\n")
	buf.WriteString("                return true;\n")
	buf.WriteString("            }\n")
	buf.WriteString("            clearTimeout(current.timeoutId);\n")
	buf.WriteString("            pending.delete(payload.callId);\n")
	buf.WriteString("            if (payload.ok) {\n")
	buf.WriteString("                current.resolve(payload.result);\n")
	buf.WriteString("            } else {\n")
	buf.WriteString("                current.reject(channelError(payload.error || \"go call failed\"));\n")
	buf.WriteString("            }\n")
	buf.WriteString("            return true;\n")
	buf.WriteString("        },\n")
	buf.WriteString("        invalidate: (reason?: string) => {\n")
	buf.WriteString("            if (channel.destroyed) {\n")
	buf.WriteString("                return;\n")
	buf.WriteString("            }\n")
	buf.WriteString("            channel.destroyed = true;\n")
	buf.WriteString("            for (const [callId, current] of pending.entries()) {\n")
	buf.WriteString("                clearTimeout(current.timeoutId);\n")
	buf.WriteString("                const binding = store[goCallBindingName];\n")
	buf.WriteString("                if (typeof binding === \"function\") binding(JSON.stringify({kind: \"cancel\", sessionKey, callId}));\n")
	buf.WriteString("                current.reject(channelError(reason ? `session invalidated: ${reason}` : `session invalidated (${callId})`));\n")
	buf.WriteString("            }\n")
	buf.WriteString("            pending.clear();\n")
	buf.WriteString("            if (store[goCallDeliverySlotName] === channel) {\n")
	buf.WriteString("                delete store[goCallDeliverySlotName];\n")
	buf.WriteString("            }\n")
	buf.WriteString("        },\n")
	buf.WriteString("    };\n")
	buf.WriteString("    setHiddenGlobal(goCallDeliverySlotName, channel);\n")
	buf.WriteString("    return channel;\n")
	buf.WriteString("}\n\n")
	buf.WriteString("function ensureChannel(): GoCallChannel {\n")
	buf.WriteString("    const existing = globalStore()[goCallDeliverySlotName];\n")
	buf.WriteString("    if (isChannel(existing) && !existing.destroyed) {\n")
	buf.WriteString("        return existing;\n")
	buf.WriteString("    }\n")
	buf.WriteString("    return createChannel();\n")
	buf.WriteString("}\n\n")
	buf.WriteString("function getBinding(funcName: string): (payload: string) => void {\n")
	buf.WriteString("    const binding = globalStore()[goCallBindingName];\n")
	buf.WriteString("    if (typeof binding !== \"function\") {\n")
	buf.WriteString("        throw channelError(`window.${goCallBindingName} not found for ${funcName}`);\n")
	buf.WriteString("    }\n")
	buf.WriteString("    return binding as (payload: string) => void;\n")
	buf.WriteString("}\n\n")
	buf.WriteString("function notify(funcName: string, ...args: unknown[]): void {\n")
	buf.WriteString("    const channel = ensureChannel();\n")
	buf.WriteString("    const binding = getBinding(funcName);\n")
	buf.WriteString("    binding(JSON.stringify({\n")
	buf.WriteString("        kind: \"notify\",\n")
	buf.WriteString("        sessionKey: channel.sessionKey,\n")
	buf.WriteString("        funcName,\n")
	buf.WriteString("        args: args.map((arg: unknown) => JSON.stringify(arg)),\n")
	buf.WriteString("    }));\n")
	buf.WriteString("}\n\n")
	buf.WriteString("function request<T>(funcName: string, ...args: unknown[]): Promise<T> {\n")
	buf.WriteString("    const channel = ensureChannel();\n")
	buf.WriteString("    const binding = getBinding(funcName);\n")
	buf.WriteString("    const callId = randomKey(`${goCallBindingName}__result_`);\n")
	buf.WriteString("    return new Promise<T>((resolve, reject) => {\n")
	buf.WriteString("        const timeoutId = setTimeout(() => {\n")
	buf.WriteString("            channel.pending.delete(callId);\n")
	buf.WriteString("            try { binding(JSON.stringify({kind: \"cancel\", sessionKey: channel.sessionKey, callId})); } catch (_err) {}\n")
	buf.WriteString("            reject(channelError(`Call to ${funcName} timed out`));\n")
	buf.WriteString("        }, 30000);\n")
	buf.WriteString("        channel.pending.set(callId, { resolve, reject, timeoutId });\n")
	buf.WriteString("        try {\n")
	buf.WriteString("            binding(JSON.stringify({\n")
	buf.WriteString("                kind: \"request\",\n")
	buf.WriteString("                sessionKey: channel.sessionKey,\n")
	buf.WriteString("                callId,\n")
	buf.WriteString("                timeoutMs: 30000,\n")
	buf.WriteString("                funcName,\n")
	buf.WriteString("                args: args.map((arg: unknown) => JSON.stringify(arg)),\n")
	buf.WriteString("            }));\n")
	buf.WriteString("        } catch (err) {\n")
	buf.WriteString("            clearTimeout(timeoutId);\n")
	buf.WriteString("            channel.pending.delete(callId);\n")
	buf.WriteString("            reject(err instanceof Error ? err : channelError(String(err)));\n")
	buf.WriteString("        }\n")
	buf.WriteString("    });\n")
	buf.WriteString("}\n\n")
	fmt.Fprintf(&buf, "export class %s {\n", p.tsClassName)
	buf.WriteString("    destroy(): void {\n")
	buf.WriteString("        const existing = globalStore()[goCallDeliverySlotName];\n")
	buf.WriteString("        if (isChannel(existing)) {\n")
	buf.WriteString("            existing.invalidate(\"destroy\");\n")
	buf.WriteString("        }\n")
	buf.WriteString("    }\n\n")
	for _, method := range p.methods {
		params := make([]string, 0, len(method.Params))
		argNames := make([]string, 0, len(method.Params))
		for _, param := range method.Params {
			params = append(params, fmt.Sprintf("%s: %s", param.Name, param.TSType))
			argNames = append(argNames, param.Name)
		}
		if method.CallMode == callModeNotify {
			fmt.Fprintf(&buf, "    %s(%s): void {\n", method.JSName, strings.Join(params, ", "))
			fmt.Fprintf(&buf, "        notify(%q%s);\n", method.JSName, tsArgSuffix(argNames))
			buf.WriteString("    }\n\n")
			continue
		}
		fmt.Fprintf(&buf, "    async %s(%s): Promise<%s> {\n", method.JSName, strings.Join(params, ", "), method.Result)
		fmt.Fprintf(&buf, "        return await request<%s>(%q%s);\n", method.Result, method.JSName, tsArgSuffix(argNames))
		buf.WriteString("    }\n\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes()
}

func renderExpr(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, expr)
	return buf.String()
}

func receiverTypeName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return receiverTypeName(v.X)
	default:
		return ""
	}
}

func jsName(name string, doc *ast.CommentGroup) string {
	if doc != nil {
		for _, comment := range doc.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if value, ok := strings.CutPrefix(text, "gocall:name "); ok {
				return strings.TrimSpace(value)
			}
		}
	}
	return strings.ToLower(name[:1]) + name[1:]
}

func hasGoCallDirective(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	for _, comment := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
		if strings.HasPrefix(text, "gocall:") {
			return true
		}
	}
	return false
}

func jsonTag(lit *ast.BasicLit) (string, bool) {
	if lit == nil || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	for _, part := range strings.Split(value, " ") {
		if !strings.HasPrefix(part, "json:\"") {
			continue
		}
		tag := strings.TrimSuffix(strings.TrimPrefix(part, "json:\""), "\"")
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "-" {
			return "-", false
		}
		omitempty := false
		for _, option := range parts[1:] {
			if option == "omitempty" {
				omitempty = true
				break
			}
		}
		return name, omitempty
	}
	return "", false
}

func (p *parserState) resolveCrossPackageType(importAlias, typeName string) (*ast.TypeSpec, error) {
	importPath, ok := p.importPathsByAlias[importAlias]
	if !ok {
		return nil, fmt.Errorf("import alias %q not found", importAlias)
	}

	if cache, ok := p.externalTypeCache[importPath]; ok {
		spec, ok := cache[typeName]
		if !ok {
			return nil, fmt.Errorf("type %q not found in package %q", typeName, importPath)
		}
		return spec, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	moduleRoot, modulePath, err := findModuleRoot(wd)
	if err != nil {
		return nil, err
	}
	pkgRel := strings.TrimPrefix(importPath, modulePath)
	pkgRel = strings.TrimPrefix(pkgRel, "/")
	pkgDir := filepath.Join(moduleRoot, pkgRel)
	if _, err := os.Stat(pkgDir); err != nil {
		return nil, fmt.Errorf("package directory %q not found for import %q", pkgDir, importPath)
	}

	_, files, _, err := parsePackageFiles(pkgDir, nil)
	if err != nil {
		return nil, fmt.Errorf("parse package %q: %w", importPath, err)
	}

	typeSpecs := make(map[string]*ast.TypeSpec)
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if ok {
					typeSpecs[typeSpec.Name.Name] = typeSpec
				}
			}
		}
	}
	p.externalTypeCache[importPath] = typeSpecs
	for name, spec := range typeSpecs {
		if _, exists := p.typeSpecs[name]; !exists {
			p.typeSpecs[name] = spec
		}
	}

	spec, ok := typeSpecs[typeName]
	if !ok {
		return nil, fmt.Errorf("type %q not found in package %q", typeName, importPath)
	}
	return spec, nil
}

func findModuleRoot(wd string) (string, string, error) {
	dir := wd
	for {
		modFile := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(modFile); err == nil {
			data, err := os.ReadFile(modFile)
			if err != nil {
				return "", "", err
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if mod, ok := strings.CutPrefix(line, "module "); ok {
					return dir, strings.TrimSpace(mod), nil
				}
			}
			return "", "", fmt.Errorf("module directive not found in go.mod")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("go.mod not found above %s", wd)
		}
		dir = parent
	}
}

func tsArgSuffix(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return ", " + strings.Join(args, ", ")
}

func fieldNames(names []*ast.Ident) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name.Name)
	}
	return strings.Join(parts, ", ")
}

func mustGoFormat(src []byte) []byte {
	formatted, err := format.Source(src)
	if err != nil {
		fail(err.Error())
	}
	return formatted
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
