package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	ads "github.com/fluxin/go-native-ads"
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
	rpcClients  = flag.String("rpc", "", "RPC clients as instance=Client pairs (e.g., MAIN.axis=Axis)")
	symbols     = flag.String("symbols", "", "Comma-separated list of symbols to generate (e.g., MAIN.i,MAIN.b,MAIN.eeks)")
	packageName = flag.String("pkg", "main", "Package name for generated code")
)

func main() {
	flag.Parse()

	if *symbols == "" && *rpcClients == "" {
		fmt.Println("Usage: go run . -symbols=MAIN.i,MAIN.b,MAIN.eeks")
		fmt.Println("  -rpc: RPC clients as instance=Client pairs")
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
		if symName == "" {
			continue
		}
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

	if *rpcClients != "" {
		clients, err := parseRPCClients(*rpcClients)
		if err != nil {
			slog.Error("Invalid RPC clients", "error", err)
			connection.Close()
			os.Exit(1)
		}
		code, err := connection.GenerateRPCClients(clients)
		if err != nil {
			slog.Error("Failed to generate RPC clients", "error", err)
			connection.Close()
			os.Exit(1)
		}
		generatedTypes = append(generatedTypes, code)
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

func generateGoCode(types []string, pkg string) (string, error) {
	return ads.FormatGeneratedCode(types, pkg)
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

func generateEnumCode(conn *ads.Connection, name string) (string, error) {
	return conn.GenerateEnum(name)
}

func parseRPCClients(value string) (map[string]string, error) {
	out := make(map[string]string)
	for _, item := range strings.Split(value, ",") {
		instance, name, ok := strings.Cut(strings.TrimSpace(item), "=")
		instance = strings.TrimSpace(instance)
		name = strings.TrimSpace(name)
		if !ok || instance == "" || name == "" {
			return nil, fmt.Errorf("expected instance=Client, got %q", item)
		}
		if _, ok := out[instance]; ok {
			return nil, fmt.Errorf("duplicate RPC instance %s", instance)
		}
		out[instance] = name
	}
	return out, nil
}
