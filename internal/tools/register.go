package tools

import (
	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterAll registers all InvenTree MCP tools with the server.
func RegisterAll(server *mcp.Server, c *client.Client) {
	// Parts
	RegisterSearchParts(server, c)
	RegisterGetPart(server, c)
	RegisterCreatePart(server, c)
	RegisterUpdatePart(server, c)
	RegisterDeletePart(server, c)
	RegisterListParts(server, c)

	// Stock
	RegisterGetStock(server, c)
	RegisterGetStockItem(server, c)
	RegisterAddStock(server, c)
	RegisterStockAdd(server, c)
	RegisterStockRemove(server, c)
	RegisterStockTransfer(server, c)
	RegisterDeleteStockItem(server, c)

	// Locations
	RegisterSearchLocations(server, c)
	RegisterGetLocation(server, c)
	RegisterListLocations(server, c)
	RegisterCreateLocation(server, c)
	RegisterUpdateLocation(server, c)
	RegisterDeleteLocation(server, c)

	// Categories
	RegisterSearchCategories(server, c)
	RegisterListCategories(server, c)
	RegisterCreateCategory(server, c)
	RegisterUpdateCategory(server, c)
	RegisterDeleteCategory(server, c)
}
