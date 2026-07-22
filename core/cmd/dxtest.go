package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/soi/doctorx/core/internal/blockdev"
)

func main() {
	d, err := blockdev.ListExternalDisks(context.Background())
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	b, _ := json.MarshalIndent(d, "", "  ")
	fmt.Println(string(b))
}
