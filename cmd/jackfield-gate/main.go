package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/shreyansqt/jackfield/internal/gate"
)

func main() {
	var configPath string
	var profileName string
	var listenAddress string
	var upstreamEndpoint string

	flag.StringVar(&configPath, "config", "gate.example.json", "Path to the gate policy file")
	flag.StringVar(&profileName, "profile", "side-projects", "Policy profile to serve")
	flag.StringVar(&listenAddress, "listen", "127.0.0.1:8181", "Local listen address")
	flag.StringVar(&upstreamEndpoint, "upstream", "", "Upstream Streamable HTTP MCP endpoint")
	flag.Parse()

	policy, err := gate.LoadPolicy(configPath, profileName)
	if err != nil {
		log.Fatalf("The gate policy is invalid: %v", err)
	}

	handler := gate.NewHandler(policy)
	var upstream *gate.Upstream
	if upstreamEndpoint != "" {
		connectContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var tools []mcp.Tool
		for connectContext.Err() == nil {
			upstream, tools, err = gate.ConnectUpstream(connectContext, upstreamEndpoint)
			if err == nil {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if err != nil {
			log.Fatalf("The upstream MCP server is unavailable: %v", err)
		}
		defer upstream.Close()
		handler = gate.NewProxyHandler(policy, "jackfield-slack-"+profileName, upstream, tools)
		fmt.Fprintf(os.Stderr, "Jackfield loaded %d tools from %s\n", len(tools), upstreamEndpoint)
	}
	server := &http.Server{
		Addr:              listenAddress,
		Handler:           handler,
		ReadHeaderTimeout: gate.DefaultReadHeaderTimeout,
	}

	fmt.Fprintf(os.Stderr, "Jackfield gate profile %q listens at http://%s/mcp\n", profileName, listenAddress)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("The Jackfield gate stopped: %v", err)
	}
}
