package ads

import (
	"fmt"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// GenerateRPCClient emits one typed client bound to the instance's function-block
// type. New<Client> accepts another instance with an identical method signature.
func (conn *Connection) GenerateRPCClient(instance, clientName string) (string, error) {
	return conn.GenerateRPCClients(map[string]string{instance: clientName})
}

// GenerateRPCClients emits clients and their shared dependent types once.
// The map associates PLC instance paths with exported Go client names.
func (conn *Connection) GenerateRPCClients(clients map[string]string) (string, error) {
	conn.generationLock.RLock()
	defer conn.generationLock.RUnlock()
	var names []string
	for name := range clients {
		names = append(names, name)
	}
	sort.Strings(names)
	deps := map[string]string{}
	var clientsCode strings.Builder
	var typeFor func(*Symbol) (string, error)
	typeFor = func(s *Symbol) (string, error) {
		if isArraySymbol(s) {
			children := getFieldsByOffset(s)
			if len(children) == 0 {
				return "", fmt.Errorf("empty array")
			}
			elem, err := typeFor(children[0])
			return fmt.Sprintf("[%d]%s", len(children), elem), err
		}
		if len(s.Children) > 0 {
			name := goName(s.DataType)
			if _, ok := deps[s.DataType]; !ok {
				// Record a visiting marker. Parser/layout validation already rejects cycles.
				deps[s.DataType] = ""
				var b strings.Builder
				fmt.Fprintf(&b, "// %s represents PLC %s.\ntype %s struct {\n", name, commentText(s.DataType), name)
				for _, field := range getFieldsByOffset(s) {
					typ, err := typeFor(field)
					if err != nil {
						return "", err
					}
					emitRPCField(&b, goName(field.Name), typ, field.Name, field.Comment)
				}
				b.WriteString("}\n")
				deps[s.DataType] = b.String()
			}
			return name, nil
		}
		if _, err := conn.GetEnum(s.DataType); err == nil {
			if _, ok := deps[s.DataType]; !ok {
				code, err := conn.GenerateEnum(s.DataType)
				if err != nil {
					return "", err
				}
				deps[s.DataType] = code
			}
			return goName(s.DataType), nil
		}
		typ := conn.generateGoTypeString(s, "")
		if strings.Contains(typ, "interface{}") {
			return "", fmt.Errorf("unsupported RPC datatype %s", s.DataType)
		}
		return typ, nil
	}
	for _, instance := range names {
		client := clients[instance]
		if !token.IsIdentifier(client) || !token.IsExported(client) || token.Lookup(client).IsKeyword() {
			return "", fmt.Errorf("invalid exported client name %q", client)
		}
		methods, types, err := conn.rpcMethods(instance)
		if err != nil {
			return "", err
		}
		// Stable output even if the server changes method-table order.
		methods = append([]RPCMethod(nil), methods...)
		sort.Slice(methods, func(i, j int) bool { return methods[i].Name < methods[j].Name })
		var records, fields, ctor, functions strings.Builder
		methodNames := make(map[string]bool)
		for i, m := range methods {
			method := goName(m.Name)
			if methodNames[method] {
				return "", fmt.Errorf("generated RPC method collision: %s.%s", client, method)
			}
			methodNames[method] = true
			in, out, signature, err := rpcSchema(m, types, conn.frameLimit())
			if err != nil {
				return "", err
			}
			inputName, outputName := client+method+"Input", client+method+"Result"
			for _, record := range []struct {
				s    *Symbol
				name string
			}{{in, inputName}, {out, outputName}} {
				fmt.Fprintf(&records, "type %s struct {\n", record.name)
				for _, f := range getFieldsByOffset(record.s) {
					typ, err := typeFor(f)
					if err != nil {
						return "", err
					}
					name := goName(f.Name)
					if f.Name == "$return" {
						name = "ReturnValue"
					}
					emitRPCField(&records, name, typ, f.Name, f.Comment)
				}
				records.WriteString("}\n")
			}
			fmt.Fprintf(&fields, "rpc%d *ads.RPC[%s,%s]\n", i, inputName, outputName)
			fmt.Fprintf(&ctor, "r.rpc%d, err = conn.BindRPC[%s,%s](instance,%q,ads.RPCOptions{Signature:%q})\nif err != nil { return nil, err }\n", i, inputName, outputName, m.Name, signature)
			fmt.Fprintf(&functions, "// %s calls PLC method %s.\n", method, commentText(m.Name))
			if m.Comment != "" {
				fmt.Fprintf(&functions, "// %s\n", commentText(m.Comment))
			}
			fmt.Fprintf(&functions, "func (c *%s) %s(ctx context.Context", client, method)
			arg := "input"
			if len(in.Children) > 0 {
				fmt.Fprintf(&functions, ", input %s", inputName)
			} else {
				arg = inputName + "{}"
			}
			if len(out.Children) == 0 {
				fmt.Fprintf(&functions, ") error { _, err := c.rpc%d.Call(ctx,%s); return err }\n", i, arg)
			} else {
				fmt.Fprintf(&functions, ") (%s,error) { return c.rpc%d.Call(ctx,%s) }\n", outputName, i, arg)
			}
		}
		clientsCode.WriteString(records.String())
		fmt.Fprintf(&clientsCode, "// %s is a typed PLC RPC client. Calls are never retried automatically.\ntype %s struct {\n%s}\n", client, client, fields.String())
		fmt.Fprintf(&clientsCode, "// New%s binds an instance and verifies its generated method signatures.\nfunc New%s(conn *ads.Connection,instance string)(*%s,error){\nr:=new(%s)\nvar err error\n%sreturn r,nil\n}\n", client, client, client, client, ctor.String())
		clientsCode.WriteString(functions.String())
	}
	var keys []string
	for key := range deps {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, key := range keys {
		out.WriteString(deps[key])
	}
	out.WriteString(clientsCode.String())
	// Validate all emitted names, including dependencies, constructors and methods.
	if _, err := FormatGeneratedCode([]string{out.String()}, "plc"); err != nil {
		return "", err
	}
	return out.String(), nil
}
func commentText(s string) string { return strings.Join(strings.Fields(s), " ") }
func emitRPCField(b *strings.Builder, name, typ, tag, comment string) {
	if comment != "" {
		fmt.Fprintf(b, "// %s\n", commentText(comment))
	}
	fmt.Fprintf(b, "%s %s %s\n", name, typ, strconv.Quote("ads:"+strconv.Quote(tag)))
}
