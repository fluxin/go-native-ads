package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	ads "github.com/fluxin/go-native-ads"
)

func runReferenceSmoke(ctx context.Context, conn *ads.Connection, instance string) error {
	methods, err := conn.RPCMethods(instance)
	if err != nil {
		return err
	}
	metadata, err := json.MarshalIndent(methods, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(metadata))
	client, err := NewReferenceClient(conn, instance)
	if err != nil {
		return err
	}
	ref, err := client.Increment(ctx, ReferenceClientIncrementInput{Value: 41})
	if err != nil {
		return err
	}
	if ref.Value != 42 {
		return fmt.Errorf("Increment returned %d, want 42", ref.Value)
	}
	for _, values := range [][]int16{nil, {7}, {-2, 7, 5}} {
		var want int32
		for _, v := range values {
			want += int32(v)
		}
		sum, err := client.SumBuffer(ctx, ReferenceClientSumBufferInput{Count: uint16(len(values)), Values: values})
		if err != nil {
			return err
		}
		if sum.ReturnValue != want {
			return fmt.Errorf("SumBuffer returned %d, want %d", sum.ReturnValue, want)
		}
	}
	for _, count := range []uint16{0, 1, 3} {
		result, err := client.FillBuffer(ctx, ReferenceClientFillBufferInput{Count: count})
		if err != nil {
			return err
		}
		want := make([]int16, count)
		for i := range want {
			want[i] = int16(i * 2)
		}
		if !reflect.DeepEqual(result.Values, want) {
			return fmt.Errorf("FillBuffer returned %v, want %v", result.Values, want)
		}
	}
	fmt.Println("RPC reference smoke passed: Increment; SumBuffer and FillBuffer at lengths 0, 1, 3")
	return nil
}
