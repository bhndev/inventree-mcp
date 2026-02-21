package tools

import (
	"context"
	"fmt"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// StockItem represents an InvenTree stock item.
type StockItem struct {
	PK         int      `json:"pk"`
	Part       int      `json:"part"`
	Quantity   float64  `json:"quantity"`
	Serial     *string  `json:"serial"`
	Batch      string   `json:"batch"`
	Location   *int     `json:"location"`
	InStock    bool     `json:"in_stock"`
	Status     int      `json:"status"`
	StatusText string   `json:"status_text"`
	Notes      *string  `json:"notes"`
	Updated    string   `json:"updated"`
	Tags       []string `json:"tags"`
	PartDetail *struct {
		PK       int    `json:"pk"`
		Name     string `json:"name"`
		FullName string `json:"full_name"`
	} `json:"part_detail"`
}

// -- Get Stock --

type GetStockInput struct {
	Part     int `json:"part,omitempty" jsonschema:"Filter by part ID. 0 or omit to list all."`
	Location int `json:"location,omitempty" jsonschema:"Filter by location ID. 0 or omit to list all."`
	Limit    int `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
	Offset   int `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

func RegisterGetStock(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "get_stock",
		Description: "List stock items, optionally filtered by part ID and/or location ID. Returns stock quantities, locations, and status for each item.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetStockInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/stock/?limit=%d&offset=%d&format=json", limit, input.Offset)
		if input.Part != 0 {
			path += fmt.Sprintf("&part=%d", input.Part)
		}
		if input.Location != 0 {
			path += fmt.Sprintf("&location=%d", input.Location)
		}

		var resp client.PaginatedResponse[StockItem]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing stock: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get Stock Item --

type GetStockItemInput struct {
	ID int `json:"id" jsonschema:"The stock item ID (pk) to retrieve"`
}

func RegisterGetStockItem(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "get_stock_item",
		Description: "Get detailed information about a specific stock item by its ID.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetStockItemInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/stock/%d/?format=json", input.ID)
		var item StockItem
		if err := c.Get(path, &item); err != nil {
			return errResult(fmt.Errorf("getting stock item %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(item)
	})
}

// -- Add Stock (create a stock item) --

type AddStockInput struct {
	Part     int     `json:"part" jsonschema:"Part ID to add stock for (required)"`
	Quantity float64 `json:"quantity" jsonschema:"Quantity to add (required)"`
	Location int     `json:"location,omitempty" jsonschema:"Location ID where stock will be stored. 0 or omit for no location."`
	Batch    string  `json:"batch,omitempty" jsonschema:"Batch code"`
	Serial   string  `json:"serial,omitempty" jsonschema:"Serial number (for trackable parts)"`
	Notes    string  `json:"notes,omitempty" jsonschema:"Notes about this stock item"`
}

func RegisterAddStock(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "add_stock",
		Description: "Add stock by creating a new stock item. Requires a part ID and quantity. Optionally specify a location. Use search_parts to find the part ID and search_stock_locations to find the location ID first.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AddStockInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{
			"part":     input.Part,
			"quantity": input.Quantity,
		}
		if input.Location != 0 {
			payload["location"] = input.Location
		}
		if input.Batch != "" {
			payload["batch"] = input.Batch
		}
		if input.Serial != "" {
			payload["serial"] = input.Serial
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		// InvenTree returns an array of created stock items
		var created []StockItem
		if err := c.Post("/api/stock/", payload, &created); err != nil {
			return errResult(fmt.Errorf("adding stock: %w", err)), nil, nil
		}
		if len(created) == 0 {
			return errResult(fmt.Errorf("no stock items returned")), nil, nil
		}
		return jsonResult(created[0])
	})
}

// -- Update Stock Quantity --

type UpdateStockQuantityInput struct {
	Items []StockAdjustment `json:"items" jsonschema:"List of stock items to adjust"`
	Notes string            `json:"notes,omitempty" jsonschema:"Notes about this adjustment"`
}

type StockAdjustment struct {
	PK       int     `json:"pk" jsonschema:"Stock item ID"`
	Quantity float64 `json:"quantity" jsonschema:"Quantity to add (use stock_remove for subtraction)"`
}

func RegisterStockAdd(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "stock_add_quantity",
		Description: "Add quantity to existing stock items. Use this to increase stock levels without creating new stock entries. Provide the stock item PK (not part ID) and the quantity to add.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UpdateStockQuantityInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{
			"items": input.Items,
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var result any
		if err := c.Post("/api/stock/add/", payload, &result); err != nil {
			return errResult(fmt.Errorf("adding stock quantity: %w", err)), nil, nil
		}
		return textResult("Stock quantity updated successfully.")
	})
}

func RegisterStockRemove(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "stock_remove_quantity",
		Description: "Remove quantity from existing stock items. Provide the stock item PK (not part ID) and the quantity to remove.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UpdateStockQuantityInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{
			"items": input.Items,
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var result any
		if err := c.Post("/api/stock/remove/", payload, &result); err != nil {
			return errResult(fmt.Errorf("removing stock quantity: %w", err)), nil, nil
		}
		return textResult("Stock quantity removed successfully.")
	})
}

// -- Transfer Stock --

type TransferStockInput struct {
	Items    []StockAdjustment `json:"items" jsonschema:"List of stock items to transfer (pk and quantity)"`
	Location int               `json:"location" jsonschema:"Destination location ID"`
	Notes    string            `json:"notes,omitempty" jsonschema:"Notes about this transfer"`
}

func RegisterStockTransfer(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "stock_transfer",
		Description: "Transfer stock items to a different location. Moves the specified quantity of each stock item to the target location.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input TransferStockInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{
			"items":    input.Items,
			"location": input.Location,
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var result any
		if err := c.Post("/api/stock/transfer/", payload, &result); err != nil {
			return errResult(fmt.Errorf("transferring stock: %w", err)), nil, nil
		}
		return textResult("Stock transferred successfully.")
	})
}

// -- Delete Stock Item --

type DeleteStockItemInput struct {
	ID int `json:"id" jsonschema:"The stock item ID (pk) to delete"`
}

func RegisterDeleteStockItem(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "delete_stock_item",
		Description: "Delete a stock item. This is destructive and cannot be undone.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeleteStockItemInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/stock/%d/", input.ID)
		if err := c.Delete(path); err != nil {
			return errResult(fmt.Errorf("deleting stock item %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Stock item %d deleted successfully.", input.ID))
	})
}
