package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	ads "codeberg.org/fluxin/go-native-ads"
)

func main() {
	var (
		plcIP          = flag.String("plc-ip", "", "PLC IP address")
		sendingNetID   = flag.String("sending-netid", "", "AMS NetID of this client")
		addingHostName = flag.String("adding-hostname", "", "Host name or label for the client route")
		username       = flag.String("username", "Administrator", "PLC username")
		password       = flag.String("password", "", "PLC password")
		routeName      = flag.String("route-name", "", "Route name on PLC (defaults to adding-hostname)")
		addedNetID     = flag.String("added-netid", "", "NetID to add on PLC (defaults to sending-netid)")
		discover       = flag.Bool("discover-netid", true, "Discover PLC NetID before adding route")
		timeout        = flag.Duration("timeout", 5*time.Second, "UDP timeout for route operation")
	)
	flag.Parse()

	if *plcIP == "" || *sendingNetID == "" || *addingHostName == "" {
		fmt.Println("Missing required flags.")
		fmt.Println("Required: -plc-ip -sending-netid -adding-hostname")
		os.Exit(2)
	}
	if err := validateAmsNetID(*sendingNetID); err != nil {
		fmt.Printf("Invalid -sending-netid: %v\n", err)
		fmt.Println("Expected format: a.b.c.d.e.f (six 0-255 integers)")
		os.Exit(2)
	}
	if *addedNetID != "" {
		if err := validateAmsNetID(*addedNetID); err != nil {
			fmt.Printf("Invalid -added-netid: %v\n", err)
			fmt.Println("Expected format: a.b.c.d.e.f (six 0-255 integers)")
			os.Exit(2)
		}
	}
	if *password == "" {
		fmt.Println("Warning: empty password provided; PLC may reject route creation")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *discover {
		netID, err := ads.DiscoverNetID(ctx, *plcIP)
		if err != nil {
			fmt.Printf("DiscoverNetID failed: %v\n", err)
		} else {
			fmt.Printf("DiscoverNetID(%s) = %s\n", *plcIP, netID)
		}
	}

	accepted, err := ads.AddRouteToPLC(ctx, ads.AddRouteToPLCRequest{
		SendingNetID:   *sendingNetID,
		AddingHostName: *addingHostName,
		PLCIP:          *plcIP,
		Username:       *username,
		Password:       *password,
		RouteName:      *routeName,
		AddedNetID:     *addedNetID,
	})
	if err != nil {
		fmt.Printf("AddRouteToPLC failed: %v\n", err)
		os.Exit(1)
	}

	if !accepted {
		fmt.Println("AddRouteToPLC rejected credentials or route request")
		os.Exit(1)
	}

	fmt.Println("AddRouteToPLC accepted")
}

func validateAmsNetID(netID string) error {
	parts := strings.Split(netID, ".")
	if len(parts) != 6 {
		return fmt.Errorf("must have 6 dot-separated parts, got %d", len(parts))
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("part %q is not an integer", p)
		}
		if n < 0 || n > 255 {
			return fmt.Errorf("part %q out of range 0..255", p)
		}
	}
	return nil
}
