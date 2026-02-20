package tools

import (
	"context"
	"fmt"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// StockLocation represents an InvenTree stock location.
type StockLocation struct {
	PK          int      `json:"pk"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Parent      *int     `json:"parent"`
	PathString  string   `json:"pathstring"`
	Level       int      `json:"level"`
	Items       int      `json:"items"`
	Sublocations int     `json:"sublocations"`
	Structural  bool     `json:"structural"`
	External    bool     `json:"external"`
	Icon        string   `json:"icon"`
	Tags        []string `json:"tags"`
}

// -- Search Stock Locations --

type SearchLocationsInput struct {
	Search string `json:"search" jsonschema:"Search query to find locations by name or description. Supports partial/fuzzy matching (e.g. 'green' finds 'Green 1')."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 25)"`
}

func RegisterSearchLocations(server *mcp.Server, c *client.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_stock_locations",
		Description: "Search for stock locations by name or description. Use this to find locations when the user gives an approximate name (e.g., 'green box 2' or 'office'). Returns matching locations with their full path (e.g., 'Office/Green 1'). The pathstring field shows the hierarchical location path.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchLocationsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 25
		}
		path := fmt.Sprintf("/api/stock/location/?search=%s&limit=%d&format=json", input.Search, limit)
		var resp client.PaginatedResponse[StockLocation]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching locations: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get Stock Location --

type GetLocationInput struct {
	ID int `json:"id" jsonschema:"The location ID (pk) to retrieve"`
}

func RegisterGetLocation(server *mcp.Server, c *client.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_stock_location",
		Description: "Get detailed information about a specific stock location by its ID, including its parent path and number of items.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetLocationInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/stock/location/%d/?format=json", input.ID)
		var loc StockLocation
		if err := c.Get(path, &loc); err != nil {
			return errResult(fmt.Errorf("getting location %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(loc)
	})
}

// -- List Stock Locations --

type ListLocationsInput struct {
	Parent *int `json:"parent,omitempty" jsonschema:"Filter by parent location ID. Use null/omit for top-level locations."`
	Limit  int  `json:"limit,omitempty" jsonschema:"Maximum number of results (default 100)"`
	Offset int  `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

func RegisterListLocations(server *mcp.Server, c *client.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_stock_locations",
		Description: "List all stock locations, optionally filtered by parent location. Returns the full location hierarchy with pathstrings. Use this to see all available locations and their structure.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListLocationsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 100
		}
		path := fmt.Sprintf("/api/stock/location/?limit=%d&offset=%d&format=json", limit, input.Offset)
		if input.Parent != nil {
			path += fmt.Sprintf("&parent=%d", *input.Parent)
		}

		var resp client.PaginatedResponse[StockLocation]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing locations: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Create Stock Location --

type CreateLocationInput struct {
	Name        string `json:"name" jsonschema:"Location name (required)"`
	Description string `json:"description,omitempty" jsonschema:"Location description"`
	Parent      *int   `json:"parent,omitempty" jsonschema:"Parent location ID. Omit for top-level location."`
	Structural  *bool  `json:"structural,omitempty" jsonschema:"If true, stock cannot be directly stored here (only in sub-locations)"`
}

func RegisterCreateLocation(server *mcp.Server, c *client.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_stock_location",
		Description: "Create a new stock location. Locations can be nested (e.g., Office > Green 1). Provide a parent ID to create a sub-location. Always search for existing locations first to avoid duplicates.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateLocationInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{
			"name": input.Name,
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Parent != nil {
			payload["parent"] = *input.Parent
		}
		if input.Structural != nil {
			payload["structural"] = *input.Structural
		}

		var created StockLocation
		if err := c.Post("/api/stock/location/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating location: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Delete Stock Location --

type DeleteLocationInput struct {
	ID int `json:"id" jsonschema:"The location ID (pk) to delete"`
}

func RegisterDeleteLocation(server *mcp.Server, c *client.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_stock_location",
		Description: "Delete a stock location. This is destructive and cannot be undone. The location must be empty (no stock items or sub-locations).",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeleteLocationInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/stock/location/%d/", input.ID)
		if err := c.Delete(path); err != nil {
			return errResult(fmt.Errorf("deleting location %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Location %d deleted successfully.", input.ID))
	})
}
