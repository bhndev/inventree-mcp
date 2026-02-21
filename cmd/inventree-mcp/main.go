package main

import (
	"context"
	"log"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/config"
	"github.com/chrisbotelho/inventree-mcp/internal/imagesearch"
	"github.com/chrisbotelho/inventree-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	c := client.New(cfg.URL, cfg.Token)
	imgClient := imagesearch.New(cfg.GoogleAPIKey, cfg.GoogleCSEID)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "inventree-mcp",
		Version: "0.1.0",
	}, nil)

	registry := tools.RegisterAll(server, c, imgClient)
	server.AddReceivingMiddleware(registry.Middleware())

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
