package tools

import (
	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/chrisbotelho/inventree-mcp/internal/imagesearch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterAll registers all InvenTree MCP tools with the server and returns
// a coerce.Registry populated with the schema types for each tool. Use the
// registry to install coercion middleware via registry.Middleware().
// imgClient may be nil if image search is not configured.
func RegisterAll(server *mcp.Server, c *client.Client, imgClient *imagesearch.Client) *coerce.Registry {
	r := coerce.NewRegistry()

	// Parts
	RegisterSearchParts(server, c, r)
	RegisterGetPart(server, c, r)
	RegisterCreatePart(server, c, r)
	RegisterUpdatePart(server, c, r)
	RegisterDeletePart(server, c, r)
	RegisterListParts(server, c, r)
	RegisterSetPartImage(server, c, r)
	RegisterSearchPartImages(server, imgClient, r)

	// Stock
	RegisterGetStock(server, c, r)
	RegisterGetStockItem(server, c, r)
	RegisterAddStock(server, c, r)
	RegisterStockAdd(server, c, r)
	RegisterStockRemove(server, c, r)
	RegisterStockTransfer(server, c, r)
	RegisterDeleteStockItem(server, c, r)

	// Locations
	RegisterSearchLocations(server, c, r)
	RegisterGetLocation(server, c, r)
	RegisterListLocations(server, c, r)
	RegisterCreateLocation(server, c, r)
	RegisterUpdateLocation(server, c, r)
	RegisterDeleteLocation(server, c, r)

	// Categories
	RegisterSearchCategories(server, c, r)
	RegisterListCategories(server, c, r)
	RegisterCreateCategory(server, c, r)
	RegisterUpdateCategory(server, c, r)
	RegisterDeleteCategory(server, c, r)

	return r
}
