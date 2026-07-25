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

	// Companies (suppliers / manufacturers)
	RegisterSearchCompanies(server, c, r)
	RegisterCreateCompany(server, c, r)

	// Manufacturer parts (internal part <-> manufacturer MPN)
	RegisterListManufacturerParts(server, c, r)
	RegisterCreateManufacturerPart(server, c, r)

	// Supplier parts (internal part <-> vendor SKU)
	RegisterSearchSupplierParts(server, c, r)
	RegisterCreateSupplierPart(server, c, r)

	// Purchase orders
	RegisterListPurchaseOrders(server, c, r)
	RegisterGetPurchaseOrder(server, c, r)
	RegisterCreatePurchaseOrder(server, c, r)
	RegisterAddPurchaseOrderLine(server, c, r)
	RegisterIssuePurchaseOrder(server, c, r)
	RegisterReceivePurchaseOrder(server, c, r)

	// Build orders (manufacturing an assembly from its BOM)
	RegisterListBuildOrders(server, c, r)
	RegisterGetBuildOrder(server, c, r)
	RegisterCreateBuildOrder(server, c, r)
	RegisterIssueBuildOrder(server, c, r)
	RegisterAllocateBuildStock(server, c, r)
	RegisterAutoAllocateBuildStock(server, c, r)
	RegisterCreateBuildOutput(server, c, r)
	RegisterCompleteBuildOutputs(server, c, r)
	RegisterFinishBuildOrder(server, c, r)
	RegisterCancelBuildOrder(server, c, r)

	return r
}
