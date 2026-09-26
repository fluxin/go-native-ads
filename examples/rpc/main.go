// RPC smoke harness. Calls methods in the explicitly configured test PLC.
package main

//go:generate go run ../../cmd/codegen/main.go -rpc=MAIN.rpc=RPCClient -o=generated.go -pkg=main
//go:generate go run ../../cmd/codegen/main.go -rpc=MAIN.rpcRefs=ReferenceClient -o=references_generated.go -pkg=main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	ads "github.com/fluxin/go-native-ads"
)

func main() {
	ip := flag.String("ip", "127.0.0.1", "AMS router IP")
	netID := flag.String("netid", "localhost", "target AMS Net ID")
	instance := flag.String("instance", "MAIN.rpc", "FB_RPC instance")
	references := flag.Bool("references", false, "also run reference and pointer buffer checks")
	referenceInstance := flag.String("reference-instance", "MAIN.rpcRefs", "FB_RPCReferences instance")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := ads.NewConnection(ctx, ads.ConnectionOptions{IP: *ip, NetID: *netID, AMSPort: 851})
	if err != nil {
		log.Fatal(err)
	}
	if err = conn.Connect(); err != nil {
		conn.Close()
		log.Fatal(err)
	}
	if err = runSmoke(ctx, conn, *instance); err != nil {
		conn.Close()
		log.Fatal(err)
	}
	if *references {
		if err = runReferenceSmoke(ctx, conn, *referenceInstance); err != nil {
			conn.Close()
			log.Fatal(err)
		}
	}
	conn.Close()
}
func runSmoke(ctx context.Context, conn *ads.Connection, instance string) error {
	metadata, err := conn.RPCMethods(instance)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	client, err := NewRPCClient(conn, instance)
	if err != nil {
		return err
	}
	result, err := client.Add(ctx, RPCClientAddInput{A: 7, B: 5})
	if err != nil {
		return err
	}
	if result.ReturnValue != 12 {
		return fmt.Errorf("Add returned %d, want 12", result.ReturnValue)
	}
	if err = client.Ping(ctx); err != nil {
		return err
	}
	fmt.Println("RPC smoke passed: Add(7,5)=12; Ping completed")
	return nil
}
