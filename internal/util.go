package internal

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

func ReadMemUsage(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			fmt.Println("=============================================")
			fmt.Printf("Alloc = %v MiB\n", m.Alloc/1024/1024)
			fmt.Printf("TotalAlloc = %v MiB\n", m.TotalAlloc/1024/1024)
			fmt.Printf("Sys = %v MiB\n", m.Sys/1024/1024)
			fmt.Printf("NumGC = %v\n", m.NumGC)
			fmt.Println("=============================================")
		}
	}
}

func RegisterPprof() {
	if err := http.ListenAndServe("localhost:5000", nil); err != nil {
		panic(err)
	}
}
