package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

// healthcheck performs one self-request against /api/health and returns a
// process exit code. The final Docker image is FROM scratch — there is no shell
// and no wget inside it — so the binary itself must act as the container
// health check via exec form: HEALTHCHECK ["CMD", "/zpwu", "-healthcheck"].
func healthcheck(addr string) int {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + probeHost(addr) + "/api/health")
	if err != nil {
		log.Printf("healthcheck request failed: %v", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("healthcheck: unexpected status %d", resp.StatusCode)
		return 1
	}
	fmt.Println("ok")
	return 0
}

// probeHost turns a listen address such as ":8080" or "0.0.0.0:80" into a
// dialable loopback address so the probe reaches this very process.
func probeHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1:8080"
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
