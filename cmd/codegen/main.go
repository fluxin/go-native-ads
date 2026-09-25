package main

import (
	"context"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"

	ads "codeberg.org/fluxin/go-native-ads"
)

func init() {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))
}

var (
	ip          = flag.String("ip", "127.0.0.1", "the address to the AMS router")
	netid       = flag.String("netid", "localhost", "AMS NetID of the target (use 'localhost' for local)")
	port        = flag.Int("port", 48898, "AMS router TCP port")
	output      = flag.String("o", "generated_types.go", "Output file for generated Go code")
	symbols     = flag.String("symbols", "", "Comma-separated list of symbols to generate (e.g., MAIN.i,MAIN.b,MAIN.eeks)")
	packageName = flag.String("pkg", "main", "Package name for generated code")
)

func main() {
	flag.Parse()

	if *symbols == "" {
		fmt.Println("Usage: go run . -symbols=MAIN.i,MAIN.b,MAIN.eeks")
		fmt.Println("  -symbols: Comma-separated list of TwinCAT symbols")
		fmt.Println("  -o: Output file (default: generated_types.go)")
		fmt.Println("  -pkg: Package name (default: main)")
		os.Exit(1)
	}

	fmt.Printf("TwinCAT Go Code Generator\n")
	fmt.Printf("========================\n\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connection, err := ads.NewConnection(ctx, ads.ConnectionOptions{
		IP:      *ip,
		Port:    *port,
		NetID:   *netid,
		AMSPort: 851,
	})
	if err != nil {
		slog.Error("Failed to create connection", "error", err)
		os.Exit(1)
	}

	err = connection.Connect()
	if err != nil {
		slog.Error("Failed to connect", "error", err)
		os.Exit(1)
	}
	defer connection.Close()

	// Parse symbol list
	symbolList := strings.Split(*symbols, ",")
	for i := range symbolList {
		symbolList[i] = strings.TrimSpace(symbolList[i])
	}

	fmt.Printf("Analyzing %d symbols...\n\n", len(symbolList))

	// Generate types for each symbol
	var generatedTypes []string
	enumTypeNames := map[string]bool{}
	for _, symName := range symbolList {
		code, err := connection.GenerateType(symName)
		if err != nil {
			slog.Error("Failed to generate type", "symbol", symName, "error", err)
			connection.Close()
			os.Exit(1)
		}
		generatedTypes = append(generatedTypes, code)
		collectEnumTypeCandidates(connection, symName, enumTypeNames)

		// Get info for display
		info, err := connection.AnalyzeType(symName)
		if err == nil {
			fmt.Printf("✓ %s: %s -> %s\n", symName, info.Type, info.GoType)
		}
	}

	if len(generatedTypes) == 0 {
		slog.Error("No symbols to generate")
		os.Exit(1)
	}

	enumNames := make([]string, 0, len(enumTypeNames))
	for name := range enumTypeNames {
		enumNames = append(enumNames, name)
	}
	sort.Strings(enumNames)
	for _, typeName := range enumNames {
		enumCode, err := generateEnumCode(connection, typeName)
		if err != nil {
			continue
		}
		generatedTypes = append(generatedTypes, enumCode)
	}

	// Generate Go code
	fmt.Printf("\nGenerating Go code...\n")
	code, err := generateGoCode(generatedTypes, *packageName)
	if err != nil {
		slog.Error("Invalid generated source", "error", err)
		os.Exit(1)
	}

	// Write output file
	err = os.WriteFile(*output, []byte(code), 0o644)
	if err != nil {
		slog.Error("Failed to write output file", "error", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Generated %s with %d types\n", *output, len(generatedTypes))
}

func generateGoCode(typeCodes []string, pkg string) (string, error) {
	if !token.IsIdentifier(pkg) || token.Lookup(pkg).IsKeyword() {
		return "", fmt.Errorf("invalid package name %q", pkg)
	}
	body := "package " + pkg + "\n" + strings.Join(typeCodes, "\n")
	f, err := parser.ParseFile(token.NewFileSet(), "generated.go", body, 0)
	if err != nil {
		return "", err
	}
	imports := map[string]string{}
	ast.Inspect(f, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				switch id.Name {
				case "ads":
					imports["ads"] = "codeberg.org/fluxin/go-native-ads"
				case "time":
					imports["time"] = "time"
				}
			}
		}
		return true
	})
	names := map[string]bool{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			var ids []*ast.Ident
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				ids = []*ast.Ident{spec.Name}
			case *ast.ValueSpec:
				ids = spec.Names
			}
			for _, id := range ids {
				if names[id.Name] {
					return "", fmt.Errorf("generated name collision: %s", id.Name)
				}
				names[id.Name] = true
			}
		}
	}
	var fieldErr error
	ast.Inspect(f, func(node ast.Node) bool {
		st, ok := node.(*ast.StructType)
		if !ok {
			return true
		}
		seen := map[string]bool{}
		for _, field := range st.Fields.List {
			for _, name := range field.Names {
				if seen[name.Name] {
					fieldErr = fmt.Errorf("generated field collision: %s", name.Name)
				}
				seen[name.Name] = true
			}
		}
		return true
	})
	if fieldErr != nil {
		return "", fieldErr
	}
	var out strings.Builder
	out.WriteString("// Code generated by TwinCAT Go Code Generator. DO NOT EDIT.\n\npackage " + pkg + "\n")
	keys := make([]string, 0, len(imports))
	for key := range imports {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.WriteString("import " + key + " " + strconv.Quote(imports[key]) + "\n")
	}
	out.WriteString(strings.Join(typeCodes, "\n"))
	formatted, err := format.Source([]byte(out.String()))
	if err != nil {
		return "", err
	}
	return string(formatted), nil
}

func collectEnumTypeCandidates(conn *ads.Connection, symbolName string, out map[string]bool) {
	symbol, err := conn.GetSymbol(symbolName)
	if err != nil {
		return
	}
	collectSymbolDataTypes(symbol, out)
}

func collectSymbolDataTypes(symbol *ads.Symbol, out map[string]bool) {
	out[symbol.DataType] = true
	for _, child := range symbol.Children {
		collectSymbolDataTypes(child, out)
	}
}

func generateEnumCode(conn *ads.Connection, typeName string) (string, error) {
	enumInfo, err := conn.GetEnum(typeName)
	if err != nil {
		return "", err
	}
	goType := ads.Registry.GetGoType(enumInfo.BaseType)
	if goType == "interface{}" {
		return "", fmt.Errorf("unsupported enum base type %s", enumInfo.BaseType)
	}

	goTypeName := goName(enumInfo.Name)
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("// %s represents TwinCAT enum %s\n", goTypeName, enumInfo.Name))
	builder.WriteString(fmt.Sprintf("type %s %s\n\n", goTypeName, goType))
	builder.WriteString("const (\n")
	for _, name := range sortedEnumNames(enumInfo.Values) {
		builder.WriteString(fmt.Sprintf("\t%s %s = %d\n", enumMemberConstName(goTypeName, name), goTypeName, enumInfo.Values[name]))
	}
	builder.WriteString(")\n")
	return builder.String(), nil
}

func enumMemberConstName(enumTypeName string, memberName string) string {
	return fmt.Sprintf("%s_%s", enumTypeName, goName(memberName))
}

func sortedEnumNames(values map[string]int64) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		vi, vj := values[names[i]], values[names[j]]
		if vi == vj {
			return names[i] < names[j]
		}
		return vi < vj
	})
	return names
}

func goName(name string) string {
	var out strings.Builder
	upper := true
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			upper = true
			continue
		}
		if out.Len() == 0 && !unicode.IsLetter(r) {
			out.WriteByte('X')
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		out.WriteRune(r)
	}
	if out.Len() == 0 {
		return "X"
	}
	return out.String()
}
